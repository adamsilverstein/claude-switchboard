package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// Peeker reads the last exchange of an agent's conversation. Injected so
// tests never read a real transcript.
type Peeker func(a registry.Agent) (activity.Exchange, error)

// Replier types a reply into an agent's terminal. Injected so tests never
// type into anything.
type Replier func(a registry.Agent, text string) error

// ReplyBlocked says why an agent cannot be replied to from here, or "" when
// it can. Both front ends ask the same question, so the rule lives in one
// place.
//
// Only an idle Claude Code agent with a terminal of its own qualifies. A
// busy agent would take the text as an interruption or queue it behind
// work you cannot see, and a background session's tty is the daemon's, so
// there is no prompt on it to type into.
func ReplyBlocked(a registry.Agent) string {
	switch {
	case !a.Live:
		return "agent is gone"
	case a.Codex():
		return "replies reach Claude Code sessions only"
	case a.Background():
		return "background session; reply from its claude agents viewer"
	case a.Status == "busy":
		return "agent is working; reply once it is idle"
	case a.Status == "shell":
		return "agent is running a shell command"
	case a.Status != "idle":
		return "agent status unknown; reply from its window"
	}
	return ""
}

// peek is the open peek panel: one agent's last exchange, and the reply
// being typed under it.
type peek struct {
	agent   registry.Agent
	name    string
	ex      activity.Exchange
	loaded  bool
	err     error
	input   string
	scroll  int // lines scrolled up from the bottom
	sending bool
}

type peekMsg struct {
	pid     int
	session string
	ex      activity.Exchange
	err     error
}

type replyMsg struct {
	pid     int
	session string
	name    string
	err     error
}

// WithPeek turns on the peek panel and, when replier is non-nil, replying
// from it.
func (m Model) WithPeek(peeker Peeker, replier Replier) Model {
	m.peeker = peeker
	m.replier = replier
	return m
}

func loadPeek(p Peeker, a registry.Agent) tea.Cmd {
	return func() tea.Msg {
		ex, err := p(a)
		return peekMsg{pid: a.PID, session: a.SessionID, ex: ex, err: err}
	}
}

func (p *peek) is(pid int, session string) bool {
	return p != nil && p.agent.PID == pid && p.agent.SessionID == session
}

// openPeek opens the panel on the selected row.
func (m Model) openPeek() (tea.Model, tea.Cmd) {
	if m.peeker == nil {
		return m, nil
	}
	rows := m.visible()
	if m.cursor >= len(rows) {
		return m, nil
	}
	r := rows[m.cursor]
	m.peek = &peek{agent: r.Agent, name: r.Name}
	return m, loadPeek(m.peeker, r.Agent)
}

// replyBlocked is ReplyBlocked plus the one reason only the model knows.
func (m Model) replyBlocked() string {
	if m.replier == nil {
		return "replies are not available here"
	}
	return ReplyBlocked(m.peek.agent)
}

func (m Model) handlePeekKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := *m.peek
	m.peek = &p
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyEsc:
		m.peek = nil
		return m, nil
	case tea.KeyEnter:
		if p.sending || strings.TrimSpace(p.input) == "" {
			return m, nil
		}
		if why := m.replyBlocked(); why != "" {
			m.notice = why
			return m, nil
		}
		p.sending = true
		agent, text, name, r := p.agent, p.input, p.name, m.replier
		return m, func() tea.Msg {
			return replyMsg{pid: agent.PID, session: agent.SessionID, name: name, err: r(agent, text)}
		}
	case tea.KeyBackspace:
		if p.input != "" {
			_, size := utf8.DecodeLastRuneInString(p.input)
			p.input = p.input[:len(p.input)-size]
		}
	case tea.KeyUp:
		p.scroll++
	case tea.KeyDown:
		p.scroll--
	case tea.KeyPgUp:
		p.scroll += m.peekBodyHeight() - 1
	case tea.KeyPgDown:
		p.scroll -= m.peekBodyHeight() - 1
	case tea.KeySpace:
		p.input += " "
	case tea.KeyRunes:
		p.input += string(msg.Runes)
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
	return m, nil
}

// updatePeek applies a message that concerns the panel. handled is false
// for messages it has nothing to do with.
func (m Model) updatePeek(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case peekMsg:
		if m.peek.is(msg.pid, msg.session) {
			p := *m.peek
			p.ex, p.err, p.loaded = msg.ex, msg.err, true
			m.peek = &p
		}
		return m, nil, true
	case replyMsg:
		if !m.peek.is(msg.pid, msg.session) {
			if msg.err != nil {
				m.notice = "reply failed: " + msg.err.Error()
			}
			return m, nil, true
		}
		p := *m.peek
		p.sending = false
		m.peek = &p
		if msg.err != nil {
			m.notice = "reply failed: " + msg.err.Error()
			return m, nil, true
		}
		p.input, p.scroll = "", 0
		m.notice = "sent to " + msg.name
		return m, loadPeek(m.peeker, p.agent), true
	}
	return m, nil, false
}

// refreshPeekAgent keeps the panel's copy of the agent current, so the
// reply rule is judged on this second's status rather than the one the
// panel opened with. An agent that has left the list is marked gone.
func (m *Model) refreshPeekAgent() {
	if m.peek == nil {
		return
	}
	p := *m.peek
	p.agent.Live = false
	for _, r := range m.rows {
		if r.Agent.PID == p.agent.PID && r.Agent.SessionID == p.agent.SessionID {
			p.agent = r.Agent
			break
		}
	}
	m.peek = &p
}

// peekBodyHeight is how many lines of conversation fit between the title
// and the input.
func (m Model) peekBodyHeight() int {
	if h := m.height - 4; h > 1 {
		return h
	}
	return 1
}

func (m Model) peekView() string {
	p := m.peek
	var b strings.Builder

	status := p.agent.Status
	if !p.agent.Live {
		status = "dead"
	}
	title := titleStyle.Render("peek · "+p.name) + "  " + footerStyle.Render(status)
	b.WriteString(title + "\n")
	b.WriteString(footerStyle.Render(strings.Repeat("─", max(m.width, 10))) + "\n")

	body := m.peekBody()
	h := m.peekBodyHeight()
	end := len(body) - p.scroll
	if end < h {
		end = min(h, len(body))
	}
	start := max(end-h, 0)
	for _, l := range body[start:end] {
		b.WriteString(l + "\n")
	}
	for i := end - start; i < h; i++ {
		b.WriteString("\n")
	}

	switch why := m.replyBlocked(); {
	case p.sending:
		b.WriteString(footerStyle.Render("> "+p.input+"  sending…") + "\n")
	case why != "":
		b.WriteString(deadStyle.Render("  "+why) + "\n")
	default:
		b.WriteString(noticeStyle.Render("> ") + p.input + "▌\n")
	}
	footer := "enter send  ↑↓ pgup/pgdn scroll  esc close"
	if m.notice != "" {
		footer = m.notice
	}
	b.WriteString(footerStyle.Render(footer))
	return b.String()
}

// peekBody lays the exchange out as display lines, "you" over the agent.
func (m Model) peekBody() []string {
	p := m.peek
	switch {
	case p.err != nil:
		return []string{deadStyle.Render("  " + p.err.Error())}
	case !p.loaded:
		return []string{deadStyle.Render("  reading transcript…")}
	}
	const gutter = 8
	w := max(m.width-gutter, 20)
	var out []string
	speaker := func(who, text string) {
		for i, l := range Wrap(text, w) {
			label := ""
			if i == 0 {
				label = who
			}
			out = append(out, headerLabel(label, gutter)+l)
		}
	}
	if p.ex.Asked != "" {
		speaker("you", p.ex.Asked)
	} else {
		out = append(out, deadStyle.Render("  (your last prompt is further back than the tail reaches)"))
	}
	out = append(out, "")
	switch {
	case p.ex.Reply == "":
		out = append(out, deadStyle.Render("  (no reply yet)"))
	default:
		if p.ex.Truncated {
			out = append(out, deadStyle.Render(fmt.Sprintf("%s… (earlier text cut)", strings.Repeat(" ", gutter))))
		}
		speaker("agent", p.ex.Reply)
	}
	return out
}

func headerLabel(s string, w int) string {
	return footerStyle.Render(s) + strings.Repeat(" ", w-len(s))
}

// Wrap breaks text into lines of at most w runes, keeping its own line
// breaks and splitting on spaces where it can.
func Wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimRight(para, " \t\r")
		if para == "" {
			out = append(out, "")
			continue
		}
		r := []rune(para)
		for len(r) > w {
			cut := w
			for i := w; i > w/2; i-- {
				if r[i] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, strings.TrimRight(string(r[:cut]), " "))
			r = r[cut:]
			for len(r) > 0 && r[0] == ' ' {
				r = r[1:]
			}
		}
		out = append(out, string(r))
	}
	return out
}
