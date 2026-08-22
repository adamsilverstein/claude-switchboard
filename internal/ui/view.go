package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	headerStyle   = lipgloss.NewStyle().Faint(true).Underline(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	deadStyle     = lipgloss.NewStyle().Faint(true)
	footerStyle   = lipgloss.NewStyle().Faint(true)
	noticeStyle   = lipgloss.NewStyle().Bold(true)

	statusStyles = map[string]lipgloss.Style{
		"busy":    lipgloss.NewStyle().Foreground(lipgloss.Color("2")), // green
		"idle":    lipgloss.NewStyle().Foreground(lipgloss.Color("3")), // yellow
		"waiting": lipgloss.NewStyle().Foreground(lipgloss.Color("1")), // red
		"shell":   lipgloss.NewStyle().Foreground(lipgloss.Color("4")), // blue
	}
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder

	title := "switchboard"
	if m.err != nil {
		title += "  " + noticeStyle.Render("error: "+m.err.Error())
	}
	mode := fmt.Sprintf("sort: %s", m.sort)
	if m.typing {
		mode += fmt.Sprintf("  filter: %s▌", m.filter)
	} else if m.filter != "" {
		mode += fmt.Sprintf("  filter: %s", m.filter)
	}
	b.WriteString(titleStyle.Render(title) + "  " + footerStyle.Render(mode) + "\n")

	rows := m.visible()
	nameW, dirW, sumW := m.columnWidths()
	b.WriteString(headerStyle.Render(m.formatRowText("●", "STATUS", "AGE", "NAME", "DIR", "SUMMARY", nameW, dirW, sumW)) + "\n")

	now := time.Now()
	max := m.height - 4 // title, header, notice/footer
	if max < 1 {
		max = 1
	}
	start := 0
	if m.cursor >= max {
		start = m.cursor - max + 1
	}
	for i := start; i < len(rows) && i < start+max; i++ {
		r := rows[i]
		status := r.Agent.Status
		if status == "" {
			status = "?"
		}
		if !r.Agent.Live {
			status = "dead"
		}
		line := m.formatRowText(statusDot(r.Agent.Live, r.Agent.Status), status, FormatAge(now, r.Age),
			r.Agent.Name, ShortDir(r.Agent.Cwd), r.Summary, nameW, dirW, sumW)
		switch {
		case i == m.cursor:
			line = selectedStyle.Render(line)
		case !r.Agent.Live:
			line = deadStyle.Render(line)
		default:
			if st, ok := statusStyles[status]; ok {
				// Color only the dot so the row text stays readable.
				line = st.Render(line[:len("●")]) + line[len("●"):]
			}
		}
		b.WriteString(line + "\n")
	}
	if len(rows) == 0 {
		b.WriteString(deadStyle.Render("  no agents match") + "\n")
	}

	footer := "enter focus  / filter  s/a/n/d sort  ctrl+x stop  q quit"
	if m.notice != "" {
		footer = m.notice
	}
	if m.stopping != nil {
		footer = noticeStyle.Render(fmt.Sprintf("stop %q (SIGTERM pid %d)? y/N", m.stopping.Name, m.stopping.PID))
	}
	b.WriteString(footerStyle.Render(footer))
	return b.String()
}

func statusDot(live bool, status string) string {
	if !live {
		return "○"
	}
	if status == "" {
		return "?"
	}
	return "●"
}

// columnWidths splits the terminal width among the flexible columns.
func (m Model) columnWidths() (nameW, dirW, sumW int) {
	// Fixed: dot(1)+2 + status(7)+2 + age(6)+2 = 20; the rest splits
	// name/dir/summary.
	rest := m.width - 20
	if rest < 30 {
		rest = 30
	}
	nameW = rest * 4 / 10
	dirW = rest * 25 / 100
	sumW = rest - nameW - dirW - 4 // column gaps
	if sumW < 10 {
		sumW = 10
	}
	return nameW, dirW, sumW
}

func (m Model) formatRowText(dot, status, age, name, dir, summary string, nameW, dirW, sumW int) string {
	return fmt.Sprintf("%s  %-7s  %-6s  %s  %s  %s",
		dot, status, age, pad(name, nameW), pad(dir, dirW), pad(summary, sumW))
}

// pad truncates or right-pads s to exactly w display runes.
func pad(s string, w int) string {
	if r := []rune(s); len(r) <= w {
		return s + strings.Repeat(" ", w-len(r))
	}
	return Truncate(s, w)
}

// Truncate caps s at w runes, marking the cut with an ellipsis.
func Truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

// ShortDir abbreviates the user's home directory prefix to ~.
func ShortDir(dir string) string {
	if home, err := userHome(); err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

var (
	homeOnce sync.Once
	homeDir  string
	homeErr  error
)

func userHome() (string, error) {
	homeOnce.Do(func() { homeDir, homeErr = os.UserHomeDir() })
	return homeDir, homeErr
}
