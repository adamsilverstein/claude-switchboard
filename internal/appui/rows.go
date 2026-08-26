package appui

import (
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/forge"
	"github.com/adamsilverstein/claude-switchboard/internal/git"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// Builder turns registry entries into the enriched rows the app window
// shows. It exists so the enrichment is testable without a home directory
// full of transcripts: every source it reads from is a field.
type Builder struct {
	ProjectsDir   string       // ~/.claude/projects
	StatuslineDir string       // ~/.claude/switchboard/statusline
	Git           *git.Cache   // may be nil, in which case no repository info
	Forge         *forge.Cache // may be nil, in which case no pull requests
}

// Rows enriches agents with everything the app window can learn about them.
// Nothing here blocks: the transcript read is bounded, the statusline files
// are small, and the git cache answers from memory and refreshes behind us.
func (b Builder) Rows(agents []registry.Agent, ttys map[int]string, name func(registry.Agent, activity.Activity) string) []ui.Row {
	rows := make([]ui.Row, 0, len(agents))
	for _, a := range agents {
		act := activity.For(b.ProjectsDir, a.Cwd, a.SessionID)
		age := agentAge(a, act)
		rows = append(rows, ui.Row{
			Agent:     a,
			Name:      name(a, act),
			Summary:   act.Summary,
			Age:       age,
			Telemetry: b.telemetry(a, act, ttys[a.PID]),
		})
	}
	return rows
}

// agentAge picks the freshest timestamp that describes the agent, in the
// same order the terminal picker uses so the AGE column reads the same in
// both windows.
func agentAge(a registry.Agent, act activity.Activity) time.Time {
	for _, t := range []time.Time{a.StatusUpdatedAt, a.UpdatedAt, a.StartedAt, act.Modified} {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func (b Builder) telemetry(a registry.Agent, act activity.Activity, tty string) ui.Telemetry {
	t := ui.Telemetry{
		Model:          activity.ModelDisplayName(act.Model),
		ContextTokens:  act.ContextTokens,
		PermissionMode: act.PermissionMode,
		Branch:         act.GitBranch,
		TTY:            tty,

		// An idle agent whose last word was its own has finished its
		// turn and handed control back. The registry has no flag for
		// that, and it is the difference between "working, leave it"
		// and "stopped, it needs you".
		Waiting: a.Live && a.Status == "idle" && act.LastRole == "assistant",
	}
	if !a.StartedAt.IsZero() {
		// startedAt, not procStart: the session's own clock. They
		// differ, and it is the session that has been running for
		// four hours, not necessarily the process.
		t.Elapsed = time.Since(a.StartedAt)
	}
	if b.Git != nil {
		info := b.Git.Info(a.Cwd)
		t.Repo, t.Dirty = info.Repo, info.Dirty
		// git's answer wins where it has one. The transcript's branch
		// is only a fallback for the first poll or two, before the
		// cache has resolved the directory.
		if info.Branch != "" {
			t.Branch = info.Branch
		}
	}
	// Which pull request the branch belongs to is the one thing here that
	// costs a network round trip, so it is asked last, of a cache that
	// answers from memory and refreshes behind us.
	if b.Forge != nil {
		t.Ref = b.Forge.Ref(a.Cwd, t.Branch)
	}
	// The shim, when installed, knows things the transcript cannot: the
	// display name Claude Code itself uses, and - the load-bearing one -
	// the size of the context window, which no local file records.
	if p, ok := statusline.Read(b.StatuslineDir, a.SessionID); ok {
		if p.Model.DisplayName != "" {
			t.Model = p.Model.DisplayName
		}
		t.ContextWindow = p.ContextWindow.Size
		if n, ok := p.Tokens(); ok {
			t.ContextTokens = n
		}
	}
	return t
}

// AccountUsage reads the machine-wide rate limits the shim records. Without
// the shim there is nothing to read, and the panels that show it are omitted.
func AccountUsage(statuslineDir string, now time.Time) Account {
	var acct Account
	five, seven, ok := statusline.Account(statuslineDir)
	if !ok {
		return acct
	}
	if pct, ok := five.Pct(); ok {
		acct.Usage5hPct = &pct
		if at, ok := five.Resets(); ok {
			acct.Usage5hResetsIn = FormatDuration(at.Sub(now))
		}
	}
	if pct, ok := seven.Pct(); ok {
		acct.Usage7dPct = &pct
		if at, ok := seven.Resets(); ok {
			acct.Usage7dResetsIn = FormatDuration(at.Sub(now))
		}
	}
	return acct
}
