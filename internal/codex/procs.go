// Package codex finds the OpenAI Codex CLI sessions running on this machine
// and describes them in the same shape the Claude Code registry does, so the
// picker and the app window can list both side by side.
//
// Codex keeps no registry of live sessions the way Claude Code does under
// ~/.claude/sessions. What it does keep is a rollout transcript per session
// under ~/.codex/sessions, and a process with a controlling terminal. This
// package joins the two: the process says the session is alive and which
// terminal it is in, and the rollout says what it is doing. Like the
// registry reader, everything here is read-only and every field optional,
// because none of it is a documented interface.
package codex

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Proc is one running Codex CLI process.
type Proc struct {
	PID   int
	TTY   string    // "/dev/ttys001"; never empty, a Proc always has one
	Start time.Time // in the local zone ps reports
	Cwd   string    // filled in by the caller from lsof; "" until then
}

// lstartLayout matches ps -o lstart, "Mon Sep 28 14:41:26 2026".
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// findProcs lists every interactive Codex CLI on the machine in one ps call.
func findProcs() []Proc {
	out, err := exec.Command("ps", "-axo", "pid=,tty=,lstart=,args=").Output()
	if err != nil {
		return nil
	}
	return parseProcs(string(out))
}

// parseProcs picks the Codex CLI processes out of ps -axo pid,tty,lstart,args.
//
// Three filters, each for a process that would otherwise double up or
// offer a destination that does not exist:
//
//   - The executable must be named codex. The npm install runs a node
//     wrapper ("node .../bin/codex") that spawns the native binary; only
//     the binary counts, or every session would list twice.
//   - It must have a terminal. The Codex desktop app and its app-server
//     daemon run codex binaries too, headless, and there is no window to
//     take anyone to.
//   - It must not be the app server, which a terminal can launch by hand
//     but which is a backend, not a session.
func parseProcs(out string) []Proc {
	var procs []Proc
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		tty := fields[1]
		if tty == "??" || tty == "?" || tty == "-" {
			continue
		}
		args := fields[7:]
		if filepath.Base(args[0]) != "codex" {
			continue
		}
		if len(args) > 1 && args[1] == "app-server" {
			continue
		}
		if !strings.HasPrefix(tty, "/dev/") {
			tty = "/dev/" + tty
		}
		start, _ := time.ParseInLocation(lstartLayout, strings.Join(fields[2:7], " "), time.Local)
		procs = append(procs, Proc{PID: pid, TTY: tty, Start: start})
	}
	return procs
}

// cwds asks lsof for the working directory of each pid, in one call.
func cwds(pids []int) map[int]string {
	if len(pids) == 0 {
		return nil
	}
	strs := make([]string, len(pids))
	for i, p := range pids {
		strs[i] = strconv.Itoa(p)
	}
	// lsof exits non-zero when any pid has gone; parse what came back.
	out, _ := exec.Command("lsof", "-a", "-d", "cwd", "-p", strings.Join(strs, ","), "-Fpn").Output()
	return parseCwds(string(out))
}

// parseCwds reads lsof -F output: a "p<pid>" line opens each process and an
// "n<path>" line names its file, here always the working directory.
func parseCwds(out string) map[int]string {
	dirs := map[int]string{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid != 0 {
				dirs[pid] = line[1:]
			}
		}
	}
	return dirs
}
