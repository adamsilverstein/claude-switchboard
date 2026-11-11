// Package target owns every piece of knowledge about terminals: which
// windows exist, which one hosts a given agent, and how to focus it. Nothing
// outside this package mentions AppleScript, iTerm, or tmux. Each terminal
// (iTerm2, Terminal.app, Ghostty, tmux) is one backend file here, registered
// in the resolver.
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
	Backend   string // "iterm", "terminal", "ghostty", or "tmux"
	Desc      string // human-readable, e.g. "iTerm window 11, tab 1, session 1 (/dev/ttys008)"
	Focusable bool
	Reason    string    // why not focusable, when Focusable is false
	Commands  []Command // the steps Focus runs, in order

	// CloseCommands close the terminal session the agent ran in, and
	// are set only when that session is the agent's own: a plain iTerm
	// pane. A tmux pane's host window holds the whole tmux session, and
	// a background agent's viewer shows every background job, so
	// closing either would take other work down with it.
	CloseCommands []Command

	// TTY is the terminal the session runs on, "/dev/ttys008", when it is
	// known. A sweep checks it before closing: a pane can be shared, and
	// anything still running there besides the shell is not ours to hang
	// up.
	TTY string
}

// Focus runs a location's commands in order. The iTerm focus script reports
// "not found" on stdout rather than failing when the session has vanished
// between enumeration and focus, so that output is checked too.
func Focus(r Runner, loc Location) error {
	if !loc.Focusable {
		return fmt.Errorf("cannot focus: %s", loc.Reason)
	}
	for _, c := range loc.Commands {
		out, err := r.Run(c.Name, c.Args...)
		if err != nil {
			return fmt.Errorf("%s: %w", c.String(), err)
		}
		if strings.TrimSpace(out) == "not found" {
			return fmt.Errorf("window disappeared before it could be focused")
		}
	}
	return nil
}

// Close runs a location's close commands. Callers stop the agent first and
// wait for it to exit; closing a session with the agent still in it would
// hang it up mid-shutdown. A session that is already gone counts as closed,
// since the point was for it to be gone.
func Close(r Runner, loc Location) error {
	if len(loc.CloseCommands) == 0 {
		return fmt.Errorf("cannot close: not a session of its own")
	}
	for _, c := range loc.CloseCommands {
		out, err := r.Run(c.Name, c.Args...)
		if err != nil {
			return fmt.Errorf("%s: %w", c.String(), err)
		}
		if strings.TrimSpace(out) == "not found" {
			return nil
		}
	}
	return nil
}

// TTYQuiet reports whether nothing but a login shell is left on a terminal,
// which is what makes its session safe to close once an agent has exited.
// Background jobs, a wrapper that launched the agent, or MCP servers it has
// not reaped yet all count as something still running.
func TTYQuiet(r Runner, tty string) (bool, error) {
	name := strings.TrimPrefix(tty, "/dev/")
	if name == "" {
		return false, fmt.Errorf("no terminal to check")
	}
	out, err := r.Run("ps", "-t", name, "-o", "comm=")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		comm := strings.TrimSpace(line)
		if comm == "" {
			continue
		}
		if i := strings.LastIndex(comm, "/"); i >= 0 {
			comm = comm[i+1:]
		}
		if !quietProcs[strings.TrimPrefix(comm, "-")] {
			return false, nil
		}
	}
	return true, nil
}

// quietProcs are what an iTerm session holds when it is sitting at a prompt.
var quietProcs = map[string]bool{
	"login": true, "sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true,
}
