package main

import (
	"fmt"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func runUI() error {
	projectsDir, err := activity.DefaultProjectsDir()
	if err != nil {
		return err
	}

	source := func() ([]ui.Row, error) {
		agents, err := scanAgents()
		if err != nil {
			return nil, err
		}
		agents = onScreen(target.ExecRunner{}, agents)
		rows := make([]ui.Row, 0, len(agents))
		for _, a := range agents {
			act := activity.For(projectsDir, a.Cwd, a.SessionID)
			age := statusTime(a)
			if age.IsZero() {
				age = act.Modified
			}
			rows = append(rows, ui.Row{
				Agent:   a,
				Name:    displayName(projectsDir, a, act),
				Summary: act.Summary,
				Age:     age,
			})
		}
		return rows, nil
	}

	focuser := func(a registry.Agent) (string, error) {
		loc := locateAgent(a)
		if err := target.Focus(target.ExecRunner{}, loc); err != nil {
			return "", err
		}
		return loc.Desc, nil
	}

	// The stop confirmation can sit for a while between ctrl+x and y, so
	// re-verify the PID still belongs to the registered process before
	// signaling: a reused PID must never receive the SIGTERM.
	stopper := func(a registry.Agent) error {
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

	_, err = tea.NewProgram(ui.New(source, focuser, stopper), tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("ui: %w", err)
	}
	return nil
}
