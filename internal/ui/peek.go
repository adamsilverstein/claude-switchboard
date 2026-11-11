package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

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

	// sent identifies the reply in flight. A completion carries it back,
	// and one that does not match - from a panel since closed and
	// reopened on the same agent - may not touch this panel's draft.
	sent int
}

type peekMsg struct {
	pid     int
	session string
	ex      activity.Exchange
	err     error
}

type replyMsg struct {
	seq  int
	name string
	err  error
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
		m.replySeq++
		p.sending, p.sent = true, m.replySeq
		agent, text, name, r, seq := p.agent, p.input, p.name, m.replier, m.replySeq
		return m, func() tea.Msg {
			return replyMsg{seq: seq, name: name, err: r(agent, text)}
		}
	case tea.KeyBackspace:
		// The text being sent stays as it was until the send says
		// whether it went: kept to retry, or cleared.
		if p.input != "" && !p.sending {
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
		if !p.sending {
			p.input += " "
		}
	case tea.KeyRunes:
		if !p.sending {
			p.input += string(msg.Runes)
		}
	}
	p.scroll = min(max(p.scroll, 0), max(len(m.peekBody())-m.peekBodyHeight(), 0))
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
		if m.peek == nil || m.peek.sent != msg.seq {
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
		b.WriteString(footerStyle.Render("> "+fitTail(printable(p.input), m.width-len("> ")-len("  sending…"))+"  sending…") + "\n")
	case why != "":
		b.WriteString(deadStyle.Render("  "+why) + "\n")
	default:
		b.WriteString(noticeStyle.Render("> ") + fitTail(printable(p.input), m.width-len("> ")-1) + "▌\n")
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
		return []string{deadStyle.Render("  " + printable(p.err.Error()))}
	case !p.loaded:
		return []string{deadStyle.Render("  reading transcript…")}
	}
	const gutter = 8
	w := max(m.width-gutter, 20)
	var out []string
	speaker := func(who, text string) {
		for i, l := range Wrap(printable(text), w) {
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

// printable makes transcript text safe to print into a terminal. A
// transcript holds whatever a tool printed and the agent quoted back, and an
// escape sequence in it would be run by the terminal rather than shown. Tabs
// become spaces; every other control character but the line break becomes
// U+FFFD.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return utf8.RuneError
		}
		return r
	}, s)
}

// fitTail keeps the end of s that fits in w terminal cells, so the input
// stays on one row and the caret end of a long reply stays in view.
func fitTail(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	cells := 1 // the leading ellipsis
	i := len(r)
	for i > 0 && cells+runewidth.RuneWidth(r[i-1]) <= w {
		i--
		cells += runewidth.RuneWidth(r[i])
	}
	return "…" + string(r[i:])
}

// Wrap breaks text into lines of at most w terminal cells, keeping its own
// line breaks and splitting on spaces where it can. Cells rather than runes,
// because a wide character takes two, and a line that overflows the
// terminal wraps onto a row the scrolling does not know about.
func Wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimRight(para, " \t\r")
		if para == "" {
			out = append(out, "")
			continue
		}
		r := []rune(para)
		for runewidth.StringWidth(string(r)) > w {
			// fit is how many runes fill w cells; at least one, so a
			// character wider than the line still makes progress.
			fit, cells := 0, 0
			for fit < len(r) && cells+runewidth.RuneWidth(r[fit]) <= w {
				cells += runewidth.RuneWidth(r[fit])
				fit++
			}
			fit = max(fit, 1)
			cut := fit
			for i := fit; i > fit/2; i-- {
				if i < len(r) && r[i] == ' ' {
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
