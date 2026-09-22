package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adamsilverstein/claude-switchboard/internal/statusline"
)

// runStatusline is the shim: it copies Claude Code's statusline payload
// somewhere switchboard can read it, then runs whatever statusline command
// you already had, unchanged, with the same payload on its stdin.
//
//	"statusLine": {
//	  "type": "command",
//	  "command": "switchboard statusline -- my-old-statusline"
//	}
//
// The one rule here is that it must never break your statusline. Claude Code
// runs this on every render, and a shim that fails loudly - or at all - would
// replace your prompt with an error message. So every failure on the storing
// side is swallowed and the wrapped command still runs; if there is no
// wrapped command, an empty statusline is the correct output.
func runStatusline(args []string) error {
	// The two flags are the setup side of the same feature: chaining this
	// shim in front of an existing statusline means nesting one command
	// inside another in settings.json, which is the kind of edit people
	// put off. They are checked before anything reads stdin, because
	// neither is being run by Claude Code.
	if len(args) > 0 {
		switch args[0] {
		case "--install":
			return installStatusline()
		case "--uninstall":
			return uninstallStatusline()
		}
	}

	// Accept both "statusline -- cmd args" and "statusline cmd args": the
	// separator reads better in a settings file, but a user who leaves it
	// out should not get a confusing failure.
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		payload = nil
	}
	if len(payload) > 0 {
		if dir, err := statusline.DefaultDir(); err == nil {
			_ = statusline.Store(dir, payload)
		}
	}

	if len(args) == 0 {
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Exit with the wrapped command's own status so chaining the
		// shim in front of it is invisible to Claude Code.
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return fmt.Errorf("statusline: %w", err)
	}
	return nil
}

// installStatusline chains this binary into the user's statusLine setting.
func installStatusline() error {
	path, err := statusline.SettingsPath()
	if err != nil {
		return err
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	res, err := statusline.Install(path, self)
	if err != nil {
		return fmt.Errorf("statusline --install: %w", err)
	}
	if !res.Changed {
		fmt.Printf("Already installed in %s\n  %s\n", res.Path, res.After)
		return nil
	}
	report(res)
	fmt.Println("\nUsage readings appear in the app window within a poll or two of\nyour next Claude Code turn - each session fills in as it renders.")
	return nil
}

// uninstallStatusline puts the statusLine setting back the way it was.
func uninstallStatusline() error {
	path, err := statusline.SettingsPath()
	if err != nil {
		return err
	}
	res, err := statusline.Uninstall(path)
	if err != nil {
		return fmt.Errorf("statusline --uninstall: %w", err)
	}
	if !res.Changed {
		fmt.Printf("Not installed in %s\n", res.Path)
		return nil
	}
	report(res)
	return nil
}

// report prints what was changed, including where the backup went. A tool
// that edits a live settings file owes the reader both halves of that.
func report(res statusline.Result) {
	fmt.Printf("%s\n", res.Path)
	if res.Before == "" {
		fmt.Println("  before  (no statusLine)")
	} else {
		fmt.Printf("  before  %s\n", res.Before)
	}
	if res.After == "" {
		fmt.Println("  after   (no statusLine)")
	} else {
		fmt.Printf("  after   %s\n", res.After)
	}
	if res.Backup != "" {
		fmt.Printf("  backup  %s\n", res.Backup)
	}
}

// selfPath is the absolute path of this binary, which is what has to go
// into settings.json: Claude Code runs the statusline with its own PATH,
// which need not be the one switchboard was launched from.
func selfPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// A symlinked install - a Homebrew shim, a ~/bin link - should record
	// the real binary, so the setting keeps working if the link goes.
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return filepath.Abs(self)
}
