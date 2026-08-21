package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
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
	summary := fs.Bool("summary", false, "include a one-line summary from each agent's transcript")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}
	var projectsDir string
	if *summary {
		if projectsDir, err = activity.DefaultProjectsDir(); err != nil {
			return err
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	header := "PID\tSTATUS\tAGE\tDIR\tNAME"
	if *summary {
		header += "\tSUMMARY"
	}
	fmt.Fprintln(w, header)
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
		age := statusTime(a)
		row := ""
		if *summary {
			act := activity.For(projectsDir, a.Cwd, a.SessionID)
			if age.IsZero() {
				age = act.Modified
			}
			row = "\t" + truncate(act.Summary, 80)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s%s\n",
			a.PID, status, ui.FormatAge(now, age), shortDir(a.Cwd), a.Name, row)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func shortDir(dir string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}
