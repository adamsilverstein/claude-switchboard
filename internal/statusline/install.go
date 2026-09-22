package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the other half of the bargain the package doc describes:
// chaining the shim in front of whatever statusline you already run.
//
// Doing it by hand means hand-editing settings.json to nest one command
// inside another, which is exactly the kind of edit people put off. So
// `switchboard statusline --install` does it, and `--uninstall` puts it
// back.
//
// The edit is deliberately surgical. settings.json is a file people
// maintain by hand, with their own key order and their own spacing, and
// re-serializing it would sort the keys and reflow the whole thing to
// change one string. So the installer finds the byte span of the
// statusLine command and rewrites only that.

// Result describes what an install or uninstall did, so the command can
// print it and a test can assert on it.
type Result struct {
	Path    string // the settings file examined
	Backup  string // copy taken before writing; "" when nothing was written
	Before  string // the statusLine command as it was
	After   string // the statusLine command as it now is
	Changed bool   // false when it was already in the wanted state
}

// SettingsPath returns the Claude Code settings file, honouring
// CLAUDE_CONFIG_DIR the same way Claude Code itself does.
func SettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// Install rewrites the statusLine command in the settings file at path so
// that self runs in front of it. An absent settings file, or one with no
// statusLine at all, gets a statusLine that is the shim alone - which is a
// valid, empty statusline that still records the payload.
//
// Installing twice is not an error: the second call reports Changed false
// and leaves the file alone.
func Install(path, self string) (Result, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		raw = nil
	} else if err != nil {
		return Result{Path: path}, err
	}

	res := Result{Path: path}
	before, span, ok := command(raw)
	res.Before = before
	if ok && Installed(before) {
		res.After = before
		return res, nil
	}

	res.After = wrap(self, before)
	next, err := replace(raw, res.After, span, ok)
	if err != nil {
		return res, err
	}
	if res.Backup, err = write(path, raw, next); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// Uninstall unchains the shim, leaving whatever command it was wrapping.
// When it was wrapping nothing - the settings file had no statusLine
// before the install - the statusLine is removed entirely, which is the
// state it was found in.
func Uninstall(path string) (Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Result{Path: path}, err
	}
	res := Result{Path: path}
	before, span, ok := command(raw)
	res.Before, res.After = before, before
	if !ok || !Installed(before) {
		return res, nil
	}

	res.After = unwrap(before)
	var next []byte
	if res.After == "" {
		// Nothing was behind the shim, so there is no command to put
		// back. Dropping a key is the one edit that cannot be done in
		// place, and it is rare enough to be worth re-serializing for.
		if next, err = dropStatusLine(raw); err != nil {
			return res, err
		}
	} else if next, err = replace(raw, res.After, span, true); err != nil {
		return res, err
	}
	if res.Backup, err = write(path, raw, next); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// Installed reports whether a statusLine command already runs the shim.
// It matches on the shape rather than on an exact path, so a switchboard
// that has since moved is still recognised as installed.
func Installed(cmd string) bool {
	t := split(cmd)
	if len(t) < 2 || t[1].text != "statusline" {
		return false
	}
	return filepath.Base(t[0].text) == "switchboard"
}

// token is one word of a command line, with the offset just past it so a
// caller can take everything that follows verbatim.
type token struct {
	text string
	end  int
}

// split breaks a command into words the way a shell would, to the extent
// this needs: quoted runs hold together, everything else splits on spaces.
// It is not a shell parser and does not pretend to be one - it exists so
// that a switchboard installed under a path with a space in it is still
// recognised as the first word.
func split(cmd string) []token {
	var out []token
	var cur strings.Builder
	var quote rune
	open := false
	for i, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, open = r, true
		case r == ' ' || r == '\t':
			if open {
				out = append(out, token{cur.String(), i})
				cur.Reset()
				open = false
			}
		default:
			cur.WriteRune(r)
			open = true
		}
	}
	if open {
		out = append(out, token{cur.String(), len(cmd)})
	}
	return out
}

// wrap builds the command that runs the shim in front of inner. The "--"
// is not required by the shim, but it makes the settings file read as what
// it is: everything after the separator is somebody else's command.
func wrap(self, inner string) string {
	self = quote(self)
	if strings.TrimSpace(inner) == "" {
		return self + " statusline"
	}
	return self + " statusline -- " + inner
}

// unwrap returns the command the shim was put in front of, or "" when it
// was put in front of nothing. It cuts after the "statusline" word rather
// than at the first occurrence of that text, which a path like
// ~/src/statusline-tools/switchboard would otherwise land in the middle of.
func unwrap(cmd string) string {
	t := split(cmd)
	if len(t) < 2 {
		return ""
	}
	rest := strings.TrimSpace(cmd[t[1].end:])
	return strings.TrimSpace(strings.TrimPrefix(rest, "--"))
}

// quote shell-quotes a path that needs it. Claude Code hands the command
// to a shell, so a switchboard installed under a directory with a space in
// its name has to survive that.
func quote(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// command finds the statusLine command in raw: its decoded value, the byte
// span of the JSON string that holds it, and whether there was one at all.
func command(raw []byte) (value string, span [2]int, ok bool) {
	block := bytes.Index(raw, []byte(`"statusLine"`))
	if block < 0 {
		return "", span, false
	}
	i, ok := key(raw, block, `"command"`)
	if !ok {
		return "", span, false
	}
	end := i + 1
	for end < len(raw) {
		if raw[end] == '\\' {
			end += 2
			continue
		}
		if raw[end] == '"' {
			end++
			break
		}
		end++
	}
	if err := json.Unmarshal(raw[i:end], &value); err != nil {
		return "", span, false
	}
	return value, [2]int{i, end}, true
}

// key finds the value of the named JSON key at or after from, returning
// the offset of the value's opening quote.
//
// The search has to tell a key from a string that happens to read like
// one: `"type": "command"` sits directly above `"command": "..."`, and
// only the second is followed by a colon.
func key(raw []byte, from int, name string) (int, bool) {
	for at := from; ; {
		rel := bytes.Index(raw[at:], []byte(name))
		if rel < 0 {
			return 0, false
		}
		i := at + rel + len(name)
		at = i
		for i < len(raw) && isSpace(raw[i]) {
			i++
		}
		if i >= len(raw) || raw[i] != ':' {
			continue // a value, not a key
		}
		i++
		for i < len(raw) && isSpace(raw[i]) {
			i++
		}
		if i < len(raw) && raw[i] == '"' {
			return i, true
		}
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// replace puts cmd into raw. With a span it overwrites that string in
// place; without one it inserts a whole statusLine block after the opening
// brace, so a settings file that never had a statusline gains one.
func replace(raw []byte, cmd string, span [2]int, inPlace bool) ([]byte, error) {
	encoded, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	if inPlace {
		out := make([]byte, 0, len(raw)+len(encoded))
		out = append(out, raw[:span[0]]...)
		out = append(out, encoded...)
		return append(out, raw[span[1]:]...), nil
	}

	block := fmt.Sprintf("\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": %s\n  }", encoded)
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []byte("{" + block + "\n}\n"), nil
	}
	open := bytes.IndexByte(raw, '{')
	if open < 0 {
		return nil, errors.New("settings file is not a JSON object")
	}
	// A settings file with any key at all needs a comma after the block;
	// one that is only "{}" must not have one.
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
		block += ","
	}
	out := make([]byte, 0, len(raw)+len(block))
	out = append(out, raw[:open+1]...)
	out = append(out, block...)
	return append(out, raw[open+1:]...), nil
}

// dropStatusLine removes the statusLine key. This is the one path that
// re-serializes, so it is also the one path that can reorder keys; it runs
// only when the shim was installed into a file that had no statusline of
// its own, and only after a backup has been taken.
func dropStatusLine(raw []byte) ([]byte, error) {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	delete(settings, "statusLine")
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// write saves next over path, after copying what was there to a backup
// beside it. The write is atomic, because this is somebody's live settings
// file and a half-written one would break every Claude Code session on the
// machine.
func write(path string, prev, next []byte) (backup string, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if len(prev) > 0 {
		backup = fmt.Sprintf("%s.bak-switchboard-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, prev, 0o644); err != nil {
			return "", err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return backup, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		return backup, err
	}
	if err := tmp.Close(); err != nil {
		return backup, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return backup, err
	}
	return backup, os.Rename(tmp.Name(), path)
}
