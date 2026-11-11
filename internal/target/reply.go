package target

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ReplyTarget is where a reply would be typed: the terminal session an
// agent's own tty belongs to. It is resolved fresh for every reply, never
// cached, because typing into the wrong session is far worse than focusing
// the wrong window.
type ReplyTarget struct {
	Backend string // "iterm" or "tmux"; "" when there is nowhere to type
	Desc    string
	Reason  string // why not, when Backend is ""

	// TTY is the agent's own terminal. The session found for it is
	// checked against this again at the moment of typing, so a pane that
	// was closed and reopened on another tty is refused rather than typed
	// into.
	TTY string

	Iterm ItermSession // when Backend is "iterm"
	Pane  string       // tmux pane id, "%2", when Backend is "tmux"
}

// maxReplyRunes bounds a reply. The input is one line, and anything longer
// than this is better written in the agent's own window.
const maxReplyRunes = 2000

// CleanReply trims a reply and refuses anything that is not one line of
// plain text. Control characters are the point: an escape sequence or a
// stray carriage return typed into a terminal is a keystroke, not text.
func CleanReply(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("nothing to send")
	}
	if !utf8.ValidString(text) {
		return "", errors.New("reply is not valid text")
	}
	if utf8.RuneCountInString(text) > maxReplyRunes {
		return "", fmt.Errorf("reply is over %d characters; answer in the agent's own window", maxReplyRunes)
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return "", errors.New("reply must be one line of plain text")
		}
	}
	return text, nil
}

// ResolveReply finds where a reply to the agent on tty should be typed. It
// needs the agent's own tty in both cases: inside tmux it is the pane's, and
// outside it is the iTerm session's. With no tty there is nothing to check
// the session against, so there is nowhere safe to type.
func (res *Resolver) ResolveReply(tmuxField, tty string) ReplyTarget {
	if tty == "" {
		return ReplyTarget{Reason: "no controlling terminal to type into"}
	}
	if tmuxField != "" {
		ref, ok := ParseTmuxRef(tmuxField)
		if !ok {
			return ReplyTarget{Reason: fmt.Sprintf("unrecognized tmux field %q", tmuxField)}
		}
		return ReplyTarget{
			Backend: "tmux",
			Desc:    fmt.Sprintf("tmux pane %s (%s)", ref.Pane, tty),
			TTY:     tty,
			Pane:    ref.Pane,
		}
	}
	s, ok := res.byTTY[tty]
	if !ok {
		return ReplyTarget{Reason: res.noWindowReason(tty)}
	}
	return ReplyTarget{
		Backend: "iterm",
		Desc:    fmt.Sprintf("iTerm window %d, tab %d, session %d (%s)", s.WindowIndex, s.TabIndex, s.SessionIndex, tty),
		TTY:     tty,
		Iterm:   s,
	}
}

// replyPause separates the text from the return that submits it. Claude
// Code reads a burst of input as a paste, and a return inside the paste is a
// line break in the prompt rather than a send. A variable so tests do not
// wait on it.
var replyPause = 200 * time.Millisecond

// SendReply types text into the target and presses return. The text must
// already have been through CleanReply.
func SendReply(r Runner, t ReplyTarget, text string) error {
	switch t.Backend {
	case "iterm":
		_, err := r.Run("osascript", "-e", sendTextScript,
			t.Iterm.SessionID, t.TTY,
			strconv.Itoa(t.Iterm.WindowID),
			strconv.Itoa(t.Iterm.TabIndex),
			strconv.Itoa(t.Iterm.SessionIndex),
			text,
			strconv.FormatFloat(replyPause.Seconds(), 'f', 2, 64))
		return err
	case "tmux":
		out, err := r.Run("tmux", "display-message", "-p", "-t", t.Pane, "#{pane_tty}")
		if err != nil {
			return err
		}
		if got := strings.TrimSpace(out); got != t.TTY {
			return fmt.Errorf("tmux pane %s is on %s now, not %s; not sending", t.Pane, got, t.TTY)
		}
		if _, err := r.Run("tmux", "send-keys", "-t", t.Pane, "-l", "--", text); err != nil {
			return err
		}
		time.Sleep(replyPause)
		_, err = r.Run("tmux", "send-keys", "-t", t.Pane, "Enter")
		return err
	default:
		if t.Reason == "" {
			return errors.New("nowhere to send the reply")
		}
		return fmt.Errorf("cannot reply: %s", t.Reason)
	}
}

// sendTextScript types a reply into one iTerm session and submits it.
//
// The session is addressed the way focusSessionScript addresses it - by its
// recorded position, confirmed by UUID, with a scan as the fallback - and
// then its tty is compared with the agent's before anything is typed. A
// UUID cannot be reused, but the check is cheap and is the same guard the
// tmux path has.
//
// The text is written without a newline, and the return follows after a
// pause as a bare carriage return: Enter in a terminal is CR, and the pause
// keeps it out of the burst Claude Code would read as a paste.
//
// Failures raise an AppleScript error, which osascript reports on stderr and
// ExecRunner turns into the error message.
const sendTextScript = `on run argv
	set targetId to item 1 of argv
	set targetTty to item 2 of argv
	set wid to (item 3 of argv) as integer
	set ti to (item 4 of argv) as integer
	set si to (item 5 of argv) as integer
	set msg to item 6 of argv
	set pauseFor to (item 7 of argv) as real
	tell application "iTerm2"
		set s to missing value
		try
			set c to session si of tab ti of (window id wid)
			if (id of c) is targetId then set s to c
		end try
		if s is missing value then
			repeat with w in windows
				repeat with t in tabs of w
					repeat with c in sessions of t
						if (id of c) is targetId then
							set s to contents of c
							exit repeat
						end if
					end repeat
					if s is not missing value then exit repeat
				end repeat
				if s is not missing value then exit repeat
			end repeat
		end if
		if s is missing value then error "the window has closed; not sending"
		if (tty of s) is not targetTty then error "the session's terminal changed; not sending"
		tell s to write text msg newline false
		delay pauseFor
		tell s to write text (character id 13) newline false
	end tell
	return "ok"
end run`
