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

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// Row is one agent plus everything the list displays about it.
type Row struct {
	Agent   registry.Agent
	Summary string
	Age     time.Time // freshest activity timestamp known for the agent
}

// Source fetches the current rows. Injected so tests can script the data.
type Source func() ([]Row, error)

// Focuser jumps to an agent's window and returns a human-readable
// description of where it went. Injected so tests never steal focus.
type Focuser func(a registry.Agent) (string, error)

// Stopper terminates an agent's process. Injected so tests never kill
// anything.
type Stopper func(a registry.Agent) error

type sortKey int

const (
	sortStatus sortKey = iota
	sortAge
	sortName
	sortDir
)

func (k sortKey) String() string {
	switch k {
	case sortAge:
		return "age"
	case sortName:
		return "name"
	case sortDir:
		return "dir"
	default:
		return "status"
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
	sort     sortKey
	cursor   int             // index into visible()
	stopping *registry.Agent // agent awaiting stop confirmation
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

const pollInterval = time.Second

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

	case focusMsg:
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
		m.sort = sortStatus
		return m, nil
	case "a":
		m.sort = sortAge
		return m, nil
	case "n":
		m.sort = sortName
		return m, nil
	case "d":
		m.sort = sortDir
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
		return m, func() tea.Msg {
			desc, err := f(agent)
			return focusMsg{desc: desc, err: err}
		}
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

// visible returns the rows that pass the filter, in sort order. Dead agents
// stay listed (greyed out in the view) so you can see what just finished;
// they sort after live ones under every key.
func (m Model) visible() []Row {
	rows := make([]Row, 0, len(m.rows))
	q := strings.ToLower(m.filter)
	for _, r := range m.rows {
		if q == "" ||
			strings.Contains(strings.ToLower(r.Agent.Name), q) ||
			strings.Contains(strings.ToLower(r.Agent.Cwd), q) ||
			strings.Contains(strings.ToLower(r.Summary), q) {
			rows = append(rows, r)
		}
	}
	key := m.sort
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Agent.Live != b.Agent.Live {
			return a.Agent.Live
		}
		switch key {
		case sortAge:
			return a.Age.After(b.Age)
		case sortName:
			return strings.ToLower(a.Agent.Name) < strings.ToLower(b.Agent.Name)
		case sortDir:
			return strings.ToLower(a.Agent.Cwd) < strings.ToLower(b.Agent.Cwd)
		default:
			ra, rb := statusRank(a.Agent.Status), statusRank(b.Agent.Status)
			if ra != rb {
				return ra < rb
			}
			return a.Age.After(b.Age)
		}
	})
	return rows
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
