package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

func runUI() error {
	projectsDir, err := activity.DefaultProjectsDir()
	if err != nil {
		return err
	}

	// The window index outlives each poll so the iTerm enumeration behind
	// it is cached across them rather than run every second.
	windows := target.NewWindowIndex(target.ExecRunner{}, 0)
	source := func() ([]ui.Row, error) {
		agents, procs, err := scanAgentsWithProcs()
		if err != nil {
			return nil, err
		}
		agents = onScreen(target.ExecRunner{}, windows, agents, ttysOf(procs))
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

	_, err = tea.NewProgram(ui.New(source, focusAgent, stopAgent), tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("ui: %w", err)
	}
	return nil
}
