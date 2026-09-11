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
	agents, _, err := scanAgentsWithProcs()
	return agents, err
}

// scanAgentsWithProcs is scanAgents plus what ps reported, for callers that
// want the controlling terminals too. The tty comes free from the call
// liveness already makes; asking for it separately would mean a second
// registry read and a second ps every poll.
func scanAgentsWithProcs() ([]registry.Agent, map[int]locate.Proc, error) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return nil, nil, err
	}
	agents, err := registry.Scan(dir)
	if err != nil {
		return nil, nil, err
	}
	pids := make([]int, len(agents))
	for i, a := range agents {
		pids[i] = a.PID
	}
	procs, err := locate.Snapshot(pids)
	if err != nil {
		return nil, nil, err
	}
	starts := make(map[int]time.Time, len(procs))
	for pid, p := range procs {
		starts[pid] = p.Start
	}
	registry.CheckLiveness(agents, starts)
	return agents, procs, nil
}

// ttysOf reduces a ps snapshot to the controlling terminal of each pid,
// which is all the window check needs.
func ttysOf(procs map[int]locate.Proc) map[int]string {
	ttys := make(map[int]string, len(procs))
	for pid, p := range procs {
		ttys[pid] = p.TTY
	}
	return ttys
}

// onScreen drops agents that are running but displayed nowhere: headless SDK
// sessions, which have no controlling terminal; agents inside a detached
// tmux session, whose panes exist but are on no one's screen; background
// sessions with no "claude agents" viewer open, whose pty belongs to the
// daemon; and agents whose tty no iTerm window owns, the orphans iTerm's
// server keeps running after a restart failed to re-adopt them. All are real
// processes the picker cannot take you to, so a row for any of them offers a
// destination that does not exist. The tmux, process table, and iTerm
// queries are skipped unless some agent needs them, and a failed query means
// "unknown", which keeps the agent listed rather than hiding it.
//
// ttys is what ps reported for each agent's pid, keyed by pid; an agent
// missing from it has no tty to check and is kept.
func onScreen(r target.Runner, windows *target.WindowIndex, agents []registry.Agent, ttys map[int]string) []registry.Agent {
	attached, tmuxErr := map[string]bool{}, error(nil)
	if anyTmux(agents) {
		attached, tmuxErr = target.AttachedTmuxSessions(r)
	}
	viewers, viewersErr := []string(nil), error(nil)
	if anyBackground(agents) {
		viewers, viewersErr = target.AgentViewers(r)
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
		if a.Background() && viewersErr == nil && len(viewers) == 0 {
			continue
		}
		// The tty of a tmux pane belongs to the tmux server and the tty
		// of a background session to the daemon, so neither is ever in
		// an iTerm window and both have already been judged above.
		if a.Tmux == "" && !a.Background() {
			if tty := ttys[a.PID]; tty != "" {
				if has, known := windows.Windowed(tty); known && !has {
					continue
				}
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

func anyBackground(agents []registry.Agent) bool {
	for _, a := range agents {
		if a.Background() {
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
	agents, procs, err := scanAgentsWithProcs()
	if err != nil {
		return err
	}
	if !*all {
		r := target.ExecRunner{}
		agents = onScreen(r, target.NewWindowIndex(r, 0), agents, ttysOf(procs))
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
