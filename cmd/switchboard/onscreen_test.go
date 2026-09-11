package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adamsilverstein/claude-switchboard/internal/registry"
	"github.com/adamsilverstein/claude-switchboard/internal/target"
)

// tmuxRunner answers tmux, ps, and iTerm enumeration queries and fails
// anything else. An enumeration is only answered when a test asked for one:
// the default failure is what "iTerm cannot be asked" looks like, which
// keeps every agent listed.
type tmuxRunner struct {
	clients     string
	err         error
	calls       int
	ps          string
	psErr       error
	psCalls     int
	iterm       string
	itermCalls  int
	itermAnswer bool
}

func (r *tmuxRunner) Run(name string, args ...string) (string, error) {
	switch name {
	case "tmux":
		r.calls++
		return r.clients, r.err
	case "ps":
		r.psCalls++
		return r.ps, r.psErr
	case "osascript":
		r.itermCalls++
		if !r.itermAnswer {
			return "", fmt.Errorf("iTerm got an error: Application isn't running")
		}
		return r.iterm, nil
	}
	return "", fmt.Errorf("unexpected command %s", name)
}

// itermWindows formats an enumeration listing one window per tty.
func itermWindows(ttys ...string) string {
	out := ""
	for i, tty := range ttys {
		out += fmt.Sprintf("%d\t%d\t1\t1\tUUID-%d\t%s\n", i+1, 100+i, i, tty)
	}
	return out
}

// filter runs onScreen with a window index built on the same fake runner.
func filter(r target.Runner, agents []registry.Agent, ttys map[int]string) []registry.Agent {
	return onScreen(r, target.NewWindowIndex(r, time.Second), agents, ttys)
}

func onScreenFixtures() []registry.Agent {
	return []registry.Agent{
		{PID: 100, Name: "plain cli", Entrypoint: "cli"},
		{PID: 200, Name: "sdk observer", Entrypoint: "sdk-cli"},
		{PID: 300, Name: "in attached tmux", Entrypoint: "cli", Tmux: "work:@1.%1"},
		{PID: 400, Name: "in detached tmux", Entrypoint: "cli", Tmux: "abandoned:@2.%2"},
		{PID: 500, Name: "background job", Entrypoint: "cli", Kind: "bg"},
		{PID: 600, Name: "vscode extension", Entrypoint: "claude-vscode"},
	}
}

func names(agents []registry.Agent) string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return strings.Join(out, ",")
}

func TestOnScreenDropsSDKDetachedTmuxAndUnviewedBackground(t *testing.T) {
	r := &tmuxRunner{clients: "work\n", ps: "ttys008  claude /color\n"}
	got := names(filter(r, onScreenFixtures(), nil))
	if got != "plain cli,in attached tmux" {
		t.Errorf("kept %q", got)
	}
}

func TestOnScreenKeepsBackgroundWithViewer(t *testing.T) {
	r := &tmuxRunner{clients: "work\n", ps: "ttys005  claude agents\n"}
	got := names(filter(r, onScreenFixtures(), nil))
	if got != "plain cli,in attached tmux,background job" {
		t.Errorf("kept %q", got)
	}
}

func TestOnScreenKeepsTmuxAgentsWhenTmuxCannotAnswer(t *testing.T) {
	r := &tmuxRunner{err: fmt.Errorf("no server running"), psErr: fmt.Errorf("ps failed")}
	got := names(filter(r, onScreenFixtures(), nil))
	if got != "plain cli,in attached tmux,in detached tmux,background job" {
		t.Errorf("kept %q", got)
	}
}

func TestOnScreenSkipsTmuxQueryWithoutTmuxAgents(t *testing.T) {
	r := &tmuxRunner{}
	agents := []registry.Agent{{PID: 100, Name: "plain cli", Entrypoint: "cli"}}
	if got := names(filter(r, agents, nil)); got != "plain cli" {
		t.Errorf("kept %q", got)
	}
	if r.calls != 0 || r.psCalls != 0 {
		t.Errorf("ran %d tmux and %d ps calls, want none", r.calls, r.psCalls)
	}
}

// orphanFixtures pairs a windowed agent with one whose tty no iTerm window
// owns: the shape left behind when iTerm restarts and does not re-adopt a
// session its server kept running.
func orphanFixtures() ([]registry.Agent, map[int]string) {
	agents := []registry.Agent{
		{PID: 100, Name: "windowed", Entrypoint: "cli"},
		{PID: 700, Name: "orphaned", Entrypoint: "cli"},
	}
	return agents, map[int]string{100: "/dev/ttys001", 700: "/dev/ttys006"}
}

func TestOnScreenDropsAgentWhoseWindowIsGone(t *testing.T) {
	r := &tmuxRunner{itermAnswer: true, iterm: itermWindows("/dev/ttys001")}
	agents, ttys := orphanFixtures()
	if got := names(filter(r, agents, ttys)); got != "windowed" {
		t.Errorf("kept %q; want only the agent with a window", got)
	}
}

func TestOnScreenKeepsAgentsWhenITermCannotAnswer(t *testing.T) {
	r := &tmuxRunner{}
	agents, ttys := orphanFixtures()
	if got := names(filter(r, agents, ttys)); got != "windowed,orphaned" {
		t.Errorf("kept %q; want both when iTerm cannot be asked", got)
	}
}

func TestOnScreenKeepsAgentWithNoRecordedTTY(t *testing.T) {
	r := &tmuxRunner{itermAnswer: true, iterm: itermWindows("/dev/ttys001")}
	agents := []registry.Agent{{PID: 800, Name: "no tty recorded", Entrypoint: "cli"}}
	if got := names(filter(r, agents, nil)); got != "no tty recorded" {
		t.Errorf("kept %q; want the agent kept when ps reported no tty", got)
	}
}

// A tmux agent's own tty belongs to the tmux server, so it never appears in
// the iTerm enumeration. Whether it is reachable is the attached-client
// question, already asked above, and the window check must not second-guess
// it.
func TestOnScreenKeepsAttachedTmuxAgentWithNoITermWindowOfItsOwn(t *testing.T) {
	r := &tmuxRunner{clients: "work\n", itermAnswer: true, iterm: itermWindows("/dev/ttys001")}
	agents := []registry.Agent{{PID: 300, Name: "in attached tmux", Entrypoint: "cli", Tmux: "work:@1.%1"}}
	ttys := map[int]string{300: "/dev/ttys012"}
	if got := names(filter(r, agents, ttys)); got != "in attached tmux" {
		t.Errorf("kept %q; want the tmux agent kept", got)
	}
}

// A background session's pty belongs to the Claude Code daemon and is never
// in a window either. Its viewer is what makes it reachable.
func TestOnScreenKeepsViewedBackgroundSessionWithNoWindowOfItsOwn(t *testing.T) {
	r := &tmuxRunner{ps: "ttys005  claude agents\n", itermAnswer: true, iterm: itermWindows("/dev/ttys005")}
	agents := []registry.Agent{{PID: 500, Name: "background job", Entrypoint: "cli", Kind: "bg"}}
	ttys := map[int]string{500: "/dev/ttys099"}
	if got := names(filter(r, agents, ttys)); got != "background job" {
		t.Errorf("kept %q; want the viewed background session kept", got)
	}
}

func TestOnScreenSkipsITermQueryWhenEveryAgentIsWindowed(t *testing.T) {
	r := &tmuxRunner{itermAnswer: true, iterm: itermWindows("/dev/ttys001", "/dev/ttys006")}
	agents, ttys := orphanFixtures()
	if got := names(filter(r, agents, ttys)); got != "windowed,orphaned" {
		t.Errorf("kept %q; want both", got)
	}
	if r.itermCalls != 1 {
		t.Errorf("enumerated %d times; want 1", r.itermCalls)
	}
}
