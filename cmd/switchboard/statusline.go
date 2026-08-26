package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"

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
