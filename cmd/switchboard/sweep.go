package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/git"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
)

// sweeper stops the agents in the Done group and closes the iTerm sessions
// they leave behind. Every step it takes on the machine is injected, so the
// order of operations - and above all what it refuses to touch - is testable
// without signalling anything.
type sweeper struct {
	// sweepable is the controller's answer to "is this row still done".
	sweepable func(pid int, session string) (registry.Agent, bool)
	dirty     func(cwd string) (bool, error)
	locate    func(registry.Agent) target.Location
	stop      func(registry.Agent) error
	gone      func(registry.Agent) bool
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
		dirty:     func(cwd string) (bool, error) { return git.Dirty(git.ExecRunner{}, cwd) },
		locate:    locateAgent,
		stop:      stopAgent,
		gone:      agentGone,
		close:     func(loc target.Location) error { return target.Close(target.ExecRunner{}, loc) },
		wait:      sweepTimeout,
		poll:      100 * time.Millisecond,
	}
}

// agentGone reports whether an agent's process has exited, counting a PID
// that now belongs to some other process as exited.
func agentGone(a registry.Agent) bool {
	procs, err := locate.Snapshot([]int{a.PID})
	if err != nil {
		return false
	}
	p, ok := procs[a.PID]
	return !ok || !registry.SameProcess(a, p.Start)
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
	}
	var pending []stopped
	for _, t := range targets {
		a, ok := s.sweepable(t.PID, t.SessionID)
		if !ok {
			name := a.Name
			if name == "" {
				name = fmt.Sprintf("pid %d", t.PID)
			}
			rep.skipped = append(rep.skipped, name+" (no longer done)")
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
			pending = append(pending, stopped{a, loc})
		}
	}

	// One deadline for the lot, not one per agent: they were all
	// signalled together and exit in parallel.
	deadline := time.Now().Add(s.wait)
	for len(pending) > 0 {
		var still []stopped
		for _, p := range pending {
			if !s.gone(p.agent) {
				still = append(still, p)
				continue
			}
			if err := s.close(p.loc); err != nil {
				rep.failed = append(rep.failed, p.agent.Name+": "+err.Error())
				continue
			}
			rep.closed++
		}
		pending = still
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(s.poll)
	}
	for _, p := range pending {
		rep.failed = append(rep.failed, p.agent.Name+": still running, session left open")
	}
	return rep
}
