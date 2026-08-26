package main

import (
	"fmt"
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

// appSource builds the enriched rows the app window shows. It holds the git
// and forge caches across polls, which is the whole reason it is a value and
// not a function: the caches are what keep eighteen agents across four
// repositories down to four git invocations every ten seconds, and four gh
// invocations every two minutes.
type appSource struct {
	projectsDir   string
	statuslineDir string
	builder       appui.Builder
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
		builder: appui.Builder{
			ProjectsDir:   projectsDir,
			StatuslineDir: statuslineDir,
			Git:           git.NewCache(git.ExecRunner{}),
			Forge:         forge.NewCache(forge.ExecRunner{}),
		},
	}, nil
}

// account is the machine-wide usage the statusline shim recorded, if any.
func (s *appSource) account(now time.Time) appui.Account {
	return appui.AccountUsage(s.statuslineDir, now)
}

// rows scans the registry and enriches what it finds.
func (s *appSource) rows() ([]ui.Row, error) {
	agents, procs, err := scanAgentsWithProcs()
	if err != nil {
		return nil, err
	}
	agents = onScreen(target.ExecRunner{}, agents)
	ttys := make(map[int]string, len(procs))
	for pid, p := range procs {
		ttys[pid] = p.TTY
	}
	return s.builder.Rows(agents, ttys, func(a registry.Agent, act activity.Activity) string {
		return displayName(s.projectsDir, a, act)
	}), nil
}
