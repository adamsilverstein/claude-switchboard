package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shim runs on every statusline render, so what matters is that it is
// transparent: the wrapped command sees the same stdin and its output is the
// only output. These run the built binary because that is the only way to
// exercise stdin, stdout and the exit code together.
func shim(t *testing.T, home, stdin string, args ...string) (stdout string, code int) {
	t.Helper()
	bin := buildBinary(t)
	cmd := exec.Command(bin, append([]string{"statusline"}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// The binary is built once for the whole package: every shim test needs it,
// and building it per test would dominate the run.
var builtBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "switchboard-test-*")
	if err != nil {
		panic(err)
	}
	builtBinary = filepath.Join(dir, "switchboard")
	if out, err := exec.Command("go", "build", "-o", builtBinary, ".").CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("build: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func buildBinary(t *testing.T) string {
	t.Helper()
	return builtBinary
}

const payload = `{"session_id":"sess-1","model":{"display_name":"Opus 5"},` +
	`"context_window":{"context_window_size":1000000,"used_percentage":15}}`

func TestStatuslineStoresThePayloadAndRunsTheWrappedCommand(t *testing.T) {
	home := t.TempDir()
	out, code := shim(t, home, payload, "--", "echo", "my old statusline")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "my old statusline" {
		t.Errorf("stdout = %q, want only the wrapped command's output", out)
	}
	stored, err := os.ReadFile(filepath.Join(home, ".claude", "switchboard", "statusline", "sess-1.json"))
	if err != nil {
		t.Fatalf("payload not stored: %v", err)
	}
	if string(stored) != payload {
		t.Errorf("stored payload = %q, want it byte for byte", stored)
	}
}

// The separator reads better in a settings file but must not be required.
func TestStatuslineAcceptsTheCommandWithoutASeparator(t *testing.T) {
	out, code := shim(t, t.TempDir(), payload, "echo", "hello")
	if code != 0 || strings.TrimSpace(out) != "hello" {
		t.Errorf("stdout = %q, code = %d; want %q, 0", out, code, "hello")
	}
}

// The wrapped command must receive the payload unchanged, since it is a
// statusline of its own and needs the same JSON.
func TestStatuslinePassesStdinThrough(t *testing.T) {
	out, _ := shim(t, t.TempDir(), payload, "--", "cat")
	if out != payload {
		t.Errorf("wrapped command saw %q, want the payload unchanged", out)
	}
}

// With nothing to wrap, the shim is a sink: an empty statusline is correct.
func TestStatuslineWithNoWrappedCommandPrintsNothing(t *testing.T) {
	home := t.TempDir()
	out, code := shim(t, home, payload)
	if out != "" || code != 0 {
		t.Errorf("stdout = %q, code = %d; want empty, 0", out, code)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "switchboard", "statusline", "sess-1.json")); err != nil {
		t.Errorf("payload not stored: %v", err)
	}
}

// A payload switchboard cannot file must not stop the statusline rendering.
// Losing telemetry for one session is a far smaller failure than replacing
// someone's prompt with an error.
func TestStatuslineSurvivesAnUnusablePayload(t *testing.T) {
	for _, stdin := range []string{"", "not json at all", `{"no":"session"}`} {
		out, code := shim(t, t.TempDir(), stdin, "--", "echo", "still here")
		if code != 0 || strings.TrimSpace(out) != "still here" {
			t.Errorf("stdin %q: stdout = %q, code = %d; want %q, 0", stdin, out, code, "still here")
		}
	}
}

// Chaining the shim in front of a command must be invisible to Claude Code,
// which includes the exit code the command chose.
func TestStatuslineForwardsTheWrappedExitCode(t *testing.T) {
	_, code := shim(t, t.TempDir(), payload, "--", "sh", "-c", "exit 3")
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}
