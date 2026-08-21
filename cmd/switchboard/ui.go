package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
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
		rows := make([]ui.Row, 0, len(agents))
		for _, a := range agents {
			act := activity.For(projectsDir, a.Cwd, a.SessionID)
			age := statusTime(a)
			if age.IsZero() {
				age = act.Modified
			}
			rows = append(rows, ui.Row{Agent: a, Summary: act.Summary, Age: age})
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

	_, err = tea.NewProgram(ui.New(source, focuser), tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("ui: %w", err)
	}
	return nil
}
