// Package forge answers one question about a working directory: which pull
// request or issue the branch checked out there belongs to.
//
// The answer only exists on GitHub, so it costs a `gh` invocation and a
// network round trip. That is far too slow to sit in a one second poll, so
// nothing here ever runs on the caller's goroutine and every answer is
// reused for minutes rather than seconds - a pull request's number does not
// change, and its state changes about as often as you press the merge
// button.
package forge

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Runner executes a command inside a directory. The directory is the whole
// point: gh works out which repository it is talking to from the git remote
// where it runs, so there is nothing to pass it and nothing to parse.
type Runner interface {
	RunIn(dir, name string, args ...string) (string, error)
}

// ExecRunner runs gh for real.
type ExecRunner struct{}

func (ExecRunner) RunIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// Ref is the pull request or issue a branch belongs to.
//
// Known separates "there is no pull request for this branch" from "nobody
// has asked yet". The first is an answer and renders as a dash; the second
// is a blank cell that is about to fill in.
type Ref struct {
	Number int
	Kind   string // "pr" or "issue"
	State  string // "open", "draft", "merged", "closed"
	Title  string
	URL    string
	Known  bool
}

// Label is what the column prints: "#13".
func (r Ref) Label() string {
	if r.Number == 0 {
		return ""
	}
	return "#" + strconv.Itoa(r.Number)
}

// TTL is how long an answer is reused before gh is asked again. Two minutes
// rather than the git cache's ten seconds because every field here is
// answered over the network, and none of them changes while you look away.
const TTL = 2 * time.Minute

type entry struct {
	ref      Ref
	fetched  time.Time
	inFlight bool
}

// Cache resolves a branch to its pull request or issue, at most once per
// branch per TTL, and never on the caller's goroutine.
type Cache struct {
	run Runner
	now func() time.Time

	mu       sync.Mutex
	entries  map[string]*entry
	disabled bool // gh is not installed; stop asking entirely
}

func NewCache(run Runner) *Cache {
	return &Cache{run: run, now: time.Now, entries: map[string]*entry{}}
}

// Ref returns what is currently known about the branch checked out in dir,
// starting a background lookup if the answer is missing or older than TTL.
// It does not block.
//
// The branch is not passed to gh, which reads it from dir itself. It is here
// because it is half the cache key: checking out another branch has to
// produce another answer, not the last one until the TTL runs out.
func (c *Cache) Ref(dir, branch string) Ref {
	if dir == "" || branch == "" {
		return Ref{}
	}
	k := dir + "\x00" + branch

	c.mu.Lock()
	if c.disabled {
		c.mu.Unlock()
		return Ref{Known: true}
	}
	e, ok := c.entries[k]
	if !ok {
		e = &entry{}
		c.entries[k] = e
	}
	stale := !e.ref.Known || c.now().Sub(e.fetched) >= TTL
	start := stale && !e.inFlight
	if start {
		e.inFlight = true
	}
	ref := e.ref
	c.mu.Unlock()

	if start {
		go c.refresh(k, dir, branch)
	}
	return ref
}

// Wait blocks until every lookup started so far has finished. Tests use it;
// the app window never does.
func (c *Cache) Wait() {
	for {
		c.mu.Lock()
		busy := false
		for _, e := range c.entries {
			if e.inFlight {
				busy = true
				break
			}
		}
		c.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (c *Cache) refresh(k, dir, branch string) {
	ref, missing := c.resolve(dir, branch)
	c.mu.Lock()
	defer c.mu.Unlock()
	if missing {
		// No gh on this machine. One failed exec is a diagnosis, not a
		// reason to keep trying every two minutes for every branch.
		c.disabled = true
	}
	e := c.entries[k]
	e.ref = ref
	e.fetched = c.now()
	e.inFlight = false
}

// resolve asks gh for the branch's pull request, and failing that treats a
// number in the branch name as an issue. It reports whether gh itself is
// missing, which is a different thing from gh having nothing to say.
//
// Every failure - no gh, not logged in, no remote, no network - produces a
// Known-but-empty answer. The question has been settled for this TTL; there
// is just nothing to show, and a column of dashes is the honest rendering.
//
// `pr status` rather than `pr list --head`, even though it fetches two lists
// nothing here reads and takes about a second on a repository the size of
// gutenberg. It is the only one of the two that asks the question the column
// is about. `--head` matches a head ref by name across every fork, so three
// agents sitting on gutenberg's trunk were all labelled with whichever fork
// had last opened a pull request from its own trunk: a real number, attached
// to the wrong agent, which is worse than an empty cell. `pr status` answers
// for the branch that is actually checked out, and answers nothing on a
// trunk - which is the right answer, because a trunk is not a piece of work.
func (c *Cache) resolve(dir, branch string) (ref Ref, ghMissing bool) {
	out, err := c.run.RunIn(dir, "gh", "pr", "status",
		"--json", "number,title,state,url,isDraft")
	if err != nil {
		return Ref{Known: true}, errors.Is(err, exec.ErrNotFound)
	}
	if pr, ok := currentBranchPR(out); ok {
		return pr, false
	}
	// No pull request. A branch named for the issue it closes is the only
	// other thing on disk that points anywhere, and it is worth following:
	// most of a session's life is spent before the pull request exists.
	n := issueFromBranch(branch)
	if n == 0 {
		return Ref{Known: true}, false
	}
	out, err = c.run.RunIn(dir, "gh", "issue", "view", strconv.Itoa(n),
		"--json", "number,title,state,url")
	if err != nil {
		return Ref{Known: true}, false
	}
	return issue(out), false
}

// ghPR is the shape gh prints. State arrives shouting - "OPEN", "MERGED" -
// and a draft is a separate flag rather than a state of its own.
type ghPR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	URL     string `json:"url"`
	IsDraft bool   `json:"isDraft"`
}

// currentBranchPR reads the one field of `gh pr status` that matters. A null
// there is an answer, not a gap: this branch has no pull request.
func currentBranchPR(out string) (Ref, bool) {
	var status struct {
		CurrentBranch *ghPR `json:"currentBranch"`
	}
	if json.Unmarshal([]byte(out), &status) != nil || status.CurrentBranch == nil {
		return Ref{}, false
	}
	p := *status.CurrentBranch
	if p.Number == 0 {
		return Ref{}, false
	}
	state := strings.ToLower(p.State)
	if p.IsDraft && state == "open" {
		state = "draft"
	}
	return Ref{Number: p.Number, Kind: "pr", State: state, Title: p.Title, URL: p.URL, Known: true}, true
}

func issue(out string) Ref {
	var i ghPR
	if json.Unmarshal([]byte(out), &i) != nil || i.Number == 0 {
		return Ref{Known: true}
	}
	return Ref{
		Number: i.Number, Kind: "issue", State: strings.ToLower(i.State),
		Title: i.Title, URL: i.URL, Known: true,
	}
}

// issueFromBranch reads an issue number out of a branch name, and is
// deliberately strict about it. "1234-retry-on-429" and "issue-11" name an
// issue; "add-oauth2-support" and "fix-3-bugs" do not, and guessing wrong
// links an agent to somebody else's ticket.
//
// The rule: in the last path component, either a leading run of digits, or a
// run of digits directly after "issue" or "gh", and in both cases the digits
// must end the component or be followed by a dash or an underscore. A dot is
// not a separator here, so "release/6.9" is a version and not issue 6.
func issueFromBranch(branch string) int {
	name := branch
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(name)

	if n, rest, ok := leadingDigits(name); ok && (rest == "" || isSep(rest[0])) {
		return n
	}
	for _, prefix := range []string{"issue", "gh"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := name[len(prefix):]
		if rest != "" && isSep(rest[0]) {
			rest = rest[1:]
		}
		if n, tail, ok := leadingDigits(rest); ok && (tail == "" || isSep(tail[0])) {
			return n
		}
	}
	return 0
}

func leadingDigits(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n == 0 {
		return 0, s, false
	}
	return n, s[i:], true
}

func isSep(b byte) bool { return b == '-' || b == '_' }
