package target

import "strings"

// GhosttyTerminal is one terminal surface in Ghostty (a tab or one side of
// a split), joined to agents by tty.
type GhosttyTerminal struct {
	ID  string // Ghostty's stable surface id, the focus handle
	TTY string
}

// listGhosttyScript enumerates every Ghostty terminal with its tty, two
// Apple Events in all. The application's own "terminals" element is flat,
// so no window/tab walk is needed: focusing a terminal brings its window and
// tab along.
//
// The tty property arrived in Ghostty 1.4. Against 1.3 the script fails to
// compile, which surfaces as an enumeration error naming the cause, and the
// agent stays listed as not focusable.
const listGhosttyScript = `if application id "com.mitchellh.ghostty" is not running then return ""
tell application id "com.mitchellh.ghostty"
	set termIds to id of terminals
	set termTtys to tty of terminals
end tell
set out to ""
repeat with i from 1 to (count of termIds)
	set tv to item i of termTtys
	if tv is missing value then set tv to "-"
	set out to out & (item i of termIds) & "\t" & tv & "\n"
end repeat
return out`

// ListGhosttyTerminals enumerates Ghostty. It returns nothing and no error
// when Ghostty is not running.
func ListGhosttyTerminals(r Runner) ([]GhosttyTerminal, error) {
	out, err := r.Run("osascript", "-e", listGhosttyScript)
	if err != nil {
		return nil, err
	}
	return ParseGhosttyTerminals(out), nil
}

// ParseGhosttyTerminals parses the enumeration output.
func ParseGhosttyTerminals(out string) []GhosttyTerminal {
	var terms []GhosttyTerminal
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) != 2 || fields[0] == "" {
			continue
		}
		tty := fields[1]
		if tty == "-" || tty == "missing value" {
			tty = ""
		}
		terms = append(terms, GhosttyTerminal{ID: fields[0], TTY: tty})
	}
	return terms
}

// focusGhosttyScript focuses a terminal by id. Ghostty's focus command
// selects the tab and raises the window but does not bring Ghostty forward
// when it is in the background, so it activates as well and then focuses
// again: activating raises Ghostty's key window, which may not be the one
// just focused.
const focusGhosttyScript = `on run argv
	set targetId to item 1 of argv
	tell application id "com.mitchellh.ghostty"
		set matches to (every terminal whose id is targetId)
		if (count of matches) is 0 then return "not found"
		set t to item 1 of matches
		focus t
		if not frontmost then
			activate
			focus t
		end if
	end tell
	return "ok"
end run`

// GhosttyFocusCommand returns the osascript step that focuses a Ghostty
// terminal.
func GhosttyFocusCommand(t GhosttyTerminal) Command {
	return Command{Name: "osascript", Args: []string{"-e", focusGhosttyScript, t.ID}}
}
