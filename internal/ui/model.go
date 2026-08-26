// Package ui is the interactive picker: a live list of agents with
// incremental filtering, sorting, and focus-on-enter. It polls the registry
// once a second; the AppleScript bridge is only touched when a focus is
// requested, never during the poll.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/forge"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// Row is one agent plus everything the list displays about it.
type Row struct {
	Agent registry.Agent
	// Name is what the list shows. It is usually the agent's registered
	// name, but a session Claude Code named after its directory gets a
	// better one recovered from its transcript, so the two can differ.
	Name    string
	Summary string
	Age     time.Time // freshest activity timestamp known for the agent

	// Telemetry is the extra per-session detail the app window shows. It
	// is optional throughout: the terminal picker leaves it zero, and
	// even the app window fills in only what the machine can actually
	// tell it. Every field must render as "unknown" at its zero value.
	Telemetry Telemetry
}

// Telemetry is what could be learned about a session beyond the registry
// entry: the model it is on, how full its context window is, the repository
// it is working in. None of it is guaranteed - a session whose transcript is
// unreadable, or whose statusline shim is not installed, carries a zero
// Telemetry and still lists.
type Telemetry struct {
	Model          string // display name, "Opus 5"; "" when unknown
	ContextWindow  int    // tokens the model can hold; 0 when unknown
	ContextTokens  int    // tokens currently held; 0 when unknown
	PermissionMode string // "auto", "plan", "default"; "" when unknown
	Repo           string // basename of the repository root
	Branch         string // current git branch
	Dirty          bool   // repository has uncommitted changes

	// Ref is the pull request or issue the branch belongs to, when the
	// forge could be asked. Zero means either that there is none or that
	// nobody has looked yet; the view renders both as absent.
	Ref forge.Ref

	Elapsed    time.Duration // since the session started; 0 when unknown
	TTY        string        // "ttys014"
	WindowDesc string        // "iTerm window 3", "tmux 2.1", "not focusable"

	// Waiting is true when the agent looks like it is waiting on you
	// rather than idling on its own. The registry has no such flag, so
	// this is derived: an idle agent whose last transcript entry is an
	// assistant turn stopped and handed the turn back to you.
	Waiting bool

	// Compactions is how many times the session has been compacted, and
	// KnownCompactions says whether that count was actually measured.
	// Counting requires reading the whole transcript, so it stays unset
	// until something is willing to pay for that.
	Compactions      int
	KnownCompactions bool
}

// ContextPct returns how full the context window is as a whole percentage,
// and false when either half of the fraction is missing. Callers must not
// substitute zero: a session with no context reading is not a session at 0%.
func (t Telemetry) ContextPct() (int, bool) {
	if t.ContextWindow <= 0 || t.ContextTokens <= 0 {
		return 0, false
	}
	pct := t.ContextTokens * 100 / t.ContextWindow
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// Source fetches the current rows. Injected so tests can script the data.
type Source func() ([]Row, error)

// Focuser jumps to an agent's window and returns a human-readable
// description of where it went. Injected so tests never steal focus.
type Focuser func(a registry.Agent) (string, error)

// Stopper terminates an agent's process. Injected so tests never kill
// anything.
type Stopper func(a registry.Agent) error

// SortKey is the column the list is ordered by. The app window and the
// terminal picker share the key set so a sort chosen in one means the same
// thing in the other.
type SortKey int

const (
	SortStatus SortKey = iota
	SortAge
	SortName
	SortDir
	SortContext // context window used, fullest first
	SortRepo
)

func (k SortKey) String() string {
	switch k {
	case SortAge:
		return "age"
	case SortName:
		return "name"
	case SortDir:
		return "dir"
	case SortContext:
		return "context"
	case SortRepo:
		return "repo"
	default:
		return "status"
	}
}

// ParseSortKey resolves a key name sent over the app window's bridge.
// An unrecognised name falls back to status rather than erroring: a UI that
// asks for a sort this build does not have should still get a sorted list.
func ParseSortKey(s string) SortKey {
	switch s {
	case "age":
		return SortAge
	case "name":
		return SortName
	case "dir":
		return SortDir
	case "context":
		return SortContext
	case "repo":
		return SortRepo
	default:
		return SortStatus
	}
}

// Model is the Bubble Tea model for the picker.
type Model struct {
	source  Source
	focuser Focuser
	stopper Stopper

	rows     []Row
	err      error
	notice   string // transient message shown in the footer
	filter   string
	typing   bool // filter input active
	sort     SortKey
	cursor   int             // index into visible()
	stopping *registry.Agent // agent awaiting stop confirmation
	flash    int             // remaining flash frames on the focused row
	width    int
	height   int
	quitting bool
}

// New builds the picker model.
func New(source Source, focuser Focuser, stopper Stopper) Model {
	return Model{source: source, focuser: focuser, stopper: stopper, width: 100, height: 24}
}

type rowsMsg struct {
	rows []Row
	err  error
}

type focusMsg struct {
	desc string
	err  error
}

type stopMsg struct {
	name string
	err  error
}

type tickMsg struct{}

type flashMsg struct{}

const pollInterval = time.Second

// Focusing a window takes a few hundred milliseconds of AppleScript, which
// is long enough to wonder whether the keypress registered. The picked row
// blinks for the duration so the keypress is visibly acknowledged before
// anything else happens.
const (
	flashInterval = 70 * time.Millisecond
	flashFrames   = 4
)

func flashTick() tea.Cmd {
	return tea.Tick(flashInterval, func(time.Time) tea.Msg { return flashMsg{} })
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetch(m.source), tick())
}

func fetch(s Source) tea.Cmd {
	return func() tea.Msg {
		rows, err := s()
		return rowsMsg{rows: rows, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetch(m.source), tick())

	case rowsMsg:
		return m.applyRows(msg), nil

	case flashMsg:
		if m.flash > 0 {
			m.flash--
			if m.flash > 0 {
				return m, flashTick()
			}
		}
		return m, nil

	case focusMsg:
		m.flash = 0
		if msg.err != nil {
			m.notice = "focus failed: " + msg.err.Error()
		} else {
			m.notice = "focused " + msg.desc
		}
		return m, nil

	case stopMsg:
		if msg.err != nil {
			m.notice = "stop failed: " + msg.err.Error()
		} else {
			m.notice = "sent SIGTERM to " + msg.name
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// applyRows swaps in fresh data while keeping the cursor on the same agent.
func (m Model) applyRows(msg rowsMsg) Model {
	if msg.err != nil {
		m.err = msg.err
		return m
	}
	m.err = nil
	var selected int
	if rows := m.visible(); m.cursor < len(rows) {
		selected = rows[m.cursor].Agent.PID
	}
	m.rows = msg.rows
	m.clampCursor()
	if selected != 0 {
		for i, r := range m.visible() {
			if r.Agent.PID == selected {
				m.cursor = i
				break
			}
		}
	}
	return m
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	if m.stopping != nil {
		agent := *m.stopping
		m.stopping = nil
		if msg.String() == "y" {
			st := m.stopper
			return m, func() tea.Msg {
				return stopMsg{name: agent.Name, err: st(agent)}
			}
		}
		m.notice = "stop cancelled"
		return m, nil
	}
	if m.typing {
		switch msg.Type {
		case tea.KeyEsc:
			m.typing = false
			m.filter = ""
		case tea.KeyEnter:
			m.typing = false
		case tea.KeyBackspace:
			if len(m.filter) > 0 {
				// Trim a whole rune, not a byte: the filter holds
				// UTF-8 and a byte-level slice would corrupt a
				// multi-byte final character.
				_, size := utf8.DecodeLastRuneInString(m.filter)
				m.filter = m.filter[:len(m.filter)-size]
			}
		case tea.KeyCtrlC:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp, tea.KeyDown:
			return m.moveCursor(msg), nil
		case tea.KeySpace:
			m.filter += " "
		case tea.KeyRunes:
			m.filter += string(msg.Runes)
		}
		m.clampCursor()
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "/":
		m.typing = true
		m.filter = ""
		return m, nil
	case "esc":
		m.filter = ""
		m.clampCursor()
		return m, nil
	case "up", "k", "down", "j":
		return m.moveCursor(msg), nil
	case "g":
		m.cursor = 0
		return m, nil
	case "G":
		m.cursor = len(m.visible()) - 1
		m.clampCursor()
		return m, nil
	case "s":
		m.sort = SortStatus
		return m, nil
	case "a":
		m.sort = SortAge
		return m, nil
	case "n":
		m.sort = SortName
		return m, nil
	case "d":
		m.sort = SortDir
		return m, nil
	case "ctrl+x":
		rows := m.visible()
		if m.cursor >= len(rows) {
			return m, nil
		}
		agent := rows[m.cursor].Agent
		if !agent.Live {
			m.notice = "agent is already gone"
			return m, nil
		}
		m.stopping = &agent
		return m, nil
	case "enter":
		rows := m.visible()
		if m.cursor >= len(rows) {
			return m, nil
		}
		agent := rows[m.cursor].Agent
		if !agent.Live {
			m.notice = "agent is gone; nothing to focus"
			return m, nil
		}
		f := m.focuser
		m.notice = "focusing " + rows[m.cursor].Name + "…"
		m.flash = flashFrames
		return m, tea.Batch(flashTick(), func() tea.Msg {
			desc, err := f(agent)
			return focusMsg{desc: desc, err: err}
		})
	}
	return m, nil
}

func (m Model) moveCursor(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.visible())-1 {
			m.cursor++
		}
	}
	return m
}

func (m *Model) clampCursor() {
	if n := len(m.visible()); m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// visible returns the rows that pass the filter, in sort order.
func (m Model) visible() []Row {
	rows := Filter(m.rows, m.filter)
	SortRows(rows, m.sort)
	return rows
}

// Filter returns the rows matching q, case-insensitively, across every field
// worth searching: name, working directory, summary, and - when the caller
// has filled them in - repository, model, and the pull request the branch
// belongs to, by number or by title. An empty query matches
// everything. The result is a fresh slice; the input is left alone.
func Filter(rows []Row, q string) []Row {
	out := make([]Row, 0, len(rows))
	q = strings.ToLower(strings.TrimSpace(q))
	for _, r := range rows {
		if q == "" || r.matches(q) {
			out = append(out, r)
		}
	}
	return out
}

func (r Row) matches(q string) bool {
	for _, field := range []string{
		r.Name,
		r.Agent.Cwd,
		r.Summary,
		r.Telemetry.Repo,
		r.Telemetry.Model,
		r.Telemetry.Ref.Label(),
		r.Telemetry.Ref.Title,
	} {
		if field != "" && strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

// SortRows orders rows in place under key. Dead agents stay listed (greyed
// out in the view) so you can see what just finished; they sort after live
// ones under every key. Rows missing the data a key needs sort last within
// their liveness group rather than sorting as zero.
func SortRows(rows []Row, key SortKey) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Agent.Live != b.Agent.Live {
			return a.Agent.Live
		}
		switch key {
		case SortAge:
			return a.Age.After(b.Age)
		case SortName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case SortDir:
			return strings.ToLower(a.Agent.Cwd) < strings.ToLower(b.Agent.Cwd)
		case SortContext:
			pa, oka := a.Telemetry.ContextPct()
			pb, okb := b.Telemetry.ContextPct()
			if oka != okb {
				return oka
			}
			if pa != pb {
				return pa > pb
			}
			return a.Age.After(b.Age)
		case SortRepo:
			ra, rb := a.Telemetry.Repo, b.Telemetry.Repo
			if (ra == "") != (rb == "") {
				return ra != ""
			}
			if !strings.EqualFold(ra, rb) {
				return strings.ToLower(ra) < strings.ToLower(rb)
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		default:
			ra, rb := statusRank(a.statusForRank()), statusRank(b.statusForRank())
			if ra != rb {
				return ra < rb
			}
			return a.Age.After(b.Age)
		}
	})
}

// statusForRank is the status the sort treats the row as having. It is the
// registry's own status unless telemetry derived that the agent is waiting
// on you, which the registry never records itself. Rows with no telemetry -
// every row the terminal picker builds - rank exactly as they always did.
func (r Row) statusForRank() string {
	if r.Telemetry.Waiting {
		return "waiting"
	}
	return r.Agent.Status
}

// statusRank orders statuses by how much attention they deserve: agents
// waiting on you first, then working ones.
func statusRank(status string) int {
	switch status {
	case "waiting":
		return 0
	case "idle":
		return 1
	case "busy":
		return 2
	case "shell":
		return 3
	default:
		return 4
	}
}

// FormatAge renders a duration compactly for the AGE column.
func FormatAge(now, t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
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
