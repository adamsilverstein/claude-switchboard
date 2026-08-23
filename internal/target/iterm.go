package target

import (
	"strconv"
	"strings"
)

// ItermSession is one pane in one iTerm2 tab, joined to agents by tty.
type ItermSession struct {
	WindowIndex  int    // 1-based front-to-back order at enumeration time
	WindowID     int    // stable iTerm window id
	TabIndex     int    // 1-based
	SessionIndex int    // 1-based within the tab (split panes)
	SessionID    string // iTerm session UUID, the stable focus handle
	TTY          string // "/dev/ttys008", or "" when iTerm reports none
}

// listSessionsScript enumerates every window, tab, and session with its tty
// in ONE osascript invocation.
//
// Each Apple Event costs roughly 20ms, so the cost of this script is set by
// how many properties it asks for, not how much AppleScript it runs. Asking
// per session ("repeat with s in sessions ... get tty of s") took ~2.0s on a
// 22-window desktop. Asking for all of one property at once - "tty of
// sessions of tabs of windows" - is three events total and takes ~0.13s for
// byte-identical output. The nested list shape (windows, then tabs, then
// sessions) survives in AppleScript even though coercing it to a string
// flattens it, so the indices are recovered by walking the lists locally,
// which costs nothing.
//
// The separators are "\t" and "\n" string escapes rather than AppleScript's
// tab and linefeed constants: inside the iTerm2 tell block, "tab" resolves
// to iTerm's tab class and stringifies as the word "tab" instead of the
// character.
const listSessionsScript = `tell application "iTerm2"
	set winIds to id of windows
	set sessIds to id of sessions of tabs of windows
	set sessTtys to tty of sessions of tabs of windows
end tell
if class of winIds is not list then set winIds to {winIds}
set out to ""
repeat with wi from 1 to (count of winIds)
	set wid to item wi of winIds
	set tabIds to item wi of sessIds
	set tabTtys to item wi of sessTtys
	repeat with ti from 1 to (count of tabIds)
		set sessOfTab to item ti of tabIds
		set ttyOfTab to item ti of tabTtys
		repeat with si from 1 to (count of sessOfTab)
			set sid to item si of sessOfTab
			set tv to item si of ttyOfTab
			if tv is missing value then set tv to "-"
			set out to out & wi & "\t" & wid & "\t" & ti & "\t" & si & "\t" & sid & "\t" & tv & "\n"
		end repeat
	end repeat
end repeat
return out`

// ListItermSessions enumerates iTerm2. An error here (iTerm not running,
// automation permission denied) should degrade to a listing without window
// info, not a crash - callers treat it as "no windows found".
func ListItermSessions(r Runner) ([]ItermSession, error) {
	out, err := r.Run("osascript", "-e", listSessionsScript)
	if err != nil {
		return nil, err
	}
	return ParseItermSessions(out), nil
}

// ParseItermSessions parses the tab-separated enumeration output. Split out
// so it can golden-tested against captured output.
func ParseItermSessions(out string) []ItermSession {
	var sessions []ItermSession
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) != 6 {
			continue
		}
		wi, err1 := strconv.Atoi(fields[0])
		wid, err2 := strconv.Atoi(fields[1])
		ti, err3 := strconv.Atoi(fields[2])
		si, err4 := strconv.Atoi(fields[3])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		tty := fields[5]
		if tty == "-" || tty == "missing value" {
			tty = ""
		}
		sessions = append(sessions, ItermSession{
			WindowIndex:  wi,
			WindowID:     wid,
			TabIndex:     ti,
			SessionIndex: si,
			SessionID:    fields[4],
			TTY:          tty,
		})
	}
	return sessions
}

// focusSessionScript brings iTerm forward and then selects a session, its
// tab, and its window. The window follows its own Space rather than being
// dragged to the current one.
//
// It takes the window id and the tab and session indices recorded at
// enumeration so it can address the session directly, and confirms the UUID
// at that position before selecting anything. Scanning every window instead
// cost up to 1.2s on a 22-window desktop; addressing it directly is ~0.2s,
// which is mostly osascript startup. The window is addressed by id, not
// index, because iTerm orders "windows" front-to-back and focusing anything
// reshuffles the indices.
//
// The scan remains as a fallback for when the layout moved between
// enumeration and focus - a tab dragged to another window, a pane closed -
// so a stale position self-heals instead of failing. It only records where
// the session turned up; the selecting happens afterwards, addressed by
// window id, so raising a window mid-scan cannot invalidate the loop.
//
// Activating comes before selecting, and that order is the whole fix for
// multi-monitor desktops. macOS answers an activate by bringing forward the
// app's window on the display the user is already looking at, so an
// activate issued after the select threw the selection away: picking an
// agent on the second monitor jumped to whatever iTerm window happened to
// sit on the first. Selecting last leaves the requested window as the one
// macOS settles on.
//
// The activate is still guarded, because it is by far the most expensive
// step: ~2.1s against ~0.03s for reading the frontmost property. Pressing
// enter in a picker that is itself running in iTerm never needs it, which is
// the common case; switching in from the Switchboard app still pays for it.
// It is also guarded on having found the session, so focusing an agent whose
// window has since closed does not yank iTerm forward for nothing.
const focusSessionScript = `on run argv
	set targetId to item 1 of argv
	set wid to (item 2 of argv) as integer
	set ti to (item 3 of argv) as integer
	set si to (item 4 of argv) as integer
	set found to false
	tell application "iTerm2"
		try
			if (id of session si of tab ti of (window id wid)) is targetId then
				set found to true
			end if
		end try
		if not found then
			set wins to windows
			repeat with wj from 1 to (count of wins)
				set w to item wj of wins
				set tabsOfW to tabs of w
				repeat with tj from 1 to (count of tabsOfW)
					set sessOfT to sessions of (item tj of tabsOfW)
					repeat with sj from 1 to (count of sessOfT)
						if (id of item sj of sessOfT) is targetId then
							set wid to id of w
							set ti to tj
							set si to sj
							set found to true
							exit repeat
						end if
					end repeat
					if found then exit repeat
				end repeat
				if found then exit repeat
			end repeat
		end if
		if found then
			if not frontmost then activate
			set w to window id wid
			set t to tab ti of w
			select session si of t
			select t
			select w
		end if
	end tell
	if not found then return "not found"
	return "ok"
end run`

// FocusCommand returns the single osascript step that focuses an iTerm
// session. The session's recorded position is passed as a hint; the script
// verifies the UUID there and falls back to a scan if it has moved.
func FocusCommand(s ItermSession) Command {
	return Command{Name: "osascript", Args: []string{
		"-e", focusSessionScript,
		s.SessionID,
		strconv.Itoa(s.WindowID),
		strconv.Itoa(s.TabIndex),
		strconv.Itoa(s.SessionIndex),
	}}
}
