package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// Scanner finds live Codex sessions. It is a value, not a function, for
// the caches it keeps across polls: rollout heads never change once
// written, and a process's working directory rarely does, so neither is
// worth asking the disk or lsof for twice.
type Scanner struct {
	SessionsDir string // ~/.codex/sessions
	IndexPath   string // ~/.codex/session_index.jsonl, the thread names

	mu    sync.Mutex
	metas map[string]Rollout
	dirs  map[procKey]string

	names     map[string]string
	namesSize int64
	namesMod  time.Time

	// find and cwds are swapped out by tests.
	find func() []Proc
	cwds func([]int) map[int]string
}

type procKey struct {
	pid   int
	start time.Time
}

// NewScanner reads from ~/.codex, or from $CODEX_HOME when it is set.
func NewScanner() *Scanner {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".codex")
		}
	}
	return newScanner(home, findProcs, cwds)
}

func newScanner(home string, find func() []Proc, cwds func([]int) map[int]string) *Scanner {
	return &Scanner{
		SessionsDir: filepath.Join(home, "sessions"),
		IndexPath:   filepath.Join(home, "session_index.jsonl"),
		metas:       map[string]Rollout{},
		dirs:        map[procKey]string{},
		find:        find,
		cwds:        cwds,
	}
}

// Scan returns one live agent per running Codex CLI, with the tty ps
// reported for each keyed by pid, ready to merge with the registry's.
// A machine with no Codex installed costs one ps call.
func (s *Scanner) Scan() ([]registry.Agent, map[int]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	procs := s.find()
	if len(procs) == 0 {
		return nil, nil
	}
	s.fillCwds(procs)

	earliest := procs[0].Start
	for _, p := range procs[1:] {
		if p.Start.Before(earliest) {
			earliest = p.Start
		}
	}
	owned := match(procs, s.rollouts(earliest.Add(-startSlack)))
	names := s.threadNames()

	agents := make([]registry.Agent, 0, len(procs))
	ttys := make(map[int]string, len(procs))
	for _, p := range procs {
		a := registry.Agent{
			PID:        p.PID,
			Cwd:        p.Cwd,
			Entrypoint: registry.EntrypointCodex,
			ProcStart:  p.Start,
			StartedAt:  p.Start,
			Status:     "idle",
			Live:       true,
			Name:       "codex",
			NameSource: "derived",
		}
		if p.Cwd != "" {
			a.Name = filepath.Base(p.Cwd)
		}
		if r, ok := owned[p.PID]; ok {
			a.SessionID = r.ID
			a.Transcript = r.Path
			a.Cwd = r.Cwd
			if !r.Created.IsZero() {
				a.StartedAt = r.Created
			}
			if n := names[r.ID]; n != "" {
				a.Name, a.NameSource = n, ""
			}
			tail := ReadTail(r.Path)
			if tail.Busy {
				a.Status = "busy"
			}
			a.StatusUpdatedAt = tail.Updated
		}
		agents = append(agents, a)
		ttys[p.PID] = p.TTY
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].PID < agents[j].PID })
	return agents, ttys
}

// fillCwds sets each process's working directory, asking lsof only about
// the processes it has not seen before.
func (s *Scanner) fillCwds(procs []Proc) {
	var missing []int
	live := make(map[procKey]bool, len(procs))
	for _, p := range procs {
		k := procKey{p.PID, p.Start}
		live[k] = true
		if _, ok := s.dirs[k]; !ok {
			missing = append(missing, p.PID)
		}
	}
	if len(missing) > 0 {
		found := s.cwds(missing)
		for _, p := range procs {
			if d, ok := found[p.PID]; ok {
				s.dirs[procKey{p.PID, p.Start}] = d
			}
		}
	}
	for i := range procs {
		procs[i].Cwd = s.dirs[procKey{procs[i].PID, procs[i].Start}]
	}
	for k := range s.dirs {
		if !live[k] {
			delete(s.dirs, k)
		}
	}
}

// startSlack absorbs the second or so between a process starting and ps
// rounding its start time down, so a rollout written in the process's
// first moment is not mistaken for one that predates it.
const startSlack = 2 * time.Second

// match assigns each process the rollout it is writing.
//
// Nothing on disk names the process that owns a rollout, and a Codex
// process does not hold its rollout open between writes, so the join is
// inferred in two passes.
//
// First, new sessions. A rollout is created just after its process starts,
// in the process's directory, so it belongs to the process in that
// directory that started most recently before it. A process that has run
// /new owns several; it is on the one written most recently.
//
// Second, resumed sessions, whose rollout predates the process. A process
// the first pass left empty takes the most recently written rollout in its
// directory that no one else owns and that has been written since the
// process started.
//
// Each rollout goes to at most one process.
func match(procs []Proc, rollouts []Rollout) map[int]Rollout {
	owned := map[int]Rollout{}
	taken := map[string]bool{}
	for _, r := range rollouts {
		if !r.Interactive || r.Cwd == "" {
			continue
		}
		var owner *Proc
		for i := range procs {
			p := &procs[i]
			if p.Cwd != r.Cwd || r.Created.Before(p.Start.Add(-startSlack)) {
				continue
			}
			if owner == nil || p.Start.After(owner.Start) {
				owner = p
			}
		}
		if owner == nil {
			continue
		}
		taken[r.Path] = true
		if cur, ok := owned[owner.PID]; !ok || r.Modified.After(cur.Modified) {
			owned[owner.PID] = r
		}
	}

	// Newest rollout first, so a resumed session is matched with the
	// transcript it is writing now rather than an older one.
	sorted := append([]Rollout(nil), rollouts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Modified.After(sorted[j].Modified) })
	for _, p := range procs {
		if _, ok := owned[p.PID]; ok {
			continue
		}
		for _, r := range sorted {
			if !r.Interactive || taken[r.Path] || r.Cwd == "" || r.Cwd != p.Cwd {
				continue
			}
			if r.Modified.Before(p.Start.Add(-startSlack)) {
				continue
			}
			owned[p.PID] = r
			taken[r.Path] = true
			break
		}
	}
	return owned
}

// threadNames reads the names Codex gives its threads - "Review stacked
// PRs" - from the index it appends to whenever it names or renames one.
// Later lines win. The file is re-read only when it has changed.
func (s *Scanner) threadNames() map[string]string {
	info, err := os.Stat(s.IndexPath)
	if err != nil {
		return nil
	}
	if s.names != nil && info.Size() == s.namesSize && info.ModTime().Equal(s.namesMod) {
		return s.names
	}
	f, err := os.Open(s.IndexPath)
	if err != nil {
		return s.names
	}
	defer f.Close()
	names := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" && e.Name != "" {
			names[e.ID] = oneLine(e.Name)
		}
	}
	s.names, s.namesSize, s.namesMod = names, info.Size(), info.ModTime()
	return names
}
