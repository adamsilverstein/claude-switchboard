// Package appui is the standalone app window: a native HTML view of the same
// agent list the terminal picker shows.
//
// The split is deliberate. Filtering and sorting happen here in Go, through
// the exported functions in internal/ui, so the two front ends cannot drift
// apart on what "sorted by status" means. Everything that is purely about
// presentation - which row the cursor is on, whether the list is grouped,
// how dense it is - lives in the page, where it can respond to a keypress
// without a round trip.
package appui

import (
	"fmt"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// Snapshot is one frame of the app window, pushed to the page after every
// poll and after every command that changes what should be on screen.
type Snapshot struct {
	// Generation increments per frame. The page uses it to ignore a
	// snapshot that arrives out of order.
	Generation int `json:"generation"`

	PolledAt time.Time `json:"polledAt"`
	Sort     string    `json:"sort"`
	Filter   string    `json:"filter"`
	Grouped  bool      `json:"grouped"`
	Compact  bool      `json:"compact"`

	// Error is a scan failure, shown in place of the list. An empty
	// string means the scan succeeded, including when it found nothing.
	Error string `json:"error,omitempty"`

	Account Account     `json:"account"`
	Agents  []AgentView `json:"agents"`

	// Total is how many agents there are before the filter, so the page
	// can say "3 of 18" rather than implying the rest stopped existing.
	Total int `json:"total"`
}

// Account is the machine-wide usage the statusline shim records. Both
// windows are pointers: absent is not zero, and a missing shim must render
// as an omitted panel rather than a meter reading 0%.
type Account struct {
	Usage5hPct      *int   `json:"usage5hPct"`
	Usage5hResetsIn string `json:"usage5hResetsIn,omitempty"`
	Usage7dPct      *int   `json:"usage7dPct"`
	Usage7dResetsIn string `json:"usage7dResetsIn,omitempty"`
}

// AgentView is one row, formatted. Numbers arrive as strings that are ready
// to print, because both front ends should agree on how a duration reads and
// the terminal picker's formatter is the one that already decides that.
//
// Every optional field is either omitted from the JSON or explicitly null.
// The page has one rule for all of them: absent renders as a dash, or as
// nothing at all where the design says the element should not be there.
type AgentView struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Summary   string `json:"summary,omitempty"`
	Cwd       string `json:"cwd"`
	CwdShort  string `json:"cwdShort"`
	Live      bool   `json:"live"`

	// Status is the word the row shows: "waiting", "busy", "idle",
	// "shell", "dead", or "" when the registry recorded none.
	Status string `json:"status"`
	Age    string `json:"age"`

	Model          string `json:"model,omitempty"`
	ContextPct     *int   `json:"contextPct"`
	ContextLabel   string `json:"contextLabel,omitempty"`  // "158k / 1M"
	ContextWindow  string `json:"contextWindow,omitempty"` // "1M"
	PermissionMode string `json:"permissionMode,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Branch         string `json:"branch,omitempty"`
	Dirty          bool   `json:"dirty"`
	Elapsed        string `json:"elapsed,omitempty"`

	TTY       string `json:"tty,omitempty"`
	Tmux      string `json:"tmux,omitempty"`
	Focusable bool   `json:"focusable"`
}

// view formats one row for the page.
func view(now time.Time, r ui.Row) AgentView {
	t := r.Telemetry
	v := AgentView{
		PID:            r.Agent.PID,
		SessionID:      r.Agent.SessionID,
		Name:           r.Name,
		Summary:        r.Summary,
		Cwd:            r.Agent.Cwd,
		CwdShort:       ui.ShortDir(r.Agent.Cwd),
		Live:           r.Agent.Live,
		Status:         statusWord(r),
		Age:            ui.FormatAge(now, r.Age),
		Model:          t.Model,
		PermissionMode: t.PermissionMode,
		Repo:           t.Repo,
		Branch:         t.Branch,
		Dirty:          t.Dirty,
		TTY:            shortTTY(t.TTY),
		Tmux:           r.Agent.Tmux,
		Focusable:      r.Agent.Focusable() && r.Agent.Live,
	}
	if t.ContextWindow > 0 {
		v.ContextWindow = FormatTokens(t.ContextWindow)
	}
	if pct, ok := t.ContextPct(); ok {
		v.ContextPct = &pct
		v.ContextLabel = FormatTokens(t.ContextTokens) + " / " + v.ContextWindow
	}
	if t.Elapsed > 0 {
		v.Elapsed = FormatDuration(t.Elapsed)
	}
	return v
}

// statusWord is the word the row shows. A dead agent is "dead" whatever the
// registry last recorded, and an idle agent whose transcript says it spoke
// last is "waiting" - the registry has no such flag, so it is derived.
func statusWord(r ui.Row) string {
	if !r.Agent.Live {
		return "dead"
	}
	if r.Telemetry.Waiting {
		return "waiting"
	}
	return r.Agent.Status
}

// shortTTY trims /dev/ so the readout reads "ttys014" like the statusline.
func shortTTY(tty string) string {
	if len(tty) > 5 && tty[:5] == "/dev/" {
		return tty[5:]
	}
	return tty
}

// FormatTokens renders a token count the way the statusline does: 1000000 is
// "1M", 200000 is "200k", 158000 is "158k".
func FormatTokens(n int) string {
	switch {
	case n <= 0:
		return "—"
	case n >= 1_000_000:
		if n%1_000_000 == 0 {
			return fmt.Sprintf("%dM", n/1_000_000)
		}
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// FormatDuration renders how long something has been going: "4h 42m",
// "18m", "2d 3h". Distinct from ui.FormatAge, which is tuned for a narrow
// column and drops the second unit.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// Match reports whether v is the agent identified by pid and session.
//
// The session id is what makes this safe. registry.SameProcess warns that a
// PID may have been reused between the scan that produced a row and the
// moment someone acts on it; carrying the session id across the bridge means
// a click on a row that has since died cannot land on whatever took its PID.
func (v AgentView) Match(a registry.Agent) bool {
	return a.PID == v.PID && a.SessionID == v.SessionID
}
