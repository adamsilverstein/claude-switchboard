// Package git answers the questions the app window asks about a working
// directory: which repository it belongs to, which branch is checked out,
// and whether that repository has uncommitted work in it. All three need a
// subprocess, and one of them is slow on a large repository, so nothing here
// ever runs on the caller's goroutine.
package git

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Runner executes external commands. It exists so tests can script git's
// answers instead of needing a repository on disk.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ExecRunner runs git for real.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// Info is what git could say about a directory.
type Info struct {
	Repo   string // basename of the repository root; "" outside a repository
	Branch string // checked-out branch; "" outside a repository or on a detached HEAD
	Dirty  bool   // tracked files differ from HEAD
	Known  bool   // a resolution has completed for this directory
}

// TTL is how long an answer is reused before git is asked again. The dirty
// flag is the only thing here that changes minute to minute, and a ten
// second lag on it is not worth a subprocess per poll.
const TTL = 10 * time.Second

type entry struct {
	info     Info
	fetched  time.Time
	inFlight bool
}

// Cache resolves repository info per directory, at most once per TTL, and
// never on the caller's goroutine.
//
// Info is deliberately non-blocking: a poll asks for what is known right now
// and gets it, while a miss or a stale entry starts a refresh that a later
// poll will pick up. A first poll therefore shows no repository at all,
// which the UI renders as absent - the alternative is a 1s poll loop that
// stalls behind `git status` on a repository the size of gutenberg.
type Cache struct {
	run Runner
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

func NewCache(run Runner) *Cache {
	return &Cache{run: run, now: time.Now, entries: map[string]*entry{}}
}

// Info returns what is currently known about cwd, starting a background
// refresh if the answer is missing or older than TTL. It does not block.
func (c *Cache) Info(cwd string) Info {
	if cwd == "" {
		return Info{}
	}
	c.mu.Lock()
	e, ok := c.entries[cwd]
	if !ok {
		e = &entry{}
		c.entries[cwd] = e
	}
	stale := !e.info.Known || c.now().Sub(e.fetched) >= TTL
	start := stale && !e.inFlight
	if start {
		e.inFlight = true
	}
	info := e.info
	c.mu.Unlock()

	if start {
		go c.refresh(cwd)
	}
	return info
}

// Wait blocks until every refresh started so far has finished. Tests use it;
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

func (c *Cache) refresh(cwd string) {
	info := c.resolve(cwd)
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[cwd]
	e.info = info
	e.fetched = c.now()
	e.inFlight = false
}

// resolve asks git its three questions. A directory outside a repository, or a
// git that is not installed, yields a Known-but-empty answer: the question
// has been settled, there is just nothing to show.
func (c *Cache) resolve(cwd string) Info {
	info := Info{Known: true}
	top, err := c.run.Run("git", "-C", cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return info
	}
	top = strings.TrimSpace(top)
	if top == "" {
		return info
	}
	info.Repo = filepath.Base(top)

	// The branch is asked of git rather than taken from the transcript.
	// A transcript records the branch an agent was on when it last spoke,
	// which is the wrong answer the moment somebody checks out another
	// one - and it is the branch that decides which pull request the
	// column shows. A detached HEAD prints "HEAD", which names nothing.
	if out, err := c.run.Run("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if b := strings.TrimSpace(out); b != "HEAD" {
			info.Branch = b
		}
	}

	// --untracked-files=no on purpose. Untracked files are usually build
	// output and scratch files, and enumerating them is the expensive part
	// of `git status` on a large repository. The flag means "you have work
	// that is not committed", which is what the branch marker is for.
	out, err := c.run.Run("git", "-C", cwd, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return info
	}
	info.Dirty = strings.TrimSpace(out) != ""
	return info
}
