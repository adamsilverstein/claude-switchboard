// Package activity derives a one-line summary of what an agent is doing from
// the tail of its transcript under ~/.claude/projects. Transcripts can be
// hundreds of megabytes, so only a bounded tail is ever read.
package activity

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Activity is what the transcript reveals about an agent.
type Activity struct {
	Summary  string    // last assistant text, collapsed to one line
	Modified time.Time // transcript mtime, a fallback for age display
}

// tailBytes bounds how much of the transcript is read. 256KB comfortably
// covers the last few assistant turns even with large tool results between
// them.
const tailBytes = 256 * 1024

// maxSummary bounds the summary length; the UI truncates further to fit.
const maxSummary = 500

// For returns the activity for one session. projectsDir is normally
// ~/.claude/projects. A missing or unreadable transcript is not an error:
// it returns a zero Activity, because a listing with a blank summary beats
// a listing that fails.
func For(projectsDir, cwd, sessionID string) Activity {
	if cwd == "" || sessionID == "" {
		return Activity{}
	}
	path := filepath.Join(projectsDir, Slug(cwd), sessionID+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return Activity{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Activity{Modified: info.ModTime()}
	}
	defer f.Close()

	size := info.Size()
	offset := size - tailBytes
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return Activity{Modified: info.ModTime()}
	}
	return Activity{Summary: lastAssistantText(buf, offset > 0), Modified: info.ModTime()}
}

// Slug converts a working directory to the directory name Claude Code uses
// under ~/.claude/projects: every "/" and "." becomes "-".
func Slug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
}

// transcriptLine is the subset of a transcript entry the summary needs.
// message.content is either a string or an array of typed blocks depending
// on the entry, so it is decoded in two steps.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// lastAssistantText scans newline-delimited JSON for the final assistant
// entry that carries text. When truncated is true the first line is the tail
// of a longer line and is dropped; a trailing partial line (an entry being
// written right now) simply fails to parse and is skipped.
func lastAssistantText(buf []byte, truncated bool) string {
	lines := bytes.Split(buf, []byte("\n"))
	if truncated && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var entry transcriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" || len(entry.Message.Content) == 0 {
			continue
		}
		if text := contentText(entry.Message.Content); text != "" {
			return oneLine(text)
		}
	}
	return ""
}

func contentText(raw json.RawMessage) string {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		return strings.Join(parts, " ")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

// oneLine collapses whitespace runs to single spaces and caps the length.
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteRune(' ')
		}
		space = false
		b.WriteRune(r)
		if b.Len() >= maxSummary {
			break
		}
	}
	return b.String()
}

// DefaultProjectsDir returns the standard transcript location,
// ~/.claude/projects.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
