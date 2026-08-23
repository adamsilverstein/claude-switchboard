package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/locate"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
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

// onScreen drops agents that are running but displayed nowhere: headless SDK
// sessions, which have no controlling terminal, and agents inside a detached
// tmux session, whose panes exist but are on no one's screen. Both are real
// processes the picker cannot take you to, so a row for either offers a
// destination that does not exist. The tmux query is skipped unless some
// agent claims to be inside tmux, and a failed query means "unknown", which
// keeps the agent listed rather than hiding it.
func onScreen(r target.Runner, agents []registry.Agent) []registry.Agent {
	attached, tmuxErr := map[string]bool{}, error(nil)
	if anyTmux(agents) {
		attached, tmuxErr = target.AttachedTmuxSessions(r)
	}
	kept := make([]registry.Agent, 0, len(agents))
	for _, a := range agents {
		if !a.Focusable() {
			continue
		}
		if a.Tmux != "" && tmuxErr == nil {
			if ref, ok := target.ParseTmuxRef(a.Tmux); ok && !attached[ref.Session] {
				continue
			}
		}
		kept = append(kept, a)
	}
	return kept
}

func anyTmux(agents []registry.Agent) bool {
	for _, a := range agents {
		if a.Tmux != "" {
			return true
		}
	}
	return false
}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include dead entries and agents with no terminal on screen")
	summary := fs.Bool("summary", false, "include a one-line summary from each agent's transcript")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agents, err := scanAgents()
	if err != nil {
		return err
	}
	if !*all {
		agents = onScreen(target.ExecRunner{}, agents)
	}
	projectsDir, err := activity.DefaultProjectsDir()
	if err != nil {
		return err
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
		act := activity.For(projectsDir, a.Cwd, a.SessionID)
		if age.IsZero() {
			age = act.Modified
		}
		row := ""
		if *summary {
			row = "\t" + ui.Truncate(act.Summary, 80)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s%s\n",
			a.PID, status, ui.FormatAge(now, age), ui.ShortDir(a.Cwd),
			displayName(projectsDir, a, act), row)
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
