// Package target owns every piece of knowledge about terminals: which
// windows exist, which one hosts a given agent, and how to focus it. Nothing
// outside this package mentions AppleScript, iTerm, or tmux. Adding another
// terminal (Ghostty, Terminal.app) should mean adding one backend file here
// and registering it in the resolver.
package target

import (
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes external commands. It exists so tests can capture the
// commands a backend would run instead of running them.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ExecRunner runs commands for real.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}

// Command is one step a Focus would execute, printable for --dry-run.
type Command struct {
	Name string
	Args []string
}

func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, c.Name)
	for _, a := range c.Args {
		if strings.ContainsAny(a, " \t\n\"'") {
			a = fmt.Sprintf("%q", a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// Location says where an agent's terminal lives and how to focus it.
type Location struct {
	Backend   string // "iterm" or "tmux"
	Desc      string // human-readable, e.g. "iTerm window 11, tab 1, session 1 (/dev/ttys008)"
	Focusable bool
	Reason    string    // why not focusable, when Focusable is false
	Commands  []Command // the steps Focus runs, in order
}
