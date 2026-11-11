package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// fakeSweep records what a sweep did to the machine instead of doing it.
type fakeSweep struct {
	agents  map[int]registry.Agent
	done    map[int]bool
	dirty   map[string]bool
	stale   map[int]string // what a fresh look finds changed
	stopped []int
	closed  []int
	exits   bool          // whether stopped agents exit
	busyTTY bool          // whether something besides the shell stays on the terminal
	hang    chan struct{} // when set, closes block until it is closed

	mu sync.Mutex // closes run concurrently
}

func (f *fakeSweep) sweeper() sweeper {
	return sweeper{
		sweepable: func(pid int, session string) (ui.Row, bool) {
			a, ok := f.agents[pid]
			if !ok || a.SessionID != session {
				return ui.Row{}, false
			}
			return ui.Row{Agent: a}, f.done[pid]
		},
		recheck: func(r ui.Row) error {
			if why := f.stale[r.Agent.PID]; why != "" {
				return errors.New(why)
			}
			return nil
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
		gone:  func(a registry.Agent) bool { return f.exits },
		quiet: func(target.Location) (bool, error) { return !f.busyTTY, nil },
		close: func(loc target.Location) error {
			if f.hang != nil {
				<-f.hang
				return nil
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for pid, a := range f.agents {
				if loc.CloseCommands[0].Args[0] == a.SessionID {
					f.closed = append(f.closed, pid)
				}
			}
			return nil
		},
		wait:      20 * time.Millisecond,
		poll:      time.Millisecond,
		closeWait: 20 * time.Millisecond,
	}
}

var started = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

func sweepFixture() *fakeSweep {
	return &fakeSweep{
		agents: map[int]registry.Agent{
			1: {PID: 1, SessionID: "s1", Name: "merged", ProcStart: started, Cwd: "/clean"},
			2: {PID: 2, SessionID: "s2", Name: "dirty", ProcStart: started, Cwd: "/dirty"},
			3: {PID: 3, SessionID: "s3", Name: "reopened", ProcStart: started, Cwd: "/clean"},
			4: {PID: 4, SessionID: "s4", Name: "in tmux", ProcStart: started, Cwd: "/clean", Tmux: "x:@1.%1"},
			5: {PID: 5, SessionID: "s5", Name: "no repo", ProcStart: started, Cwd: "/broken"},
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

// A fresh look right before stopping overrules the row the page swept from.
func TestSweepSkipsAnAgentThatChangedSinceThePoll(t *testing.T) {
	f := sweepFixture()
	f.stale = map[int]string{1: "branch changed"}
	rep := f.sweeper().run(targets(1))
	if len(f.stopped) != 0 {
		t.Errorf("stopped %v after the branch changed", f.stopped)
	}
	if !strings.Contains(rep.String(), "merged (branch changed)") {
		t.Errorf("report %q does not say why it skipped", rep.String())
	}
}

// A pane can be shared: something else left running on the terminal after
// the agent exits keeps the session open.
func TestSweepLeavesASharedTerminalOpen(t *testing.T) {
	f := sweepFixture()
	f.busyTTY = true
	rep := f.sweeper().run(targets(1))
	if len(f.closed) != 0 {
		t.Errorf("closed %v with other processes on the terminal", f.closed)
	}
	if !strings.Contains(rep.String(), "other processes on its terminal, session left open") {
		t.Errorf("report %q does not say the session was left open", rep.String())
	}
}

// A PID with no recorded start could have been reused by anything.
func TestSweepSkipsAnAgentWithNoRecordedStart(t *testing.T) {
	f := sweepFixture()
	a := f.agents[1]
	a.ProcStart = time.Time{}
	f.agents[1] = a
	rep := f.sweeper().run(targets(1))
	if len(f.stopped) != 0 {
		t.Errorf("stopped %v with no recorded process start", f.stopped)
	}
	if !strings.Contains(rep.String(), "merged (no recorded process start)") {
		t.Errorf("report %q does not say why it skipped", rep.String())
	}
}

// A close that never returns, say an iTerm prompt nobody answers, must not
// keep the sweep from reporting.
func TestSweepReportsAStalledCloseAndReturns(t *testing.T) {
	f := sweepFixture()
	f.hang = make(chan struct{})
	defer close(f.hang)
	done := make(chan sweepReport)
	go func() { done <- f.sweeper().run(targets(1)) }()
	select {
	case rep := <-done:
		if !strings.Contains(rep.String(), "merged: close did not finish") {
			t.Errorf("report %q does not name the stalled close", rep.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the sweep waited on a close that never returned")
	}
}

func TestSweepMatchesTheSessionNotJustThePID(t *testing.T) {
	f := sweepFixture()
	f.sweeper().run([]appui.Target{{PID: 1, SessionID: "someone-else"}})
	if len(f.stopped) != 0 {
		t.Errorf("stopped %v for a PID whose session did not match", f.stopped)
	}
}
