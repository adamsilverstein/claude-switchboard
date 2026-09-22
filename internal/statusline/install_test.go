package statusline_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
)

// commandOf reads the statusLine command back out of a settings file.
func commandOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		StatusLine *struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("settings is not valid JSON after the edit: %v\n%s", err, raw)
	}
	if settings.StatusLine == nil {
		return ""
	}
	return settings.StatusLine.Command
}

// write creates a temporary settings file containing body.
func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInstallWrapsTheExistingCommand verifies that the prior command is kept.
func TestInstallWrapsTheExistingCommand(t *testing.T) {
	path := write(t, `{
  "model": "opus",
  "statusLine": {
    "type": "command",
    "command": "bash /Users/me/.claude/statusline-wrapper.sh"
  }
}
`)
	res, err := statusline.Install(path, "/Users/me/go/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("install reported no change")
	}
	want := "/Users/me/go/bin/switchboard statusline -- bash /Users/me/.claude/statusline-wrapper.sh"
	if got := commandOf(t, path); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// The file is somebody's, and re-serializing it to change one string would
// sort their keys and reflow their spacing.
func TestInstallLeavesTheRestOfTheFileByteForByte(t *testing.T) {
	body := `{
  "zebra": 1,
  "statusLine": { "type": "command", "command": "old" },
  "alpha": { "nested": [1, 2, 3] }
}
`
	path := write(t, body)
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Replace(string(raw), `"/bin/switchboard statusline -- old"`, `"old"`, 1)
	if got != body {
		t.Errorf("the file changed beyond the command:\n%s", got)
	}
}

// TestInstallIsIdempotent verifies that a second install leaves no artifacts.
func TestInstallIsIdempotent(t *testing.T) {
	path := write(t, `{"statusLine": {"type": "command", "command": "my-statusline"}}`)
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	once := commandOf(t, path)

	res, err := statusline.Install(path, "/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("the second install rewrote the file")
	}
	if res.Backup != "" {
		t.Errorf("the second install took a backup: %s", res.Backup)
	}
	if got := commandOf(t, path); got != once {
		t.Errorf("command = %q, want it left at %q", got, once)
	}
}

// A switchboard that has moved since it was installed is still installed.
func TestInstalledMatchesOnShapeNotPath(t *testing.T) {
	for _, cmd := range []string{
		"switchboard statusline",
		"/opt/bin/switchboard statusline -- hud",
		`"/Applications/My Tools/switchboard" statusline -- hud`,
	} {
		if !statusline.Installed(cmd) {
			t.Errorf("Installed(%q) = false", cmd)
		}
	}
	for _, cmd := range []string{
		"",
		"hud",
		"switchboard list",
		"my-switchboard-helper statusline",
	} {
		if statusline.Installed(cmd) {
			t.Errorf("Installed(%q) = true", cmd)
		}
	}
}

// TestInstallIntoAFileWithNoStatusLine verifies insertion beside existing keys.
func TestInstallIntoAFileWithNoStatusLine(t *testing.T) {
	path := write(t, `{"model": "opus"}`)
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	if got, want := commandOf(t, path), "/bin/switchboard statusline"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"model"`) {
		t.Errorf("the existing keys were lost:\n%s", raw)
	}
}

// TestInstallIntoAnEmptyObjectAndAMissingFile covers both blank starting states.
func TestInstallIntoAnEmptyObjectAndAMissingFile(t *testing.T) {
	empty := write(t, "{}\n")
	if _, err := statusline.Install(empty, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	if got, want := commandOf(t, empty), "/bin/switchboard statusline"; got != want {
		t.Errorf("empty object: command = %q, want %q", got, want)
	}

	missing := filepath.Join(t.TempDir(), "nested", "settings.json")
	res, err := statusline.Install(missing, "/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != "" {
		t.Errorf("backed up a file that did not exist: %s", res.Backup)
	}
	if got, want := commandOf(t, missing), "/bin/switchboard statusline"; got != want {
		t.Errorf("missing file: command = %q, want %q", got, want)
	}
}

// TestInstallQuotesAPathWithASpace verifies that the generated command is safe.
func TestInstallQuotesAPathWithASpace(t *testing.T) {
	path := write(t, `{"statusLine": {"type": "command", "command": "hud"}}`)
	if _, err := statusline.Install(path, "/Applications/My Tools/switchboard"); err != nil {
		t.Fatal(err)
	}
	want := `"/Applications/My Tools/switchboard" statusline -- hud`
	if got := commandOf(t, path); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// TestInstallBacksUpWhatItOverwrote verifies the original bytes are preserved.
func TestInstallBacksUpWhatItOverwrote(t *testing.T) {
	body := `{"statusLine": {"type": "command", "command": "hud"}}`
	path := write(t, body)
	res, err := statusline.Install(path, "/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup == "" {
		t.Fatal("no backup taken")
	}
	saved, err := os.ReadFile(res.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != body {
		t.Errorf("backup = %q, want the file as it was", saved)
	}
}

// TestUninstallRestoresTheWrappedCommand verifies the inverse installation.
func TestUninstallRestoresTheWrappedCommand(t *testing.T) {
	path := write(t, `{"statusLine": {"type": "command", "command": "bash hud.sh --wide"}}`)
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	res, err := statusline.Uninstall(path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("uninstall reported no change")
	}
	if got, want := commandOf(t, path), "bash hud.sh --wide"; got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// Installing into a file with no statusline and then uninstalling has to
// leave no statusline, not an empty one that renders as a broken prompt.
func TestUninstallRemovesAStatusLineItCreated(t *testing.T) {
	path := write(t, `{"model": "opus"}`)
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	if _, err := statusline.Uninstall(path); err != nil {
		t.Fatal(err)
	}
	if got := commandOf(t, path); got != "" {
		t.Errorf("command = %q, want the statusLine gone", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"model"`) {
		t.Errorf("the other keys were lost:\n%s", raw)
	}
}

// TestUninstallWithoutTheShimChangesNothing protects unrelated statuslines.
func TestUninstallWithoutTheShimChangesNothing(t *testing.T) {
	body := `{"statusLine": {"type": "command", "command": "hud"}}`
	path := write(t, body)
	res, err := statusline.Uninstall(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("uninstall rewrote a file it had never installed into")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != body {
		t.Errorf("file changed:\n%s", raw)
	}
}

// Uninstall has to be safe to run on a machine that never had a settings
// file, the same way Install is.
func TestUninstallWithNoSettingsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	res, err := statusline.Uninstall(path)
	if err != nil {
		t.Fatalf("uninstall with no settings file: %v", err)
	}
	if res.Changed {
		t.Error("uninstall reported a change with nothing to change")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("uninstall created a settings file")
	}
}

// The command lookup must stay inside the top-level statusLine object. A
// statusLine with no command of its own, followed by a hook that has one,
// used to rewrite the hook.
func TestInstallLeavesOtherCommandsAlone(t *testing.T) {
	cases := map[string]string{
		"hook after a commandless statusLine":      `{"statusLine":{"type":"command"},"hooks":[{"command":"hook"}]}`,
		"the word statusLine in an earlier string": `{"note":"see \"statusLine\" below","hooks":[{"command":"hook"}],"statusLine":{"type":"command","command":"hud"}}`,
		"a nested statusLine key":                  `{"profiles":{"statusLine":{"command":"nested"}},"statusLine":{"type":"command","command":"hud"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := write(t, body)
			if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			for _, keep := range []string{`"command":"hook"`, `"command":"nested"`} {
				if strings.Contains(body, keep) && !strings.Contains(string(raw), keep) {
					t.Errorf("install rewrote %s:\n%s", keep, raw)
				}
			}
			if got := commandOf(t, path); !statusline.Installed(got) {
				t.Errorf("statusLine command = %q, want the shim", got)
			}
			if strings.Count(string(raw), `"statusLine"`) != strings.Count(body, `"statusLine"`) {
				t.Errorf("install added a second statusLine key:\n%s", raw)
			}
		})
	}
}

// A settings file that is not valid JSON must be refused, not patched.
func TestInstallRefusesInvalidJSON(t *testing.T) {
	body := `{"statusLine": {"command": "hud"`
	path := write(t, body)
	if _, err := statusline.Install(path, "/bin/switchboard"); err == nil {
		t.Error("install accepted a settings file that is not valid JSON")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != body {
		t.Errorf("file changed:\n%s", raw)
	}
}

// An install and an uninstall in the same second must not share a backup,
// or the second would overwrite the only copy of the original file.
func TestBackupsDoNotOverwriteEachOther(t *testing.T) {
	body := `{"statusLine": {"type": "command", "command": "hud"}}`
	path := write(t, body)
	in, err := statusline.Install(path, "/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	out, err := statusline.Uninstall(path)
	if err != nil {
		t.Fatal(err)
	}
	if in.Backup == out.Backup {
		t.Fatalf("install and uninstall both backed up to %s", in.Backup)
	}
	if saved, _ := os.ReadFile(in.Backup); string(saved) != body {
		t.Errorf("install's backup = %q, want the original file", saved)
	}
}

// settings.json can hold credentials in its env block, so neither the
// rewritten file nor its backup may be more readable than the original.
func TestInstallKeepsTheFileMode(t *testing.T) {
	path := write(t, `{"statusLine": {"type": "command", "command": "hud"}}`)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := statusline.Install(path, "/bin/switchboard")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, res.Backup} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
		}
	}
}

// A settings file the installer creates starts private.
func TestInstallCreatesAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := statusline.Install(path, "/bin/switchboard"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}
