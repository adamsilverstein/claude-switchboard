package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/git"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// sweeper stops the agents in the Done group and closes the iTerm sessions
// they leave behind. Every step it takes on the machine is injected, so the
// order of operations - and above all what it refuses to touch - is testable
// without signalling anything.
type sweeper struct {
	// sweepable is the controller's answer to "is this row still done",
	// and recheck asks the machine again, past every cache, right before
	// the agent is stopped.
	sweepable func(pid int, session string) (ui.Row, bool)
	recheck   func(ui.Row) error
	dirty     func(cwd string) (bool, error)
	locate    func(registry.Agent) target.Location
	stop      func(registry.Agent) error
	gone      func(registry.Agent) bool
	quiet     func(target.Location) (bool, error)
	close     func(target.Location) error

	// wait is how long to give the stopped agents to exit before their
	// sessions are closed, and poll how often to look.
	wait time.Duration
	poll time.Duration
}

// sweepTimeout is how long a SIGTERMed agent gets to exit on its own. Claude
// Code usually goes in well under a second; a session still running after
// this is left open rather than hung up on.
const sweepTimeout = 5 * time.Second

func newSweeper(control *appui.Controller) sweeper {
	return sweeper{
		sweepable: control.Sweepable,
		recheck:   recheckDone,
		dirty:     func(cwd string) (bool, error) { return git.Dirty(git.ExecRunner{}, cwd) },
		locate:    locateAgent,
		stop:      stopAgent,
		gone:      agentGone,
		quiet:     func(loc target.Location) (bool, error) { return target.TTYQuiet(target.ExecRunner{}, loc.TTY) },
		close:     func(loc target.Location) error { return target.Close(target.ExecRunner{}, loc) },
		wait:      sweepTimeout,
		poll:      100 * time.Millisecond,
	}
}

// recheckDone reads the agent's registry entry and branch afresh. The row
// it was swept from can be up to a poll old, and an agent that took a prompt
// or a checkout that moved to new work in that time is not done any more.
func recheckDone(r ui.Row) error {
	a, ok := registry.Reread(r.Agent)
	if !ok {
		return errors.New("could not re-read its status")
	}
	if a.Status != "idle" {
		return errors.New("no longer idle")
	}
	b, err := git.Branch(git.ExecRunner{}, r.Agent.Cwd)
	if err != nil {
		return errors.New("could not check its branch")
	}
	if b != r.Telemetry.Branch {
		return errors.New("branch changed")
	}
	return nil
}

// agentGone reports whether an agent's process has exited. Only the kernel
// saying there is no such process counts; a ps that failed or came back
// empty proves nothing, and closing on it could hang up an agent still
// shutting down. A PID that now belongs to some other process also counts as
// exited.
func agentGone(a registry.Agent) bool {
	err := syscall.Kill(a.PID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		return false
	}
	procs, err := locate.Snapshot([]int{a.PID})
	if err != nil {
		return false
	}
	p, ok := procs[a.PID]
	return ok && !registry.SameProcess(a, p.Start)
}

// sweepReport is what a sweep did, for the notice line.
type sweepReport struct {
	stopped []string
	closed  int
	skipped []string // "name (reason)"
	failed  []string // "name: error"
}

func (r sweepReport) String() string {
	var parts []string
	if n := len(r.stopped); n > 0 {
		s := fmt.Sprintf("stopped %d", n)
		if r.closed > 0 {
			s += fmt.Sprintf(", closed %d session", r.closed)
			if r.closed != 1 {
				s += "s"
			}
		}
		parts = append(parts, s)
	}
	if len(r.skipped) > 0 {
		parts = append(parts, "skipped "+strings.Join(r.skipped, ", "))
	}
	if len(r.failed) > 0 {
		parts = append(parts, "failed "+strings.Join(r.failed, "; "))
	}
	if len(parts) == 0 {
		return "nothing swept"
	}
	return "sweep: " + strings.Join(parts, " · ")
}

// run sweeps the agents you confirmed. Each is checked again here, because
// the confirmation can sit on screen for a while: an agent that has picked
// up new work, or has uncommitted changes, is skipped and named.
//
// Everything is located before anything is stopped. The tty that joins an
// agent to its session is read from its process, and once the process has
// exited there is nothing left to read it from.
func (s sweeper) run(targets []appui.Target) sweepReport {
	var rep sweepReport
	type stopped struct {
		agent registry.Agent
		loc   target.Location
		why   string // why the session is still open, if it stays that way
	}
	var pending []stopped
	for _, t := range targets {
		r, ok := s.sweepable(t.PID, t.SessionID)
		a := r.Agent
		if !ok {
			name := a.Name
			if name == "" {
				name = fmt.Sprintf("pid %d", t.PID)
			}
			rep.skipped = append(rep.skipped, name+" (no longer done)")
			continue
		}
		// Without a recorded start time a reused PID cannot be told
		// apart from the agent, and a bulk stop is no place to guess.
		if a.ProcStart.IsZero() {
			rep.skipped = append(rep.skipped, a.Name+" (no recorded process start)")
			continue
		}
		if err := s.recheck(r); err != nil {
			rep.skipped = append(rep.skipped, a.Name+" ("+err.Error()+")")
			continue
		}
		dirty, err := s.dirty(a.Cwd)
		switch {
		case err != nil:
			rep.skipped = append(rep.skipped, a.Name+" (could not check for changes)")
			continue
		case dirty:
			rep.skipped = append(rep.skipped, a.Name+" (uncommitted changes)")
			continue
		}
		loc := s.locate(a)
		if err := s.stop(a); err != nil {
			rep.failed = append(rep.failed, a.Name+": "+err.Error())
			continue
		}
		rep.stopped = append(rep.stopped, a.Name)
		if len(loc.CloseCommands) > 0 {
			pending = append(pending, stopped{agent: a, loc: loc, why: "still running"})
		}
	}

	// One deadline for the lot, not one per agent: they were all
	// signalled together and exit in parallel. A session is closed once
	// its agent has exited and nothing but the shell is left on its
	// terminal; the closes run side by side, so one that iTerm stalls on
	// does not hold up the rest.
	var mu sync.Mutex
	deadline := time.Now().Add(s.wait)
	for len(pending) > 0 {
		var still, ready []stopped
		for _, p := range pending {
			if !s.gone(p.agent) {
				p.why = "still running"
				still = append(still, p)
				continue
			}
			quiet, err := s.quiet(p.loc)
			switch {
			case err != nil:
				rep.failed = append(rep.failed, p.agent.Name+": could not check its terminal, session left open")
			case !quiet:
				p.why = "other processes on its terminal"
				still = append(still, p)
			default:
				ready = append(ready, p)
			}
		}
		var wg sync.WaitGroup
		for _, p := range ready {
			wg.Add(1)
			go func(p stopped) {
				defer wg.Done()
				err := s.close(p.loc)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					rep.failed = append(rep.failed, p.agent.Name+": "+err.Error())
					return
				}
				rep.closed++
			}(p)
		}
		wg.Wait()
		pending = still
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(s.poll)
	}
	for _, p := range pending {
		rep.failed = append(rep.failed, p.agent.Name+": "+p.why+", session left open")
	}
	return rep
}
