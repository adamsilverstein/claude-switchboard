package target

import (
	"path"
	"strconv"
	"strings"
)

// Host is the terminal application an agent's process descends from.
type Host string

const (
	HostUnknown  Host = ""
	HostIterm    Host = "iterm"
	HostTerminal Host = "terminal"
	HostGhostty  Host = "ghostty"
)

// ProcTree is one snapshot of the process table's parent links, used to
// find which terminal application an agent runs under. The tty alone cannot
// say: a pty carries no record of the app that opened it, but the app is
// always an ancestor of the shell that runs on it.
type ProcTree struct {
	parent map[int]int
	comm   map[int]string
}

// LoadProcTree snapshots the whole process table. One ps call costs about
// as much as one for a single pid, so callers take it once per pass.
func LoadProcTree(r Runner) (*ProcTree, error) {
	out, err := r.Run("ps", "-axo", "pid=,ppid=,comm=")
	if err != nil {
		return nil, err
	}
	return ParseProcTree(out), nil
}

// ParseProcTree parses "ps -axo pid=,ppid=,comm=" output. The command is
// the rest of the line, since an app path can contain spaces
// ("Application Support/iTerm2/iTermServer-3.7.7").
func ParseProcTree(out string) *ProcTree {
	t := &ProcTree{parent: map[int]int{}, comm: map[int]string{}}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		t.parent[pid] = ppid
		t.comm[pid] = strings.Join(fields[2:], " ")
	}
	return t
}

// Host walks pid's ancestors to the first one that is a known terminal
// application. When none is, it returns HostUnknown and the name of the
// outermost ancestor below launchd ("wezterm-gui", "kitty", "tmux"), which
// is what a reason shown to the user should name.
func (t *ProcTree) Host(pid int) (Host, string) {
	name := ""
	for i := 0; i < 64 && pid > 1; i++ {
		comm, ok := t.comm[pid]
		if !ok {
			break
		}
		base := path.Base(comm)
		if h := hostOf(base); h != HostUnknown {
			return h, base
		}
		name = strings.TrimPrefix(base, "-")
		pid = t.parent[pid]
	}
	return HostUnknown, name
}

func hostOf(base string) Host {
	switch {
	case base == "iTerm2" || strings.HasPrefix(base, "iTermServer"):
		return HostIterm
	case base == "Terminal":
		return HostTerminal
	case strings.EqualFold(base, "ghostty"):
		return HostGhostty
	}
	return HostUnknown
}
