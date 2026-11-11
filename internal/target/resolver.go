package target

import (
	"fmt"
	"sort"
)

// Resolver joins agents to terminal windows. It enumerates iTerm once at
// construction (the AppleScript round trip is the expensive part) and
// answers every Resolve from that snapshot. The other terminals are
// enumerated only when an agent misses iTerm, and only once each; tmux
// lookups happen per agent but only for agents that carry a tmux field;
// and the process table is read once, and only when an answer needs it.
type Resolver struct {
	r        Runner
	byTTY    map[string]ItermSession
	itermErr error

	terminalScanned bool
	terminalByTTY   map[string]TerminalTab
	terminalErr     error

	ghosttyScanned bool
	ghosttyByTTY   map[string]GhosttyTerminal
	ghosttyErr     error

	treeScanned bool
	tree        *ProcTree

	viewersScanned bool
	viewers        []string
	viewersErr     error
}

// Agent is what the resolver needs to know about one agent: its pid, to
// find the terminal application it runs under; the tty ps reports for it;
// and the registry's tmux field.
type Agent struct {
	PID  int
	TTY  string
	Tmux string
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

// Resolve locates one agent. Agents inside tmux need two hops: the window
// hosting the tmux client, then the window and pane inside tmux. Agents
// with no tty (SDK sessions) are listed as not focusable rather than
// dropped. Background sessions have a tty no window owns and go through
// ResolveBackground.
func (res *Resolver) Resolve(a Agent) Location {
	if a.Tmux != "" {
		return res.resolveTmux(a.Tmux)
	}
	if a.TTY == "" {
		return Location{
			Backend: "iterm",
			Desc:    "no terminal",
			Reason:  "no controlling terminal (background or SDK session)",
		}
	}
	if w, ok := res.window(a.TTY, a.PID); ok {
		return Location{Backend: w.backend, Desc: w.desc, Focusable: true, Commands: w.commands, CloseCommands: w.close, TTY: a.TTY}
	}
	backend, reason := res.missReason(a.TTY, a.PID)
	return Location{
		Backend: backend,
		Desc:    fmt.Sprintf("unknown window (%s)", a.TTY),
		Reason:  reason,
	}
}

// hostWindow is a terminal window found to own a tty.
type hostWindow struct {
	backend  string
	desc     string // e.g. "iTerm window 3, tab 1, session 1 (/dev/ttys008)"
	short    string // e.g. "iTerm window 3, tab 1", for a tmux client or viewer
	commands []Command

	// close closes the session the tty belongs to. Only an iTerm pane
	// has one, and only Resolve hands it on: a tmux client or a viewer
	// window shows other work too.
	close []Command
}

// window finds the window that owns tty, trying iTerm first. On a miss it
// asks the terminal the process tree says pid runs under; with no pid, or
// an unrecognized host, it asks the others in turn and ignores their
// failures, since the agent may not be in either.
func (res *Resolver) window(tty string, pid int) (hostWindow, bool) {
	if s, ok := res.byTTY[tty]; ok {
		return itermWindow(s), true
	}
	host, _ := res.host(pid)
	if host == HostTerminal || host == HostUnknown {
		if t, ok := res.terminalTabs()[tty]; ok {
			return hostWindow{
				backend:  "terminal",
				desc:     fmt.Sprintf("Terminal window %d, tab %d (%s)", t.WindowIndex, t.TabIndex, tty),
				short:    fmt.Sprintf("Terminal window %d, tab %d", t.WindowIndex, t.TabIndex),
				commands: []Command{TerminalFocusCommand(t)},
			}, true
		}
	}
	if host == HostGhostty || host == HostUnknown {
		if t, ok := res.ghosttyTerminals()[tty]; ok {
			return hostWindow{
				backend:  "ghostty",
				desc:     fmt.Sprintf("Ghostty terminal (%s)", tty),
				short:    "Ghostty",
				commands: []Command{GhosttyFocusCommand(t)},
			}, true
		}
	}
	return hostWindow{}, false
}

// itermWindow describes an iTerm session as a window to focus.
func itermWindow(s ItermSession) hostWindow {
	return hostWindow{
		backend:  "iterm",
		desc:     fmt.Sprintf("iTerm window %d, tab %d, session %d (%s)", s.WindowIndex, s.TabIndex, s.SessionIndex, s.TTY),
		short:    fmt.Sprintf("iTerm window %d, tab %d", s.WindowIndex, s.TabIndex),
		commands: []Command{FocusCommand(s)},
		close:    []Command{CloseCommand(s)},
	}
}

// missReason explains why no window owns tty, naming the terminal the
// agent runs under when it is one switchboard cannot focus.
func (res *Resolver) missReason(tty string, pid int) (backend, reason string) {
	host, name := res.host(pid)
	switch host {
	case HostTerminal:
		if res.terminalErr != nil {
			return "terminal", fmt.Sprintf("Terminal enumeration failed: %v", res.terminalErr)
		}
		return "terminal", fmt.Sprintf("no Terminal window found for %s", tty)
	case HostGhostty:
		if res.ghosttyErr != nil {
			return "ghostty", fmt.Sprintf("Ghostty enumeration failed (focusing Ghostty needs version 1.4 or later): %v", res.ghosttyErr)
		}
		return "ghostty", fmt.Sprintf("no Ghostty terminal found for %s", tty)
	case HostUnknown:
		if name != "" {
			return "", fmt.Sprintf("running in %s, which switchboard cannot focus yet (it supports iTerm2, Terminal, Ghostty, and tmux)", name)
		}
	}
	return "iterm", res.noWindowReason(tty)
}

// host reports which terminal application pid runs under. A pid of 0, or a
// process table that could not be read, answers HostUnknown with no name.
func (res *Resolver) host(pid int) (Host, string) {
	if pid <= 0 {
		return HostUnknown, ""
	}
	if !res.treeScanned {
		res.treeScanned = true
		res.tree, _ = LoadProcTree(res.r)
	}
	if res.tree == nil {
		return HostUnknown, ""
	}
	return res.tree.Host(pid)
}

// terminalTabs enumerates Terminal.app on first use and indexes its tabs
// by tty. A failure is kept for missReason and leaves the index empty.
func (res *Resolver) terminalTabs() map[string]TerminalTab {
	if !res.terminalScanned {
		res.terminalScanned = true
		res.terminalByTTY = map[string]TerminalTab{}
		tabs, err := ListTerminalTabs(res.r)
		res.terminalErr = err
		for _, t := range tabs {
			if t.TTY != "" {
				res.terminalByTTY[t.TTY] = t
			}
		}
	}
	return res.terminalByTTY
}

// ghosttyTerminals enumerates Ghostty on first use and indexes its
// terminals by tty. A failure is kept for missReason and leaves the index
// empty.
func (res *Resolver) ghosttyTerminals() map[string]GhosttyTerminal {
	if !res.ghosttyScanned {
		res.ghosttyScanned = true
		res.ghosttyByTTY = map[string]GhosttyTerminal{}
		terms, err := ListGhosttyTerminals(res.r)
		res.ghosttyErr = err
		for _, t := range terms {
			if t.TTY != "" {
				res.ghosttyByTTY[t.TTY] = t
			}
		}
	}
	return res.ghosttyByTTY
}

// resolveTmux locates an agent in a tmux pane: the window hosting the
// session's client, then the window and pane inside tmux.
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
	tty, pid := clientTTY(res.r, ref.Session)
	if tty == "" {
		return Location{
			Backend: "tmux",
			Desc:    desc + " (detached)",
			Reason:  fmt.Sprintf("tmux session %s has no attached client", ref.Session),
		}
	}
	sel := tmuxSelectCommands(ref)
	host, ok := res.window(tty, pid)
	if !ok {
		// The tmux client is attached from a terminal switchboard
		// cannot raise. Selecting the window and pane still helps: the
		// hosting terminal shows the agent even if its window cannot
		// be raised from here.
		return Location{
			Backend:   "tmux",
			Desc:      fmt.Sprintf("%s, client on %s (not a window switchboard can raise)", desc, tty),
			Focusable: true,
			Commands:  sel,
		}
	}
	return Location{
		Backend:   "tmux",
		Desc:      fmt.Sprintf("%s, via %s (%s)", desc, host.short, tty),
		Focusable: true,
		Commands:  append(append([]Command{}, host.commands...), sel...),
	}
}

// ResolveBackground locates a background session. Its own tty belongs to
// the daemon, so the destination is the window hosting a "claude agents"
// viewer instead. iTerm viewers are preferred, and when several are open
// the frontmost one wins: it is the one the user looked at last, and any viewer can be
// switched to the job once it is on screen.
func (res *Resolver) ResolveBackground() Location {
	viewers, err := res.agentViewers()
	if err != nil {
		return Location{
			Backend: "iterm",
			Desc:    "background session (viewer unknown)",
			Reason:  fmt.Sprintf("background session; could not look for a claude agents viewer: %v", err),
		}
	}
	if len(viewers) == 0 {
		return Location{
			Backend: "iterm",
			Desc:    "background session (no viewer)",
			Reason:  "background session; no claude agents viewer is open",
		}
	}
	var hosted []ItermSession
	for _, tty := range viewers {
		if s, ok := res.byTTY[tty]; ok {
			hosted = append(hosted, s)
		}
	}
	if len(hosted) == 0 {
		// No viewer is in iTerm; one may be in another terminal.
		for _, tty := range viewers {
			if w, ok := res.window(tty, 0); ok {
				return Location{
					Backend:   w.backend,
					Desc:      fmt.Sprintf("background session, via claude agents in %s", w.desc),
					Focusable: true,
					Commands:  w.commands,
				}
			}
		}
		return Location{
			Backend: "iterm",
			Desc:    fmt.Sprintf("background session, viewer on %s (not in a window switchboard can raise)", viewers[0]),
			Reason:  res.noViewerWindowReason(viewers[0]),
		}
	}
	sort.SliceStable(hosted, func(i, j int) bool { return hosted[i].WindowIndex < hosted[j].WindowIndex })
	s := hosted[0]
	return Location{
		Backend:   "iterm",
		Desc:      fmt.Sprintf("background session, via claude agents in iTerm window %d, tab %d, session %d (%s)", s.WindowIndex, s.TabIndex, s.SessionIndex, s.TTY),
		Focusable: true,
		Commands:  []Command{FocusCommand(s)},
	}
}

func (res *Resolver) agentViewers() ([]string, error) {
	if !res.viewersScanned {
		res.viewersScanned = true
		res.viewers, res.viewersErr = AgentViewers(res.r)
	}
	return res.viewers, res.viewersErr
}

func (res *Resolver) noViewerWindowReason(tty string) string {
	if res.itermErr != nil {
		return fmt.Sprintf("iTerm enumeration failed: %v", res.itermErr)
	}
	return fmt.Sprintf("background session; its claude agents viewer on %s is not in a window switchboard can raise", tty)
}

func (res *Resolver) noWindowReason(tty string) string {
	if res.itermErr != nil {
		return fmt.Sprintf("iTerm enumeration failed: %v", res.itermErr)
	}
	return fmt.Sprintf("no iTerm window found for %s", tty)
}
