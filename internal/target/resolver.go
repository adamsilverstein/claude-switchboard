package target

import "fmt"

// Resolver joins agents to terminal windows. It enumerates iTerm once at
// construction (the AppleScript round trip is the expensive part) and
// answers every Resolve from that snapshot; tmux lookups happen per agent
// but only for agents that carry a tmux field.
type Resolver struct {
	r        Runner
	byTTY    map[string]ItermSession
	itermErr error
}

// NewResolver enumerates iTerm2 and returns a resolver. An iTerm failure
// (not running, automation permission denied) is remembered rather than
// returned: agents then resolve as not focusable with that reason, so the
// listing itself keeps working.
func NewResolver(r Runner) *Resolver {
	res := &Resolver{r: r, byTTY: map[string]ItermSession{}}
	sessions, err := ListItermSessions(r)
	if err != nil {
		res.itermErr = err
		return res
	}
	for _, s := range sessions {
		if s.TTY != "" {
			res.byTTY[s.TTY] = s
		}
	}
	return res
}

// Resolve locates one agent given its registry tmux field and the tty of its
// process. Agents inside tmux need two hops: the iTerm window hosting the
// tmux client, then the window and pane inside tmux. Agents with no tty
// (background and SDK sessions) are listed as not focusable rather than
// dropped.
func (res *Resolver) Resolve(tmuxField, tty string) Location {
	if tmuxField != "" {
		return res.resolveTmux(tmuxField)
	}
	if tty == "" {
		return Location{
			Backend: "iterm",
			Desc:    "no terminal",
			Reason:  "no controlling terminal (background or SDK session)",
		}
	}
	s, ok := res.byTTY[tty]
	if !ok {
		return Location{
			Backend: "iterm",
			Desc:    fmt.Sprintf("unknown window (%s)", tty),
			Reason:  res.noWindowReason(tty),
		}
	}
	return Location{
		Backend:   "iterm",
		Desc:      fmt.Sprintf("iTerm window %d, tab %d, session %d (%s)", s.WindowIndex, s.TabIndex, s.SessionIndex, tty),
		Focusable: true,
		Commands:  []Command{FocusCommand(s)},
	}
}

func (res *Resolver) resolveTmux(field string) Location {
	ref, ok := ParseTmuxRef(field)
	if !ok {
		return Location{
			Backend: "tmux",
			Desc:    fmt.Sprintf("tmux (%s)", field),
			Reason:  fmt.Sprintf("unrecognized tmux field %q", field),
		}
	}
	desc := fmt.Sprintf("tmux session %s, window %s, pane %s", ref.Session, ref.Window, ref.Pane)
	tty := clientTTY(res.r, ref.Session)
	if tty == "" {
		return Location{
			Backend: "tmux",
			Desc:    desc + " (detached)",
			Reason:  fmt.Sprintf("tmux session %s has no attached client", ref.Session),
		}
	}
	sel := tmuxSelectCommands(ref)
	host, ok := res.byTTY[tty]
	if !ok {
		// The tmux client is attached from a terminal iTerm does not
		// know about. Selecting the window and pane still helps: the
		// hosting terminal shows the agent even if its window cannot
		// be raised from here.
		return Location{
			Backend:   "tmux",
			Desc:      fmt.Sprintf("%s, client on %s (not an iTerm window)", desc, tty),
			Focusable: true,
			Commands:  sel,
		}
	}
	return Location{
		Backend:   "tmux",
		Desc:      fmt.Sprintf("%s, via iTerm window %d, tab %d (%s)", desc, host.WindowIndex, host.TabIndex, tty),
		Focusable: true,
		Commands:  append([]Command{FocusCommand(host)}, sel...),
	}
}

func (res *Resolver) noWindowReason(tty string) string {
	if res.itermErr != nil {
		return fmt.Sprintf("iTerm enumeration failed: %v", res.itermErr)
	}
	return fmt.Sprintf("no iTerm window found for %s", tty)
}
