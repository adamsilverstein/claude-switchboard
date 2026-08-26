package appui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/ui"
)

// Controller holds everything the app window knows between frames: the last
// poll's rows, the account usage, and the view preferences.
//
// It is safe for concurrent use because the poll loop and the page's
// callbacks arrive on different goroutines.
type Controller struct {
	mu         sync.Mutex
	rows       []ui.Row
	account    Account
	err        error
	generation int
	polledAt   time.Time
	prefs      Prefs
	prefsPath  string

	// capacity is how many comfortable rows the page says it can show
	// without scrolling. Zero until the page has measured itself.
	capacity int
}

// Prefs are the view choices that outlive a launch. Sorting is here because
// a sort you picked yesterday is still the sort you want today; the cursor
// and the filter are not, because they are about the moment.
//
// Grouped is a preference rather than a fact: the snapshot only reports the
// list as grouped when the sort is by status. Bands of statuses over a list
// ordered by context would hide the ordering you asked for, and would leave
// the keyboard walking the rows in an order the screen does not show.
type Prefs struct {
	Sort    string `json:"sort"`
	Grouped bool   `json:"grouped"`

	// Density is "compact", "comfy", or "" for "decide from how many
	// rows there are". Three states rather than a bool because "I have
	// not chosen" is different from "I chose comfy", and only the second
	// should survive the list growing.
	Density string `json:"density"`

	// filter is deliberately not persisted: reopening the window to a
	// list that silently hides most of your agents is a bad surprise.
	filter string
}

// New returns a controller, restoring preferences from path if it holds any.
// A missing or unreadable file is not an error: it means the defaults, which
// are the same defaults the terminal picker has.
func New(prefsPath string) *Controller {
	c := &Controller{prefsPath: prefsPath, prefs: Prefs{Sort: "status", Grouped: true}}
	if raw, err := os.ReadFile(prefsPath); err == nil {
		var p Prefs
		if json.Unmarshal(raw, &p) == nil {
			if p.Sort != "" {
				c.prefs.Sort = p.Sort
			}
			c.prefs.Grouped = p.Grouped
			c.prefs.Density = p.Density
		}
	}
	return c
}

// DefaultPrefsPath returns ~/.claude/switchboard/app.json.
func DefaultPrefsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "switchboard", "app.json"), nil
}

// SetRows records the result of a poll. An error replaces the list rather
// than clearing it: a transient scan failure should not blank the window.
func (c *Controller) SetRows(rows []ui.Row, account Account, err error, polledAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	c.polledAt = polledAt
	if err == nil {
		c.rows = rows
		c.account = account
	}
}

// SetCapacity records how many comfortable rows the page can fit. Density is
// really a question about height - "18 rows fit at 1440 wide" is true and
// also unreachable on a laptop, which tops out near 980px of it - so the row
// count alone is the wrong trigger. The page measures and reports; nothing
// here derives a height.
func (c *Controller) SetCapacity(rows int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rows == c.capacity || rows < 0 {
		return false
	}
	c.capacity = rows
	return true
}

// SetSort, SetFilter, SetGrouped and SetCompact record a command from the
// page. Each returns true when something actually changed, so the caller can
// push a fresh frame immediately instead of waiting for the next poll.
func (c *Controller) SetSort(key string) bool {
	return c.update(true, func(p *Prefs) { p.Sort = ui.ParseSortKey(key).String() })
}

func (c *Controller) SetFilter(q string) bool {
	return c.update(false, func(p *Prefs) { p.filter = q })
}

func (c *Controller) SetGrouped(on bool) bool {
	return c.update(true, func(p *Prefs) { p.Grouped = on })
}

// SetDensity takes "compact", "comfy", or "" to hand the choice back to the
// row count.
func (c *Controller) SetDensity(d string) bool {
	if d != "compact" && d != "comfy" {
		d = ""
	}
	return c.update(true, func(p *Prefs) { p.Density = d })
}

func (c *Controller) update(persist bool, f func(*Prefs)) bool {
	c.mu.Lock()
	before := c.prefs
	f(&c.prefs)
	changed := c.prefs != before
	after := c.prefs
	c.mu.Unlock()
	if changed && persist {
		c.savePrefs(after)
	}
	return changed
}

// savePrefs writes preferences best-effort. Failing to remember a sort order
// is not worth surfacing to someone who is trying to find an agent.
func (c *Controller) savePrefs(p Prefs) {
	if c.prefsPath == "" {
		return
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.prefsPath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(c.prefsPath, raw, 0o644)
}

// Snapshot builds the next frame: the current rows, filtered and sorted by
// the shared functions the terminal picker uses, formatted for the page.
func (c *Controller) Snapshot(now time.Time) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++

	key := ui.ParseSortKey(c.prefs.Sort)
	rows := ui.Filter(c.rows, c.prefs.filter)
	ui.SortRows(rows, key)

	s := Snapshot{
		Generation: c.generation,
		PolledAt:   c.polledAt,
		Sort:       key.String(),
		Filter:     c.prefs.filter,
		Grouped:    c.prefs.Grouped && key == ui.SortStatus,
		Compact:    c.compactFor(len(rows)),
		Account:    c.account,
		Total:      len(c.rows),
		Agents:     make([]AgentView, 0, len(rows)),
	}
	if c.err != nil {
		s.Error = c.err.Error()
	}
	for _, r := range rows {
		s.Agents = append(s.Agents, view(now, r))
	}
	return s
}

// autoCompactAbove is the answer for the first frame, before the page has
// measured its own viewport and told us how many rows actually fit.
const autoCompactAbove = 8

func (c *Controller) compactFor(n int) bool {
	switch c.prefs.Density {
	case "compact":
		return true
	case "comfy":
		return false
	case "":
		if c.capacity > 0 {
			return n > c.capacity
		}
		return n > autoCompactAbove
	}
	return false
}

// Find returns the agent a page command refers to, matching on session id as
// well as PID. A row the page is holding may describe a process that has
// since exited, and acting on the PID alone could signal or focus whatever
// reused it.
func (c *Controller) Find(pid int, session string) (registry.Agent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		if r.Agent.PID == pid && r.Agent.SessionID == session {
			return r.Agent, true
		}
	}
	return registry.Agent{}, false
}
