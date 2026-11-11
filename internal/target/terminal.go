package target

import (
	"strconv"
	"strings"
)

// TerminalTab is one tab in one Terminal.app window, joined to agents by
// tty. Terminal.app has no split panes, so a tab is the whole address.
type TerminalTab struct {
	WindowIndex int    // 1-based front-to-back order at enumeration time
	WindowID    int    // stable window id
	TabIndex    int    // 1-based
	TTY         string // "/dev/ttys011", or "" when none is reported
}

// listTerminalScript enumerates Terminal.app the same way the iTerm script
// does: one Apple Event per property, with the nested window/tab lists
// walked locally. See listSessionsScript for why.
//
// It returns nothing when Terminal is not running. Asking whether it runs
// goes through Launch Services, not Apple Events, so it neither launches
// Terminal nor triggers the automation prompt for someone who never uses it.
const listTerminalScript = `if application id "com.apple.Terminal" is not running then return ""
tell application id "com.apple.Terminal"
	set winIds to id of windows
	set tabTtys to tty of tabs of windows
end tell
set out to ""
repeat with wi from 1 to (count of winIds)
	set ttysOfW to item wi of tabTtys
	repeat with ti from 1 to (count of ttysOfW)
		set tv to item ti of ttysOfW
		if tv is missing value then set tv to "-"
		set out to out & wi & "\t" & (item wi of winIds) & "\t" & ti & "\t" & tv & "\n"
	end repeat
end repeat
return out`

// ListTerminalTabs enumerates Terminal.app. It returns no tabs and no error
// when Terminal is not running.
func ListTerminalTabs(r Runner) ([]TerminalTab, error) {
	out, err := r.Run("osascript", "-e", listTerminalScript)
	if err != nil {
		return nil, err
	}
	return ParseTerminalTabs(out), nil
}

// ParseTerminalTabs parses the enumeration output.
func ParseTerminalTabs(out string) []TerminalTab {
	var tabs []TerminalTab
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) != 4 {
			continue
		}
		wi, err1 := strconv.Atoi(fields[0])
		wid, err2 := strconv.Atoi(fields[1])
		ti, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		tty := fields[3]
		if tty == "-" || tty == "missing value" {
			tty = ""
		}
		tabs = append(tabs, TerminalTab{WindowIndex: wi, WindowID: wid, TabIndex: ti, TTY: tty})
	}
	return tabs
}

// focusTerminalScript selects a tab, raises its window, and brings
// Terminal forward. The tab is addressed at its recorded window id and
// index after checking the tty there, with a scan as the fallback when the
// layout moved, as in the iTerm focus script.
//
// The window is raised on both sides of the activate: activating Terminal
// raises its key window over the one just put in front. If Terminal quit
// after enumeration, the script reports "not found" instead of relaunching
// it.
const focusTerminalScript = `on run argv
	set targetTty to item 1 of argv
	set wid to (item 2 of argv) as integer
	set ti to (item 3 of argv) as integer
	if application id "com.apple.Terminal" is not running then return "not found"
	set found to false
	tell application id "com.apple.Terminal"
		try
			if (tty of tab ti of (window id wid)) is targetTty then set found to true
		end try
		if not found then
			set winIds to id of windows
			set tabTtys to tty of tabs of windows
			repeat with wj from 1 to (count of winIds)
				set ttysOfW to item wj of tabTtys
				repeat with tj from 1 to (count of ttysOfW)
					if (item tj of ttysOfW) is targetTty then
						set wid to item wj of winIds
						set ti to tj
						set found to true
						exit repeat
					end if
				end repeat
				if found then exit repeat
			end repeat
		end if
		if found then
			set w to window id wid
			set selected of tab ti of w to true
			set index of w to 1
			if not frontmost then
				activate
				set index of w to 1
			end if
		end if
	end tell
	if not found then return "not found"
	return "ok"
end run`

// TerminalFocusCommand returns the osascript step that focuses a
// Terminal.app tab.
func TerminalFocusCommand(t TerminalTab) Command {
	return Command{Name: "osascript", Args: []string{
		"-e", focusTerminalScript,
		t.TTY,
		strconv.Itoa(t.WindowID),
		strconv.Itoa(t.TabIndex),
	}}
}
