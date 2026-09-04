package target

import (
	"fmt"
	"strings"
	"testing"
)

// psOutput is captured "ps -axo tty=,command=" output: a daemon-hosted
// background session, two viewers (one launched through the shim, one
// through a versioned binary), a one-shot agents query, and noise.
const psOutput = "??       /Users/me/.local/bin/claude daemon run --origin transient\n" +
	"??       claude bg-pty-host --bg-pty-host /tmp/cc-daemon-501/x/spare/a.pty.sock 200 50 -- /Users/me/.local/share/claude/versions/2.1.260 --bg-spare /tmp/x.sock\n" +
	"ttys007  claude bg-spare --bg-spare /tmp/x.sock\n" +
	"ttys005  claude agents\n" +
	"ttys002  /Users/me/.local/share/claude/versions/2.1.260 agents --cwd /tmp\n" +
	"ttys008  claude /color\n" +
	"ttys009  claude agents --json\n" +
	"ttys010  -zsh\n" +
	"ttys011  vim agents\n"

func TestParseAgentViewers(t *testing.T) {
	got := ParseAgentViewers(psOutput)
	want := []string{"/dev/ttys005", "/dev/ttys002"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ParseAgentViewers = %v, want %v", got, want)
	}
}

func TestResolveBackgroundViaViewer(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ps: "ttys002  claude agents\n"}
	res := NewResolver(r)
	loc := res.ResolveBackground()
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if loc.Desc != "background session, via claude agents in iTerm window 2, tab 1, session 1 (/dev/ttys002)" {
		t.Errorf("Desc = %q", loc.Desc)
	}
	if len(loc.Commands) != 1 || !strings.Contains(strings.Join(loc.Commands[0].Args, " "), "47B06502-33AD-48BD-A678-8C24E78B6F7A") {
		t.Errorf("Commands = %+v, want the viewer's session UUID", loc.Commands)
	}
}

func TestResolveBackgroundPrefersFrontmostViewer(t *testing.T) {
	// Window 3 hosts ttys008 and window 1 hosts ttys021; ps lists the
	// back one first, but the front one is where the user last looked.
	r := &fakeRunner{iterm: itermOutput, ps: "ttys008  claude agents\nttys021  claude agents\n"}
	loc := NewResolver(r).ResolveBackground()
	if !loc.Focusable {
		t.Fatalf("not focusable: %s", loc.Reason)
	}
	if !strings.Contains(loc.Desc, "iTerm window 1,") {
		t.Errorf("Desc = %q, want the frontmost viewer", loc.Desc)
	}
}

func TestResolveBackgroundWithoutViewer(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ps: "ttys008  claude /color\n"}
	loc := NewResolver(r).ResolveBackground()
	if loc.Focusable {
		t.Error("should not be focusable with no viewer")
	}
	if !strings.Contains(loc.Reason, "claude agents") {
		t.Errorf("Reason = %q, want a hint to open claude agents", loc.Reason)
	}
}

func TestResolveBackgroundViewerOutsideIterm(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ps: "ttys099  claude agents\n"}
	loc := NewResolver(r).ResolveBackground()
	if loc.Focusable {
		t.Error("a viewer iTerm does not host cannot be focused")
	}
	if !strings.Contains(loc.Reason, "/dev/ttys099") || !strings.Contains(loc.Reason, "not an iTerm window") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveBackgroundSurvivesPsFailure(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, psErr: fmt.Errorf("ps: exec format error")}
	loc := NewResolver(r).ResolveBackground()
	if loc.Focusable {
		t.Error("should not be focusable when ps failed")
	}
	if !strings.Contains(loc.Reason, "ps: exec format error") {
		t.Errorf("Reason = %q", loc.Reason)
	}
}

func TestResolveBackgroundScansProcessesOnce(t *testing.T) {
	r := &fakeRunner{iterm: itermOutput, ps: "ttys002  claude agents\n"}
	res := NewResolver(r)
	res.ResolveBackground()
	res.ResolveBackground()
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, "ps ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("ran ps %d times for two resolves, want 1", n)
	}
}
