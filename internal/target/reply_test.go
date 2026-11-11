package target

import (
	"strings"
	"testing"
)

func init() { replyPause = 0 }

func TestCleanReply(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"  go with option 1 ", "go with option 1", true},
		{"yes, ship it — and 🚀", "yes, ship it — and 🚀", true},
		{"", "", false},
		{"   ", "", false},
		{"two\nlines", "", false},
		{"carriage\rreturn", "", false},
		{"escape \x1b[2J sequence", "", false},
		{"tab\tinside", "", false},
		{strings.Repeat("a", maxReplyRunes+1), "", false},
		{"bad \xff utf8", "", false},
	} {
		got, err := CleanReply(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("CleanReply(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("CleanReply(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveReplyIterm(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	rt := res.ResolveReply("", "/dev/ttys008")
	if rt.Backend != "iterm" || rt.Iterm.SessionID != "AD11FD11-0C65-4C91-A002-D39BB6810C21" || rt.TTY != "/dev/ttys008" {
		t.Fatalf("ResolveReply = %+v", rt)
	}

	r := &fakeRunner{iterm: "ok"}
	if err := SendReply(r, rt, "go with option 1"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || !strings.HasPrefix(r.calls[0], "osascript -e ") {
		t.Fatalf("calls = %q", r.calls)
	}
	// The tty travels with the UUID so the script can refuse a session
	// that has changed hands, and the text travels as its own argument,
	// never spliced into the script.
	for _, want := range []string{"AD11FD11-0C65-4C91-A002-D39BB6810C21 /dev/ttys008 14820 1 1 go with option 1"} {
		if !strings.Contains(r.calls[0], want) {
			t.Errorf("call %q missing %q", r.calls[0], want)
		}
	}
	if strings.Contains(sendTextScript, "go with option 1") {
		t.Error("the reply must not be part of the script")
	}
}

func TestResolveReplyUnknownWindow(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	rt := res.ResolveReply("", "/dev/ttys099")
	if rt.Backend != "" || rt.Reason == "" {
		t.Fatalf("ResolveReply = %+v; want no target and a reason", rt)
	}
	r := &fakeRunner{}
	if err := SendReply(r, rt, "hi"); err == nil {
		t.Error("sending to no target should fail")
	}
	if len(r.calls) != 0 {
		t.Errorf("nothing should run; ran %q", r.calls)
	}
}

func TestResolveReplyNeedsATTY(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	if rt := res.ResolveReply("", ""); rt.Backend != "" {
		t.Errorf("an agent with no tty resolved to %+v", rt)
	}
	if rt := res.ResolveReply("claude-x:@2.%2", ""); rt.Backend != "" {
		t.Errorf("a tmux agent with no tty resolved to %+v", rt)
	}
}

func TestSendReplyTmuxChecksThePaneTTY(t *testing.T) {
	res := NewResolver(&fakeRunner{iterm: itermOutput})
	rt := res.ResolveReply("claude-x:@2.%2", "/dev/ttys030")
	if rt.Backend != "tmux" || rt.Pane != "%2" {
		t.Fatalf("ResolveReply = %+v", rt)
	}

	r := &fakeRunner{tmux: "/dev/ttys030\n"}
	if err := SendReply(r, rt, "-y please"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux display-message -p -t %2 #{pane_tty}",
		"tmux send-keys -t %2 -l -- -y please",
		"tmux send-keys -t %2 Enter",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n%s\nwant\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}

	// The pane now belongs to another terminal: refuse before typing.
	r = &fakeRunner{tmux: "/dev/ttys031\n"}
	if err := SendReply(r, rt, "hi"); err == nil {
		t.Error("a pane on another tty should be refused")
	}
	if len(r.calls) != 1 {
		t.Errorf("only the check should have run; ran %q", r.calls)
	}
}

// tmux drops a trailing semicolon from a send-keys argument, so "yes;" has
// to arrive as "yes" plus a hex ";" to be typed as written.
func TestSendReplyTmuxKeepsTrailingSemicolons(t *testing.T) {
	rt := ReplyTarget{Backend: "tmux", Pane: "%2", TTY: "/dev/ttys030"}
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"yes;", []string{"tmux send-keys -t %2 -l -- yes", "tmux send-keys -t %2 -H 3b"}},
		{"a;b;;", []string{"tmux send-keys -t %2 -l -- a;b", "tmux send-keys -t %2 -H 3b 3b"}},
		{";", []string{"tmux send-keys -t %2 -H 3b"}},
	} {
		r := &fakeRunner{tmux: "/dev/ttys030\n"}
		if err := SendReply(r, rt, tc.text); err != nil {
			t.Fatal(err)
		}
		got := r.calls[1 : len(r.calls)-1] // between the tty check and Enter
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%q sent\n%s\nwant\n%s", tc.text, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
		}
	}
}
