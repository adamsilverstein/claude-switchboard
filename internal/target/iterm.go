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
// in ONE osascript invocation. The AppleScript bridge costs ~136ms per call,
// roughly fourteen times the entire registry scan, so it must never be
// called per agent. The separators are "\t" and "\n" string escapes rather
// than AppleScript's tab and linefeed constants: inside the iTerm2 tell
// block, "tab" resolves to iTerm's tab class and stringifies as the word
// "tab" instead of the character.
const listSessionsScript = `tell application "iTerm2"
	set out to ""
	set wi to 0
	repeat with w in windows
		set wi to wi + 1
		set ti to 0
		repeat with t in tabs of w
			set ti to ti + 1
			set si to 0
			repeat with s in sessions of t
				set si to si + 1
				set ttyPath to "-"
				try
					set ttyPath to tty of s
				end try
				set out to out & wi & "\t" & (id of w) & "\t" & ti & "\t" & si & "\t" & (id of s) & "\t" & ttyPath & "\n"
			end repeat
		end repeat
	end repeat
	return out
end tell`

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

// focusSessionScript selects a session by UUID: the session within its tab,
// the tab within its window, then raises and activates the window. The
// window follows its own Space rather than being dragged to the current one.
const focusSessionScript = `on run argv
	set targetId to item 1 of argv
	tell application "iTerm2"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with s in sessions of t
					if (id of s) is targetId then
						select s
						select t
						select w
						activate
						return "ok"
					end if
				end repeat
			end repeat
		end repeat
	end tell
	return "not found"
end run`

// FocusCommand returns the single osascript step that focuses an iTerm
// session by UUID.
func FocusCommand(sessionID string) Command {
	return Command{Name: "osascript", Args: []string{"-e", focusSessionScript, sessionID}}
}
