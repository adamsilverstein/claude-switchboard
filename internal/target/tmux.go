package target

import (
	"fmt"
	"strings"
)

// TmuxRef is the parsed form of the registry's tmux field, which looks like
// "claude-e96b2a6e:@2.%2" - session name, window id, pane id.
type TmuxRef struct {
	Session string
	Window  string // "@2"
	Pane    string // "%2"
}

// ParseTmuxRef parses the registry's tmux field. ok is false when the field
// does not have the session:window.pane shape.
func ParseTmuxRef(field string) (TmuxRef, bool) {
	colon := strings.Index(field, ":")
	if colon <= 0 {
		return TmuxRef{}, false
	}
	rest := field[colon+1:]
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return TmuxRef{}, false
	}
	return TmuxRef{
		Session: field[:colon],
		Window:  rest[:dot],
		Pane:    rest[dot+1:],
	}, true
}

// clientTTY returns the tty of the first client attached to a tmux session,
// or "" when the session is detached.
func clientTTY(r Runner, session string) string {
	out, err := r.Run("tmux", "list-clients", "-t", session, "-F", "#{client_tty}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if tty := strings.TrimSpace(line); tty != "" {
			return tty
		}
	}
	return ""
}

// tmuxSelectCommands selects the agent's window and pane inside tmux.
func tmuxSelectCommands(ref TmuxRef) []Command {
	windowTarget := fmt.Sprintf("%s:%s", ref.Session, ref.Window)
	return []Command{
		{Name: "tmux", Args: []string{"select-window", "-t", windowTarget}},
		{Name: "tmux", Args: []string{"select-pane", "-t", windowTarget + "." + ref.Pane}},
	}
}
