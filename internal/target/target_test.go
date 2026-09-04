package target

import (
	"fmt"
	"strings"
	"testing"
)

// Captured from the enumeration AppleScript on a real machine (trimmed).
// Window 18448 has a split-pane tab with two sessions; the last row is a
// session iTerm reports no tty for.
const itermOutput = "1\t18448\t1\t1\t38B78799-2B91-4DB8-A8C1-B5CA971930F6\t/dev/ttys021\n" +
	"1\t18448\t1\t2\tA34271B4-CB2F-4C45-ABDB-1BF49AAD3C76\t/dev/ttys026\n" +
	"2\t7185\t1\t1\t47B06502-33AD-48BD-A678-8C24E78B6F7A\t/dev/ttys002\n" +
	"3\t14820\t1\t1\tAD11FD11-0C65-4C91-A002-D39BB6810C21\t/dev/ttys008\n" +
	"4\t9999\t1\t1\tDEAD0000-0000-0000-0000-000000000000\t-\n"

func TestParseItermSessions(t *testing.T) {
	sessions := ParseItermSessions(itermOutput)
	if len(sessions) != 5 {
		t.Fatalf("got %d sessions, want 5", len(sessions))
	}
	first := sessions[0]
	if first.WindowIndex != 1 || first.WindowID != 18448 || first.TabIndex != 1 || first.SessionIndex != 1 {
		t.Errorf("first session = %+v", first)
	}
	if first.TTY != "/dev/ttys021" {
		t.Errorf("first TTY = %q", first.TTY)
	}
	// Split pane: same window and tab, second session index.
	if sessions[1].SessionIndex != 2 || sessions[1].TTY != "/dev/ttys026" {
		t.Errorf("split pane session = %+v", sessions[1])
	}
	// A session with no tty parses with TTY "".
	if sessions[4].TTY != "" {
		t.Errorf("no-tty session TTY = %q", sessions[4].TTY)
	}
}

func TestParseItermSessionsIgnoresGarbage(t *testing.T) {
	if got := ParseItermSessions("\nnot\ttabbed\n1\tx\t1\t1\tid\ttty\n"); len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}
}

func TestParseTmuxRef(t *testing.T) {
	ref, ok := ParseTmuxRef("claude-e96b2a6e:@2.%2")
	if !ok {
		t.Fatal("expected ok")
	}
	if ref.Session != "claude-e96b2a6e" || ref.Window != "@2" || ref.Pane != "%2" {
		t.Errorf("ref = %+v", ref)
	}
	for _, bad := range []string{"", "noseparator", ":@2.%2", "sess:@2.", "sess:"} {
		if _, ok := ParseTmuxRef(bad); ok {
			t.Errorf("ParseTmuxRef(%q) unexpectedly ok", bad)
		}
	}
}

// fakeRunner serves canned output per command name and records calls.
type fakeRunner struct {
	iterm    string
	itermErr error
	tmux     string
	tmuxErr  error
	ps       string
	psErr    error
	calls    []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch name {
	case "osascript":
		return f.iterm, f.itermErr
	case "tmux":
		return f.tmux, f.tmuxErr
	case "ps":
		return f.ps, f.psErr
	}
	return "", fmt.Errorf("unexpected command %s", name)
}

func TestResolveItermAgent(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	loc := res.Resolve("", "/dev/ttys008")
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if loc.Desc != "iTerm window 3, tab 1, session 1 (/dev/ttys008)" {
		t.Errorf("Desc = %q", loc.Desc)
	}
	if len(loc.Commands) != 1 || loc.Commands[0].Name != "osascript" {
		t.Errorf("Commands = %+v", loc.Commands)
	}
	if !strings.Contains(strings.Join(loc.Commands[0].Args, " "), "AD11FD11-0C65-4C91-A002-D39BB6810C21") {
		t.Error("focus command should target the session UUID")
	}
}

func TestResolveAgentWithNoTTY(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	loc := res.Resolve("", "")
	if loc.Focusable {
		t.Error("agent with no tty should not be focusable")
	}
	if !strings.Contains(loc.Reason, "no controlling terminal") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveUnknownTTY(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	loc := res.Resolve("", "/dev/ttys099")
	if loc.Focusable {
		t.Error("unknown tty should not be focusable")
	}
	if !strings.Contains(loc.Reason, "/dev/ttys099") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveSurvivesItermFailure(t *testing.T) {
	res := NewResolver(&fakeRunner{itermErr: fmt.Errorf("iTerm got an error: not running")})
	loc := res.Resolve("", "/dev/ttys008")
	if loc.Focusable {
		t.Error("should not be focusable when iTerm enumeration failed")
	}
	if !strings.Contains(loc.Reason, "iTerm enumeration failed") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveTmuxAttachedToIterm(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, tmux: "/dev/ttys002\n"}
	res := NewResolver(r)
	loc := res.Resolve("claude-e96b2a6e:@2.%2", "")
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	// Two hops: focus the hosting iTerm window, then select window and
	// pane inside tmux.
	if len(loc.Commands) != 3 {
		t.Fatalf("got %d commands, want 3: %+v", len(loc.Commands), loc.Commands)
	}
	if loc.Commands[0].Name != "osascript" {
		t.Errorf("first command = %s, want osascript", loc.Commands[0].Name)
	}
	if got := loc.Commands[1].String(); got != "tmux select-window -t claude-e96b2a6e:@2" {
		t.Errorf("select-window = %q", got)
	}
	if got := loc.Commands[2].String(); got != "tmux select-pane -t claude-e96b2a6e:@2.%2" {
		t.Errorf("select-pane = %q", got)
	}
}

func TestResolveTmuxDetached(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput, tmux: "\n"})
	loc := res.Resolve("claude-e96b2a6e:@2.%2", "")
	if loc.Focusable {
		t.Error("detached tmux session should not be focusable")
	}
	if !strings.Contains(loc.Reason, "no attached client") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveTmuxClientOutsideIterm(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput, tmux: "/dev/ttys777\n"})
	loc := res.Resolve("claude-e96b2a6e:@2.%2", "")
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	// Only the tmux selects: the hosting terminal is not an iTerm window,
	// so there is nothing to raise, but the pane can still be shown.
	if len(loc.Commands) != 2 || loc.Commands[0].Name != "tmux" {
		t.Errorf("Commands = %+v", loc.Commands)
	}
}

func TestCommandStringQuotesSpaces(t *testing.T) {
	c := Command{Name: "osascript", Args: []string{"-e", "tell app"}}
	if got := c.String(); got != `osascript -e "tell app"` {
		t.Errorf("String = %q", got)
	}
}

func TestFocusRunsCommandsInOrder(t *testing.T) {
	r := &fakeRunner{}
	loc := Location{
		Focusable: true,
		Commands: []Command{
			{Name: "osascript", Args: []string{"-e", "x", "uuid"}},
			{Name: "tmux", Args: []string{"select-window", "-t", "s:@1"}},
		},
	}
	if err := Focus(r, loc); err != nil {
		t.Fatalf("Focus: %v", err)
	}
	if len(r.calls) != 2 || !strings.HasPrefix(r.calls[0], "osascript") || !strings.HasPrefix(r.calls[1], "tmux") {
		t.Errorf("calls = %v", r.calls)
	}
}

func TestFocusRefusesUnfocusable(t *testing.T) {
	r := &fakeRunner{}
	err := Focus(r, Location{Reason: "no controlling terminal"})
	if err == nil || !strings.Contains(err.Error(), "no controlling terminal") {
		t.Errorf("err = %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("no commands should run, got %v", r.calls)
	}
}

func TestFocusDetectsVanishedSession(t *testing.T) {
	r := &fakeRunner{iterm: "not found\n"}
	loc := Location{Focusable: true, Commands: []Command{{Name: "osascript", Args: []string{"-e", "x"}}}}
	if err := Focus(r, loc); err == nil {
		t.Error("expected an error when the focus script reports not found")
	}
}

func TestAttachedTmuxSessions(t *testing.T) {
	r := &fakeRunner{tmux: "work\n\nwork\nreview\n"}
	attached, err := AttachedTmuxSessions(r)
	if err != nil {
		t.Fatalf("AttachedTmuxSessions: %v", err)
	}
	if len(attached) != 2 || !attached["work"] || !attached["review"] {
		t.Errorf("attached = %v, want work and review", attached)
	}
}

func TestAttachedTmuxSessionsReportsFailure(t *testing.T) {
	r := &fakeRunner{tmuxErr: fmt.Errorf("no server running")}
	if _, err := AttachedTmuxSessions(r); err == nil {
		t.Fatal("want an error when tmux cannot answer")
	}
}

// The focus script must activate iTerm before it selects anything. With two
// displays macOS answers an activate by raising whatever iTerm window sits
// on the display the user is already looking at, which undoes a selection
// made beforehand: asking for a window on the second monitor lands on a
// window on the first. Activating first and selecting after leaves the
// selection as the last word.
func TestFocusScriptActivatesBeforeSelecting(t *testing.T) {
	activate := strings.Index(focusSessionScript, "activate")
	if activate < 0 {
		t.Fatal("focus script never activates iTerm")
	}
	sel := strings.Index(focusSessionScript, "select ")
	if sel < 0 {
		t.Fatal("focus script never selects a session")
	}
	if activate > sel {
		t.Error("activate must come before the first select, or macOS raises the window on the active display instead of the requested one")
	}
}
