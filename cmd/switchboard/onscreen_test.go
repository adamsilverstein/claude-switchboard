package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
)

// tmuxRunner answers tmux queries and fails anything else, so a test that
// unexpectedly shells out to osascript is loud rather than silent.
type tmuxRunner struct {
	clients string
	err     error
	calls   int
}

func (r *tmuxRunner) Run(name string, args ...string) (string, error) {
	if name != "tmux" {
		return "", fmt.Errorf("unexpected command %s", name)
	}
	r.calls++
	return r.clients, r.err
}

func onScreenFixtures() []registry.Agent {
	return []registry.Agent{
		{PID: 100, Name: "plain cli", Entrypoint: "cli"},
		{PID: 200, Name: "sdk observer", Entrypoint: "sdk-cli"},
		{PID: 300, Name: "in attached tmux", Entrypoint: "cli", Tmux: "work:@1.%1"},
		{PID: 400, Name: "in detached tmux", Entrypoint: "cli", Tmux: "abandoned:@2.%2"},
	}
}

func names(agents []registry.Agent) string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return strings.Join(out, ",")
}

func TestOnScreenDropsSDKAndDetachedTmux(t *testing.T) {
	r := &tmuxRunner{clients: "work\n"}
	got := names(onScreen(r, onScreenFixtures()))
	if got != "plain cli,in attached tmux" {
		t.Errorf("kept %q", got)
	}
}

func TestOnScreenKeepsTmuxAgentsWhenTmuxCannotAnswer(t *testing.T) {
	r := &tmuxRunner{err: fmt.Errorf("no server running")}
	got := names(onScreen(r, onScreenFixtures()))
	if got != "plain cli,in attached tmux,in detached tmux" {
		t.Errorf("kept %q", got)
	}
}

func TestOnScreenSkipsTmuxQueryWithoutTmuxAgents(t *testing.T) {
	r := &tmuxRunner{}
	agents := []registry.Agent{{PID: 100, Name: "plain cli", Entrypoint: "cli"}}
	if got := names(onScreen(r, agents)); got != "plain cli" {
		t.Errorf("kept %q", got)
	}
	if r.calls != 0 {
		t.Errorf("ran %d tmux calls, want none", r.calls)
	}
}
