package main

import (
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/appui"
	"github.com/adamsilverstein/claude-switchboard/internal/forge"
	"github.com/adamsilverstein/claude-switchboard/internal/git"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// focusAgent jumps to an agent's window and describes where it went.
func focusAgent(a registry.Agent) (string, error) {
	loc := locateAgent(a)
	if err := target.Focus(target.ExecRunner{}, loc); err != nil {
		return "", err
	}
	return loc.Desc, nil
}

// stopAgent sends SIGTERM, but only after re-verifying that the PID still
// belongs to the process the agent registered as.
//
// The gap matters in both front ends. In the picker the confirmation can sit
// for a while between ctrl+x and y; in the app window a row can be a second
// old and the agent already gone. Either way a reused PID must never receive
// the signal.
func stopAgent(a registry.Agent) error {
	procs, err := locate.Snapshot([]int{a.PID})
	if err != nil {
		return err
	}
	p, ok := procs[a.PID]
	if !ok || !registry.SameProcess(a, p.Start) {
		return fmt.Errorf("agent is gone; not signaling pid %d", a.PID)
	}
	return syscall.Kill(a.PID, syscall.SIGTERM)
}

// peekAgent reads an agent's last exchange for the peek panel.
func peekAgent(projectsDir string, a registry.Agent) (activity.Exchange, error) {
	if a.Codex() {
		return activity.Exchange{}, fmt.Errorf("peek reads Claude Code transcripts only")
	}
	ex, ok := activity.LastExchange(projectsDir, a.Cwd, a.SessionID)
	if !ok {
		return activity.Exchange{}, fmt.Errorf("no transcript found for this session")
	}
	return ex, nil
}

// replyAgent types text into an agent's terminal and presses return.
//
// It takes the same care stopAgent does, and more, because typing into the
// wrong terminal is worse than signaling the wrong process:
//
//   - The registry file is read again, so the idle-only rule is judged on
//     the status right now rather than the one the poll saw.
//   - The process is re-checked against its registry start time, and both
//     start times must be known. Listing tolerates a missing one; typing
//     does not.
//   - The tty comes from that fresh process check, and the terminal session
//     found for it is checked against it again at the moment of typing.
//
// Replies are serialized machine-wide, so two sent in quick succession are
// typed one after the other rather than interleaved ahead of a single
// return.
func replyAgent(a registry.Agent, text string) error {
	text, err := target.CleanReply(text)
	if err != nil {
		return err
	}
	replyMu.Lock()
	defer replyMu.Unlock()

	fresh, ok := registry.Reread(a)
	if !ok {
		return fmt.Errorf("agent is gone; not sending")
	}
	if why := ui.ReplyBlocked(fresh); why != "" {
		return fmt.Errorf("%s", why)
	}
	procs, err := locate.Snapshot([]int{fresh.PID})
	if err != nil {
		return err
	}
	p, ok := procs[fresh.PID]
	if !ok || fresh.ProcStart.IsZero() || p.Start.IsZero() || !registry.SameProcess(fresh, p.Start) {
		return fmt.Errorf("cannot confirm the agent is still the same process; not sending")
	}
	r := target.ExecRunner{}
	return target.SendReply(r, target.NewResolver(r).ResolveReply(fresh.Tmux, p.TTY), text)
}

var replyMu sync.Mutex

// appSource builds the enriched rows the app window shows. It holds the git
// and forge caches across polls, which is the whole reason it is a value and
// not a function: the caches are what keep eighteen agents across four
// repositories down to four git invocations every ten seconds, and four gh
// invocations every two minutes.
type appSource struct {
	projectsDir   string
	statuslineDir string
	builder       appui.Builder

	// windows is held for the same reason: it caches the iTerm
	// enumeration that says whether an agent's tty still has a window.
	windows *target.WindowIndex

	// ledger is held so each poll reads only what was appended since the
	// last one.
	ledger *statusline.Ledger
}

func newAppSource() (*appSource, error) {
	projectsDir, err := activity.DefaultProjectsDir()
	if err != nil {
		return nil, err
	}
	// A missing statusline directory is not an error: it is what a
	// machine without the shim looks like, and every surface that needed
	// it is omitted rather than blank.
	statuslineDir, _ := statusline.DefaultDir()

	return &appSource{
		projectsDir:   projectsDir,
		statuslineDir: statuslineDir,
		windows:       target.NewWindowIndex(target.ExecRunner{}, 0),
		ledger:        statusline.NewLedger(statuslineDir),
		builder: appui.Builder{
			ProjectsDir:   projectsDir,
			StatuslineDir: statuslineDir,
			Git:           git.NewCache(git.ExecRunner{}),
			Forge:         forge.NewCache(forge.ExecRunner{}),
			Subagents:     activity.NewSubagents(),
		},
	}, nil
}

// account is the machine-wide usage the statusline shim recorded, if any,
// the day's spend from its ledger, and the Codex account's windows from its
// rollouts.
func (s *appSource) account(now time.Time) appui.Account {
	return appui.AccountUsage(s.statuslineDir, now).
		WithToday(s.ledger.Day(now), now).
		WithCodex(codexScanner.Limits(now), now)
}

// rows scans the registry and enriches what it finds.
func (s *appSource) rows() ([]ui.Row, error) {
	agents, procs, err := scanAgentsWithProcs()
	if err != nil {
		return nil, err
	}
	ttys := ttysOf(procs)
	agents = onScreen(target.ExecRunner{}, s.windows, agents, ttys)
	return s.builder.Rows(agents, ttys, func(a registry.Agent, act activity.Activity) string {
		return displayName(s.projectsDir, a, act)
	}), nil
}
