package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
)

// fakeSweep records what a sweep did to the machine instead of doing it.
type fakeSweep struct {
	agents  map[int]registry.Agent
	done    map[int]bool
	dirty   map[string]bool
	stopped []int
	closed  []int
	exits   bool // whether stopped agents exit
}

func (f *fakeSweep) sweeper() sweeper {
	return sweeper{
		sweepable: func(pid int, session string) (registry.Agent, bool) {
			a, ok := f.agents[pid]
			if !ok || a.SessionID != session {
				return registry.Agent{}, false
			}
			return a, f.done[pid]
		},
		dirty: func(cwd string) (bool, error) {
			if cwd == "/broken" {
				return false, errors.New("not a git repository")
			}
			return f.dirty[cwd], nil
		},
		locate: func(a registry.Agent) target.Location {
			loc := target.Location{Focusable: true}
			if a.Tmux == "" {
				loc.CloseCommands = []target.Command{{Name: "close", Args: []string{a.SessionID}}}
			}
			return loc
		},
		stop: func(a registry.Agent) error {
			f.stopped = append(f.stopped, a.PID)
			return nil
		},
		gone: func(a registry.Agent) bool { return f.exits },
		close: func(loc target.Location) error {
			for pid, a := range f.agents {
				if loc.CloseCommands[0].Args[0] == a.SessionID {
					f.closed = append(f.closed, pid)
				}
			}
			return nil
		},
		wait: 20 * time.Millisecond,
		poll: time.Millisecond,
	}
}

func sweepFixture() *fakeSweep {
	return &fakeSweep{
		agents: map[int]registry.Agent{
			1: {PID: 1, SessionID: "s1", Name: "merged", Cwd: "/clean"},
			2: {PID: 2, SessionID: "s2", Name: "dirty", Cwd: "/dirty"},
			3: {PID: 3, SessionID: "s3", Name: "reopened", Cwd: "/clean"},
			4: {PID: 4, SessionID: "s4", Name: "in tmux", Cwd: "/clean", Tmux: "x:@1.%1"},
			5: {PID: 5, SessionID: "s5", Name: "no repo", Cwd: "/broken"},
		},
		done:  map[int]bool{1: true, 2: true, 4: true, 5: true},
		dirty: map[string]bool{"/dirty": true},
		exits: true,
	}
}

func targets(pids ...int) []appui.Target {
	var ts []appui.Target
	for _, p := range pids {
		ts = append(ts, appui.Target{PID: p, SessionID: "s" + string(rune('0'+p))})
	}
	return ts
}

func TestSweepStopsOnlyCleanDoneAgents(t *testing.T) {
	f := sweepFixture()
	rep := f.sweeper().run(targets(1, 2, 3, 4, 5))

	if got := f.stopped; len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Errorf("stopped %v, want [1 4]", got)
	}
	// The tmux agent is stopped but its host window stays: it holds the
	// whole tmux session.
	if got := f.closed; len(got) != 1 || got[0] != 1 {
		t.Errorf("closed %v, want [1]", got)
	}
	msg := rep.String()
	for _, want := range []string{
		"stopped 2", "closed 1 session",
		"dirty (uncommitted changes)",
		"reopened (no longer done)",
		"no repo (could not check for changes)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q is missing %q", msg, want)
		}
	}
}

// An agent that does not exit after SIGTERM keeps its session: closing it
// would hang up a process that is still shutting down.
func TestSweepLeavesASessionOpenWhileItsAgentRuns(t *testing.T) {
	f := sweepFixture()
	f.exits = false
	rep := f.sweeper().run(targets(1))
	if len(f.stopped) != 1 {
		t.Fatalf("stopped %v, want [1]", f.stopped)
	}
	if len(f.closed) != 0 {
		t.Errorf("closed %v while the agent was still running", f.closed)
	}
	if !strings.Contains(rep.String(), "still running, session left open") {
		t.Errorf("report %q does not say the session was left open", rep.String())
	}
}

func TestSweepMatchesTheSessionNotJustThePID(t *testing.T) {
	f := sweepFixture()
	f.sweeper().run([]appui.Target{{PID: 1, SessionID: "someone-else"}})
	if len(f.stopped) != 0 {
		t.Errorf("stopped %v for a PID whose session did not match", f.stopped)
	}
}
