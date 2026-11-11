package target

import (
	"fmt"
	"strings"
	"testing"
)

// Captured from "ps -axo pid=,ppid=,comm=" (trimmed): one agent under
// iTerm, one under Terminal.app, one under Ghostty, one under WezTerm.
const treeOutput = "    1     0 /sbin/launchd\n" +
	" 1310     1 /Applications/iTerm.app/Contents/MacOS/iTerm2\n" +
	" 1969  1310 /Users/me/Library/Application Support/iTerm2/iTermServer-3.7.7\n" +
	"80080  1969 /usr/bin/login\n" +
	"80081 80080 -zsh\n" +
	"80859 80081 claude\n" +
	"88380     1 /System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal\n" +
	"88787 88380 login\n" +
	"88788 88787 -zsh\n" +
	"88900 88788 claude\n" +
	"90000     1 /Applications/Ghostty.app/Contents/MacOS/ghostty\n" +
	"90001 90000 /usr/bin/login\n" +
	"90002 90001 -zsh\n" +
	"90003 90002 claude\n" +
	"91000     1 /Applications/WezTerm.app/Contents/MacOS/wezterm-gui\n" +
	"91001 91000 -zsh\n" +
	"91002 91001 claude\n"

func TestProcTreeHost(t *testing.T) {
	tree := ParseProcTree(treeOutput)
	cases := []struct {
		pid  int
		host Host
		name string
	}{
		{80859, HostIterm, "iTermServer-3.7.7"},
		{88900, HostTerminal, "Terminal"},
		{90003, HostGhostty, "ghostty"},
		{91002, HostUnknown, "wezterm-gui"},
		{4242, HostUnknown, ""},
	}
	for _, c := range cases {
		host, name := tree.Host(c.pid)
		if host != c.host || name != c.name {
			t.Errorf("Host(%d) = %q, %q; want %q, %q", c.pid, host, name, c.host, c.name)
		}
	}
}

// Captured from listTerminalScript with three windows, the second holding
// two tabs, and one tab Terminal reports no tty for.
const terminalOutput = "1\t908\t1\t/dev/ttys011\n" +
	"2\t911\t1\t/dev/ttys012\n" +
	"2\t911\t2\t/dev/ttys014\n" +
	"3\t913\t1\t-\n"

func TestParseTerminalTabs(t *testing.T) {
	tabs := ParseTerminalTabs(terminalOutput + "garbage\n")
	if len(tabs) != 4 {
		t.Fatalf("got %d tabs, want 4", len(tabs))
	}
	if tabs[2] != (TerminalTab{WindowIndex: 2, WindowID: 911, TabIndex: 2, TTY: "/dev/ttys014"}) {
		t.Errorf("second tab of window 2 = %+v", tabs[2])
	}
	if tabs[3].TTY != "" {
		t.Errorf("no-tty tab TTY = %q", tabs[3].TTY)
	}
}

const ghosttyOutput = "8C1D2E3F-AAAA\t/dev/ttys030\n" +
	"8C1D2E3F-BBBB\t/dev/ttys031\n" +
	"8C1D2E3F-CCCC\tmissing value\n"

func TestParseGhosttyTerminals(t *testing.T) {
	terms := ParseGhosttyTerminals(ghosttyOutput + "\n\tnoid\n")
	if len(terms) != 3 {
		t.Fatalf("got %d terminals, want 3", len(terms))
	}
	if terms[1] != (GhosttyTerminal{ID: "8C1D2E3F-BBBB", TTY: "/dev/ttys031"}) {
		t.Errorf("second terminal = %+v", terms[1])
	}
	if terms[2].TTY != "" {
		t.Errorf("no-tty terminal TTY = %q", terms[2].TTY)
	}
}

func TestResolveTerminalAgent(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, terminal: terminalOutput, tree: treeOutput}
	loc := NewResolver(r).Resolve(Agent{PID: 88900, TTY: "/dev/ttys014"})
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if loc.Backend != "terminal" || loc.Desc != "Terminal window 2, tab 2 (/dev/ttys014)" {
		t.Errorf("Backend, Desc = %q, %q", loc.Backend, loc.Desc)
	}
	// A sweep only knows how to close an iTerm pane.
	if len(loc.CloseCommands) != 0 {
		t.Errorf("a Terminal tab got close commands %+v", loc.CloseCommands)
	}
	args := strings.Join(loc.Commands[0].Args, " ")
	if !strings.HasSuffix(args, "/dev/ttys014 911 2") {
		t.Errorf("focus args should end with tty, window id, tab index; got %q", args[len(args)-30:])
	}
}

func TestResolveGhosttyAgent(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ghostty: ghosttyOutput, tree: treeOutput}
	loc := NewResolver(r).Resolve(Agent{PID: 90003, TTY: "/dev/ttys031"})
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if loc.Backend != "ghostty" {
		t.Errorf("Backend = %q", loc.Backend)
	}
	if len(loc.CloseCommands) != 0 {
		t.Errorf("a Ghostty terminal got close commands %+v", loc.CloseCommands)
	}
	args := loc.Commands[0].Args
	if got := strings.Join(args[len(args)-2:], " "); got != "8C1D2E3F-BBBB /dev/ttys031" {
		t.Errorf("focus should pass the terminal id and its tty, got %q", got)
	}
	// The agent runs under Ghostty, so Terminal is never asked.
	for _, c := range r.calls {
		if strings.Contains(c, "com.apple.Terminal") {
			t.Error("enumerated Terminal for an agent under Ghostty")
		}
	}
}

// Ghostty before 1.4 has no tty property, so the enumeration fails to
// compile. The agent is still listed, and the reason says what to upgrade.
func TestResolveOldGhosttyExplainsVersion(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ghosttyErr: fmt.Errorf("osascript: syntax error"), tree: treeOutput}
	loc := NewResolver(r).Resolve(Agent{PID: 90003, TTY: "/dev/ttys031"})
	if loc.Focusable {
		t.Fatal("should not be focusable when Ghostty cannot be enumerated")
	}
	if !strings.Contains(loc.Reason, "1.4") {
		t.Errorf("Reason = %q; want it to name the Ghostty version needed", loc.Reason)
	}
}

func TestResolveUnsupportedTerminalNamesIt(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, tree: treeOutput}
	loc := NewResolver(r).Resolve(Agent{PID: 91002, TTY: "/dev/ttys040"})
	if loc.Focusable {
		t.Fatal("an agent in WezTerm should not be focusable")
	}
	if !strings.Contains(loc.Reason, "wezterm-gui") {
		t.Errorf("Reason = %q; want it to name the terminal", loc.Reason)
	}
}

func TestResolveTmuxClientInTerminal(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, terminal: terminalOutput, tree: treeOutput, tmux: "/dev/ttys012 88900\n"}
	loc := NewResolver(r).Resolve(Agent{Tmux: "work:@1.%1"})
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if len(loc.Commands) != 3 || !strings.Contains(strings.Join(loc.Commands[0].Args, " "), "com.apple.Terminal") {
		t.Errorf("want Terminal focus then two tmux selects, got %+v", loc.Commands)
	}
	if !strings.Contains(loc.Desc, "via Terminal window 2, tab 1") {
		t.Errorf("Desc = %q", loc.Desc)
	}
}

func TestResolveBackgroundViewerInTerminal(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, terminal: terminalOutput, ps: "ttys012  claude agents\n"}
	loc := NewResolver(r).ResolveBackground()
	if !loc.Focusable || loc.Backend != "terminal" {
		t.Fatalf("Focusable, Backend = %v, %q (%s)", loc.Focusable, loc.Backend, loc.Reason)
	}
}

// As with iTerm, activating Terminal raises its key window, so the target
// window has to be put in front again afterwards.
func TestTerminalFocusRaisesAroundActivate(t *testing.T) {
	activate := strings.Index(focusTerminalScript, "activate")
	if activate < 0 {
		t.Fatal("focus script never activates Terminal")
	}
	for _, half := range []string{focusTerminalScript[:activate], focusTerminalScript[activate:]} {
		if !strings.Contains(half, "set index of w to 1") {
			t.Error("want the window raised both before and after activate")
		}
	}
}

// Enumerating a terminal must not launch it: someone who never opens
// Terminal or Ghostty should not see it start, or get an automation prompt
// for it, because an agent missed iTerm.
func TestEnumerationsDoNotLaunchTheApp(t *testing.T) {
	for name, script := range map[string]string{"Terminal": listTerminalScript, "Ghostty": listGhosttyScript} {
		if !strings.HasPrefix(script, "if application id") || !strings.Contains(strings.SplitN(script, "\n", 2)[0], "is not running then return") {
			t.Errorf("%s enumeration should return early when the app is not running", name)
		}
	}
}

// A terminal that quit between enumeration and focus must not be relaunched
// by the focus script just to report that the agent is gone.
func TestFocusScriptsDoNotRelaunchTheApp(t *testing.T) {
	for name, script := range map[string]string{"Terminal": focusTerminalScript, "Ghostty": focusGhosttyScript} {
		guard := strings.Index(script, "is not running then return \"not found\"")
		tell := strings.Index(script, "tell application")
		if guard < 0 || guard > tell {
			t.Errorf("%s focus should check the app is running before talking to it", name)
		}
	}
}
