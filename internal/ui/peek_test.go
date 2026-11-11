package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/adamsilverstein/claude-switchboard/internal/activity"
	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

type sent struct {
	pid  int
	text string
}

func peeking(t *testing.T, replyErr error) (Model, *[]sent) {
	t.Helper()
	var got []sent
	m := loaded(t, nil).WithPeek(
		func(a registry.Agent) (activity.Exchange, error) {
			return activity.Exchange{Asked: "rerun the spec?", Reply: "It fails. Which option?"}, nil
		},
		func(a registry.Agent, text string) error {
			got = append(got, sent{a.PID, text})
			return replyErr
		})
	return m, &got
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		var msg tea.KeyMsg
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func space(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	return next.(Model), cmd
}

func TestSpaceOpensPeekAndShowsTheExchange(t *testing.T) {
	m, _ := peeking(t, nil)
	m, cmd := space(t, m)
	if m.peek == nil {
		t.Fatal("space did not open the peek panel")
	}
	m = applyAll(t, m, cmd)
	view := m.View()
	for _, want := range []string{"peek · alpha gutenberg fix", "rerun the spec?", "It fails. Which option?", "> "} {
		if !strings.Contains(view, want) {
			t.Errorf("peek view missing %q:\n%s", want, view)
		}
	}
	m, _ = press(t, m, "esc")
	if m.peek != nil {
		t.Error("esc should close the panel")
	}
}

func TestReplySendsToThePeekedAgent(t *testing.T) {
	m, got := peeking(t, nil)
	m, cmd := space(t, m)
	m = applyAll(t, m, cmd)
	// Keys that mean something in the list are text in the panel.
	m = typeText(t, m, "go with q 1")
	if m.quitting || m.peek == nil {
		t.Fatal("typing in the panel should not reach the list's keys")
	}
	m, cmd = press(t, m, "enter")
	m = applyAll(t, m, cmd)
	if len(*got) != 1 || (*got)[0] != (sent{1, "go with q 1"}) {
		t.Fatalf("sent %+v, want one reply to pid 1", *got)
	}
	if m.peek.input != "" {
		t.Errorf("input = %q after a send, want it cleared", m.peek.input)
	}
	if !strings.Contains(m.notice, "sent to") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestReplyFailureKeepsTheText(t *testing.T) {
	m, _ := peeking(t, errors.New("the window has closed"))
	m, cmd := space(t, m)
	m = applyAll(t, m, cmd)
	m = typeText(t, m, "yes")
	m, cmd = press(t, m, "enter")
	m = applyAll(t, m, cmd)
	if m.peek.input != "yes" {
		t.Errorf("input = %q; a failed reply should keep the text", m.peek.input)
	}
	if !strings.Contains(m.notice, "the window has closed") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestReplyRefusedToABusyAgent(t *testing.T) {
	m, got := peeking(t, nil)
	for m.visible()[m.cursor].Agent.Status != "busy" {
		before := m.cursor
		m, _ = press(t, m, "j")
		if m.cursor == before {
			t.Fatal("test rows changed; no busy row to peek at")
		}
	}
	m, cmd := space(t, m)
	m = applyAll(t, m, cmd)
	m = typeText(t, m, "hi")
	m, cmd = press(t, m, "enter")
	applyAll(t, m, cmd)
	if len(*got) != 0 {
		t.Errorf("a busy agent was sent %+v", *got)
	}
	if !strings.Contains(m.View(), "reply once it is idle") {
		t.Error("the panel should say why it will not send")
	}
}

func TestPeekNoticesTheAgentLeaving(t *testing.T) {
	m, _ := peeking(t, nil)
	m, _ = space(t, m)
	next, _ := m.Update(rowsMsg{rows: testRows()[1:]})
	m = next.(Model)
	if m.peek.agent.Live {
		t.Error("an agent missing from the poll should be marked gone")
	}
	if ReplyBlocked(m.peek.agent) == "" {
		t.Error("a gone agent must not be replied to")
	}
}

func TestReplyBlocked(t *testing.T) {
	ok := registry.Agent{Live: true, Status: "idle"}
	if why := ReplyBlocked(ok); why != "" {
		t.Errorf("idle live agent blocked: %s", why)
	}
	for _, a := range []registry.Agent{
		{Live: false, Status: "idle"},
		{Live: true, Status: "busy"},
		{Live: true, Status: "shell"},
		{Live: true, Status: ""},
		{Live: true, Status: "idle", Kind: "bg"},
		{Live: true, Status: "idle", Entrypoint: registry.EntrypointCodex},
	} {
		if ReplyBlocked(a) == "" {
			t.Errorf("%+v should be blocked", a)
		}
	}
}

func TestWrap(t *testing.T) {
	got := Wrap("one two three four\n\nfive", 9)
	want := []string{"one two", "three", "four", "", "five"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Wrap = %q, want %q", got, want)
	}
	if got := Wrap(strings.Repeat("x", 25), 10); len(got) != 3 {
		t.Errorf("an unbroken word should hard-wrap; got %q", got)
	}
}

func TestPeekStripsTerminalEscapes(t *testing.T) {
	m := loaded(t, nil).WithPeek(func(a registry.Agent) (activity.Exchange, error) {
		return activity.Exchange{Asked: "hi", Reply: "clear \x1b[2J and bell \a and \u009b csi"}, nil
	}, nil)
	m, cmd := space(t, m)
	m = applyAll(t, m, cmd)
	view := m.View()
	for _, bad := range []string{"\x1b[2J", "\a", "\u009b"} {
		if strings.Contains(view, bad) {
			t.Errorf("view carries raw control %q", bad)
		}
	}
	if !strings.Contains(view, "clear") || !strings.Contains(view, "csi") {
		t.Error("the readable text should survive")
	}
}

// A reply sent from a panel that was then closed and reopened must not clear
// the new panel's draft when it completes.
func TestStaleReplyCompletionLeavesTheNewDraftAlone(t *testing.T) {
	m, _ := peeking(t, nil)
	m, _ = space(t, m)
	m = typeText(t, m, "first")
	m, cmd := press(t, m, "enter")
	if !m.peek.sending {
		t.Fatal("enter should start a send")
	}
	m = typeText(t, m, "x")
	if m.peek.input != "first" {
		t.Errorf("input = %q; typing while sending should not change the draft", m.peek.input)
	}
	m, _ = press(t, m, "esc")
	m, _ = space(t, m)
	m = typeText(t, m, "second")
	m = applyAll(t, m, cmd) // the first send completes now
	if m.peek.input != "second" {
		t.Errorf("input = %q; an old completion cleared the new draft", m.peek.input)
	}
}

func TestPeekScrollStopsAtTheTop(t *testing.T) {
	m, _ := peeking(t, nil)
	m.height = 6
	m, cmd := space(t, m)
	m = applyAll(t, m, cmd)
	for range 50 {
		m, _ = press(t, m, "up")
	}
	top := max(len(m.peekBody())-m.peekBodyHeight(), 0)
	if m.peek.scroll != top {
		t.Errorf("scroll = %d after 50 ups, want it held at %d", m.peek.scroll, top)
	}
}

func TestWrapCountsWideCharactersAsTwoCells(t *testing.T) {
	for _, l := range Wrap("日本語のテキストをここに書きます", 10) {
		if w := runewidth.StringWidth(l); w > 10 {
			t.Errorf("line %q is %d cells, over 10", l, w)
		}
	}
}

func TestFitTailKeepsTheEnd(t *testing.T) {
	got := fitTail("the start of a long reply and its end", 12)
	if runewidth.StringWidth(got) > 12 || !strings.HasSuffix(got, "its end") || !strings.HasPrefix(got, "…") {
		t.Errorf("fitTail = %q", got)
	}
	if got := fitTail("short", 12); got != "short" {
		t.Errorf("fitTail(short) = %q", got)
	}
}
