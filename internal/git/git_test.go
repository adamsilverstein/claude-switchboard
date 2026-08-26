package git

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGit scripts git's answers and counts the calls, which is the thing
// worth asserting on: the cache exists to keep that number small.
type fakeGit struct {
	mu     sync.Mutex
	calls  []string
	top    string
	branch string
	dirty  string
	err    error
}

func (f *fakeGit) Run(name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	if f.err != nil {
		return "", f.err
	}
	for _, a := range args {
		if a == "--show-toplevel" {
			return f.top + "\n", nil
		}
		if a == "--abbrev-ref" {
			return f.branch + "\n", nil
		}
	}
	return f.dirty, nil
}

func (f *fakeGit) count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

// The first ask must not block, which means it must not have an answer yet.
func TestInfoIsEmptyUntilTheRefreshLands(t *testing.T) {
	g := &fakeGit{top: "/Users/x/repositories/gutenberg", dirty: " M lib/foo.php\n"}
	c := NewCache(g)

	if got := c.Info("/Users/x/repositories/gutenberg/lib"); got.Known {
		t.Errorf("first Info returned %+v; want an unknown answer", got)
	}
	c.Wait()

	got := c.Info("/Users/x/repositories/gutenberg/lib")
	if !got.Known || got.Repo != "gutenberg" || !got.Dirty {
		t.Errorf("Info = %+v, want {gutenberg true true}", got)
	}
}

func TestCleanRepositoryIsNotDirty(t *testing.T) {
	g := &fakeGit{top: "/Users/x/repositories/switchboard", dirty: "\n"}
	c := NewCache(g)
	c.Info("/Users/x/repositories/switchboard")
	c.Wait()

	got := c.Info("/Users/x/repositories/switchboard")
	if got.Repo != "switchboard" || got.Dirty {
		t.Errorf("Info = %+v, want {switchboard false true}", got)
	}
}

// Eighteen agents across four repositories must cost four resolutions, not
// eighteen, and repeated polls inside the TTL must cost none.
func TestOneResolutionPerDirectoryPerTTL(t *testing.T) {
	g := &fakeGit{top: "/Users/x/repositories/gutenberg"}
	now := time.Unix(1_800_000_000, 0)
	c := NewCache(g)
	c.now = func() time.Time { return now }

	dirs := []string{"/a", "/b", "/c", "/d"}
	for poll := 0; poll < 10; poll++ {
		for i := 0; i < 18; i++ {
			c.Info(dirs[i%len(dirs)])
		}
		c.Wait()
	}
	if n := g.count("--show-toplevel"); n != len(dirs) {
		t.Errorf("resolved %d times across 10 polls of 18 agents, want %d", n, len(dirs))
	}

	now = now.Add(TTL)
	for _, d := range dirs {
		c.Info(d)
	}
	c.Wait()
	if n := g.count("--show-toplevel"); n != 2*len(dirs) {
		t.Errorf("after the TTL expired, resolved %d times, want %d", n, 2*len(dirs))
	}
}

// A directory outside a repository is a settled question, not a retry loop.
func TestOutsideARepositoryResolvesToNothing(t *testing.T) {
	g := &fakeGit{err: errors.New("not a git repository")}
	now := time.Unix(1_800_000_000, 0)
	c := NewCache(g)
	c.now = func() time.Time { return now }

	c.Info("/tmp")
	c.Wait()
	got := c.Info("/tmp")
	if !got.Known {
		t.Error("want a Known answer so the caller stops asking within the TTL")
	}
	if got.Repo != "" || got.Dirty {
		t.Errorf("Info = %+v, want an empty answer", got)
	}
	for i := 0; i < 5; i++ {
		c.Info("/tmp")
	}
	c.Wait()
	if n := g.count("git"); n != 1 {
		t.Errorf("made %d git calls for a non-repository, want 1", n)
	}
}

func TestEmptyDirectoryNeverRunsGit(t *testing.T) {
	g := &fakeGit{}
	c := NewCache(g)
	if got := c.Info(""); got != (Info{}) {
		t.Errorf("Info(\"\") = %+v, want the zero Info", got)
	}
	c.Wait()
	if n := g.count("git"); n != 0 {
		t.Errorf("made %d git calls for an empty cwd, want 0", n)
	}
}

// Concurrent polls must not stampede git for the same directory.
func TestConcurrentAsksStartOneRefresh(t *testing.T) {
	g := &fakeGit{top: "/Users/x/repositories/gutenberg"}
	c := NewCache(g)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Info("/Users/x/repositories/gutenberg") }()
	}
	wg.Wait()
	c.Wait()
	if n := g.count("--show-toplevel"); n != 1 {
		t.Errorf("50 concurrent asks started %d resolutions, want 1", n)
	}
}

func ExampleCache_Info() {
	c := NewCache(ExecRunner{})
	c.Info(".") // starts a refresh; the answer arrives on a later poll
	c.Wait()
	info := c.Info(".")
	fmt.Println(info.Known)
	// Output: true
}
