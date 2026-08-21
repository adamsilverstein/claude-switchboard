// Package locate resolves PIDs to their controlling terminal and start time
// using a single batched ps call. Its output feeds both the registry liveness
// check (start times, guarding against PID reuse) and the terminal backends
// (ttys, joining agents to windows).
package locate

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Proc is what the OS reports about one live process.
type Proc struct {
	TTY   string    // "/dev/ttys015", or "" for processes with no terminal
	Start time.Time // process start time, in the local zone ps reports
}

// lstartLayout parses ps -o lstart output like "Fri Aug 21 09:12:12 2026",
// which ps reports in local time.
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// Snapshot returns Proc info for every requested PID that currently exists,
// in one ps invocation. PIDs with no matching process are simply absent from
// the result.
func Snapshot(pids []int) (map[int]Proc, error) {
	if len(pids) == 0 {
		return map[int]Proc{}, nil
	}
	strs := make([]string, len(pids))
	for i, p := range pids {
		strs[i] = strconv.Itoa(p)
	}
	// ps exits non-zero when any requested PID is missing, so ignore the
	// exit status and parse whatever rows came back.
	out, _ := exec.Command("ps", "-o", "pid=,tty=,lstart=", "-p", strings.Join(strs, ",")).Output()
	return ParseSnapshot(string(out)), nil
}

// ParseSnapshot parses ps -o pid=,tty=,lstart= output. Split out from
// Snapshot so it can be tested against captured output.
func ParseSnapshot(out string) map[int]Proc {
	procs := make(map[int]Proc)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		tty := fields[1]
		switch {
		case tty == "??" || tty == "?" || tty == "-":
			tty = ""
		case !strings.HasPrefix(tty, "/dev/"):
			tty = "/dev/" + tty
		}
		start, err := time.ParseInLocation(lstartLayout, strings.Join(fields[2:7], " "), time.Local)
		if err != nil {
			start = time.Time{}
		}
		procs[pid] = Proc{TTY: tty, Start: start}
	}
	return procs
}
