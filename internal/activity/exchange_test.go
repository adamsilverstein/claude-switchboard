package activity

import (
	"strings"
	"testing"
)

func TestLastExchangeCollectsTheWholeReplySinceYourPrompt(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"an older prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"an older reply"}]}}`,
		`{"type":"user","message":{"role":"user","content":"Can you rerun the failing e2e spec\nand report back?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Running it now."}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"FAIL"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"injected by the harness"}}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>noise</local-command-stdout>"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"The spec fails on the second upload. Two options:\n1. await the upload\n2. bump the timeout\n\nWhich would you like?"}]}}`,
		`{"type":"system","content":"noise"}`,
	}, "\n") + "\n"
	ex, ok := LastExchange(writeTranscript(t, cwd, session, content), cwd, session)
	if !ok {
		t.Fatal("LastExchange found no transcript")
	}
	if want := "Can you rerun the failing e2e spec\nand report back?"; ex.Asked != want {
		t.Errorf("Asked = %q, want %q", ex.Asked, want)
	}
	want := "Running it now.\n\nThe spec fails on the second upload. Two options:\n1. await the upload\n2. bump the timeout\n\nWhich would you like?"
	if ex.Reply != want {
		t.Errorf("Reply = %q, want %q", ex.Reply, want)
	}
	if ex.Truncated {
		t.Error("a short reply should not be truncated")
	}
}

func TestLastExchangeWithNoReplyYet(t *testing.T) {
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"now do the next one"}]}}` + "\n"
	ex, _ := LastExchange(writeTranscript(t, cwd, session, content), cwd, session)
	if ex.Asked != "now do the next one" || ex.Reply != "" {
		t.Errorf("got %+v; want the prompt and no reply", ex)
	}
}

func TestLastExchangeKeepsTheEndOfALongReply(t *testing.T) {
	long := strings.Repeat("é", maxExchange) + " the question?"
	content := `{"type":"user","message":{"role":"user","content":"go"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + long + `"}]}}` + "\n"
	ex, _ := LastExchange(writeTranscript(t, cwd, session, content), cwd, session)
	if !ex.Truncated {
		t.Error("a reply over the cap should be marked truncated")
	}
	if !strings.HasSuffix(ex.Reply, "the question?") {
		t.Error("truncation should keep the end of the reply")
	}
	if len(ex.Reply) > maxExchange {
		t.Errorf("reply is %d bytes, over the %d cap", len(ex.Reply), maxExchange)
	}
	if !strings.HasPrefix(ex.Reply, "é") {
		t.Error("the cut should land on a rune boundary")
	}
}

func TestLastExchangeMissingTranscript(t *testing.T) {
	if _, ok := LastExchange(t.TempDir(), cwd, session); ok {
		t.Error("a missing transcript should report false")
	}
	if _, ok := LastExchange(t.TempDir(), "", ""); ok {
		t.Error("an unnamed session should report false")
	}
}

// A prompt that opens with markup is still yours, and must be paired with
// its own reply rather than skipped in favour of the one before it.
func TestLastExchangeKeepsAPromptThatStartsWithMarkup(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"first prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
		`{"type":"user","message":{"role":"user","content":"<div> is this markup valid?"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	}, "\n") + "\n"
	ex, _ := LastExchange(writeTranscript(t, cwd, session, content), cwd, session)
	if ex.Asked != "<div> is this markup valid?" || ex.Reply != "second reply" {
		t.Errorf("got %+v; want the markup prompt paired with the second reply", ex)
	}
}
