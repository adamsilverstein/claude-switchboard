package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// scanAgents reads the registry and marks liveness in one ps round trip.
func scanAgents() ([]registry.Agent, error) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return nil, err
	}
	agents, err := registry.Scan(dir)
	if err != nil {
		return nil, err
	}
	pids := make([]int, len(agents))
	for i, a := range agents {
		pids[i] = a.PID
	}
	procs, err := locate.Snapshot(pids)
	if err != nil {
		return nil, err
	}
	starts := make(map[int]time.Time, len(procs))
	for pid, p := range procs {
		starts[pid] = p.Start
	}
	registry.CheckLiveness(agents, starts)
	return agents, nil
}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include dead registry entries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tSTATUS\tAGE\tDIR\tNAME")
	now := time.Now()
	for _, a := range agents {
		if !a.Live && !*all {
			continue
		}
		status := a.Status
		if status == "" {
			status = "unknown"
		}
		if !a.Live {
			status = "dead"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n",
			a.PID, status, formatAge(now, statusTime(a)), shortDir(a.Cwd), a.Name)
	}
	return w.Flush()
}

// statusTime picks the freshest timestamp available for an agent.
func statusTime(a registry.Agent) time.Time {
	if !a.StatusUpdatedAt.IsZero() {
		return a.StatusUpdatedAt
	}
	if !a.UpdatedAt.IsZero() {
		return a.UpdatedAt
	}
	return a.StartedAt
}

func formatAge(now, t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func shortDir(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}
