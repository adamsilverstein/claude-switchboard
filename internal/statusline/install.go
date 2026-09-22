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
	loc, err := command(raw)
	if err != nil {
		return res, err
	}
	res.Before = loc.value
	if loc.ok && Installed(loc.value) {
		res.After = loc.value
		return res, nil
	}

	res.After = wrap(self, loc.value)
	next, err := replace(raw, res.After, loc)
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
	if errors.Is(err, os.ErrNotExist) {
		return Result{Path: path}, nil // nothing installed, nothing to undo
	} else if err != nil {
		return Result{Path: path}, err
	}
	res := Result{Path: path}
	loc, err := command(raw)
	if err != nil {
		return res, err
	}
	res.Before, res.After = loc.value, loc.value
	if !loc.ok || !Installed(loc.value) {
		return res, nil
	}

	res.After = unwrap(loc.value)
	var next []byte
	if res.After == "" {
		// Nothing was behind the shim, so there is no command to put
		// back. Dropping a key is the one edit that cannot be done in
		// place, and it is rare enough to be worth re-serializing for.
		if next, err = dropStatusLine(raw); err != nil {
			return res, err
		}
	} else if next, err = replace(raw, res.After, loc); err != nil {
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

// location is where the statusLine command sits in a settings file.
type location struct {
	value string // the decoded command
	span  [2]int // byte span of the JSON string holding the command
	ok    bool   // whether statusLine has a string command at all
	block [2]int // byte span of the statusLine value; zero when absent
}

// command finds the statusLine command in raw. Only the top-level
// statusLine object counts: a "command" in a hook, a nested object that
// happens to be called statusLine, or the word inside some other string
// must never be mistaken for it, because the installer overwrites whatever
// span this returns. A settings file that is not a valid JSON object is an
// error, not something to patch around.
func command(raw []byte) (location, error) {
	var loc location
	if len(bytes.TrimSpace(raw)) == 0 {
		return loc, nil
	}
	start, end, err := member(raw, "statusLine")
	if err != nil || end == 0 {
		return loc, err
	}
	loc.block = [2]int{start, end}
	block := raw[start:end]
	if bytes.TrimSpace(block)[0] != '{' {
		return loc, nil
	}
	cs, ce, err := member(block, "command")
	if err != nil || ce == 0 || block[cs] != '"' {
		return loc, err
	}
	if err := json.Unmarshal(block[cs:ce], &loc.value); err != nil {
		return loc, err
	}
	loc.span = [2]int{start + cs, start + ce}
	loc.ok = true
	return loc, nil
}

// member returns the byte span of the value of the named key in the JSON
// object raw, looking only at that object's own keys. The end is zero when
// the key is absent. The last occurrence wins, as it does for Claude Code's
// own JSON parser.
func member(raw []byte, name string) (start, end int, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil {
		return 0, 0, err
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, 0, errors.New("settings file is not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, 0, err
		}
		if tok == name {
			end = int(dec.InputOffset())
			start = end - len(v)
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return 0, 0, err
	}
	return start, end, nil
}

// replace puts cmd into raw. Where there is a command it overwrites that
// string in place. Where statusLine exists without one, it replaces the
// statusLine value, since a second statusLine key would be ambiguous.
// Otherwise it inserts a whole statusLine block after the opening brace,
// so a settings file that never had a statusline gains one.
func replace(raw []byte, cmd string, loc location) ([]byte, error) {
	encoded, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	splice := func(span [2]int, with []byte) []byte {
		out := make([]byte, 0, len(raw)+len(with))
		out = append(out, raw[:span[0]]...)
		out = append(out, with...)
		return append(out, raw[span[1]:]...)
	}
	if loc.ok {
		return splice(loc.span, encoded), nil
	}
	if loc.block[1] > 0 {
		return splice(loc.block, []byte(fmt.Sprintf(`{"type": "command", "command": %s}`, encoded))), nil
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
