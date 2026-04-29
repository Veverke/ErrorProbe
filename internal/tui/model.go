package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
)

// refreshMsg triggers a snapshot reload from disk.
type refreshMsg struct {
	snap health.HealthSnapshot
	ws   discovery.WatchSet
}

// tickMsg is sent on each poll tick.
type tickMsg time.Time

// ekgMsg drives the EKG scroll animation.
type ekgMsg struct{}

const ekgInterval = 80 * time.Millisecond

// ekgTile is one full cardiac-cycle tile (40 chars wide, 4 rows).
// Row 0 = top (R-spike tip); Row 3 = bottom (S-wave dip below baseline).
var ekgTile = [4]string{
	`              /\                        `,
	`    /\       /  \          /~~~\        `,
	`---/  \-----/    \        /     \-------`,
	`                  \______/              `,
}

const ekgTileWidth = 40

// Model is the Bubbletea model for the watch TUI.
type Model struct {
	snap         health.HealthSnapshot
	ws           discovery.WatchSet
	snapshotPath string
	watchSetPath string
	cursor       int
	expanded     bool
	width        int
	height       int
	quitting     bool
	ekgOffset    int
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectedBg  = lipgloss.NewStyle().Background(lipgloss.Color("237"))
)

// NewModel creates a TUI model. The model polls snapshotPath and watchSetPath
// every second for live updates.
func NewModel(snapshotPath, watchSetPath string, snap health.HealthSnapshot, ws discovery.WatchSet) Model {
	return Model{
		snap:         snap,
		ws:           ws,
		snapshotPath: snapshotPath,
		watchSetPath: watchSetPath,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		tea.Tick(ekgInterval, func(time.Time) tea.Msg { return ekgMsg{} }),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.expanded = false
			}
		case "down", "j":
			rows := m.sortedNames()
			if m.cursor < len(rows)-1 {
				m.cursor++
				m.expanded = false
			}
		case "e":
			m.expanded = !m.expanded
		case "r":
			rows := m.sortedNames()
			if m.cursor < len(rows) {
				name := rows[m.cursor]
				m.snap.Reset(name)
				_ = health.SaveSnapshot(m.snapshotPath, m.snap)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		snap, err := health.LoadSnapshot(m.snapshotPath)
		if err == nil {
			m.snap = snap
		}
		ws, err := discovery.LoadWatchSet(m.watchSetPath)
		if err == nil {
			m.ws = ws
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case ekgMsg:
		m.ekgOffset = (m.ekgOffset + 2) % ekgTileWidth
		return m, tea.Tick(ekgInterval, func(time.Time) tea.Msg { return ekgMsg{} })

	case refreshMsg:
		m.snap = msg.snap
		m.ws = msg.ws
	}

	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	names := m.sortedNames()
	n := len(names)

	infraState := make(map[string]string, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		infraState[c.Name] = c.InfraStatus
	}

	header := headerStyle.Render(fmt.Sprintf(" ErrorProbe  watching %d containers", n)) +
		"          " + dimStyle.Render("[↑↓] navigate  [e] expand  [r] reset  [q] quit")

	// EKG color reflects overall health: green = all OK, yellow = has errors.
	hasErrors := false
	for _, ch := range m.snap.Containers {
		if ch.State == health.StateHasErrors {
			hasErrors = true
			break
		}
	}
	ekgColor := lipgloss.Color("10") // bright green
	if hasErrors {
		ekgColor = lipgloss.Color("11") // bright yellow
	}
	ekgSty := lipgloss.NewStyle().Foreground(ekgColor)
	ekgRows := m.renderEKG(m.width)

	sep := borderStyle.Render("─")
	colFmt := "%-22s  %-20s  %-12s  %-22s"
	colHeader := fmt.Sprintf(colFmt, "CONTAINER", "FUNCTIONAL", "INFRA", "LAST ERROR")

	rows := make([]string, 0, len(names)+6)
	rows = append(rows, header)
	for _, row := range ekgRows {
		rows = append(rows, ekgSty.Render(row))
	}
	rows = append(rows, borderStyle.Render(repeat("─", 82)))
	rows = append(rows, colHeader)
	rows = append(rows, borderStyle.Render(repeat(sep, 82)))

	for i, name := range names {
		ch := m.snap.Containers[name]
		infra := infraState[name]
		if infra == "" {
			infra = "unknown"
		}

		var funcCell string
		var lastErr string
		switch ch.State {
		case health.StateHasErrors:
			funcCell = errStyle.Render(fmt.Sprintf("⚠ HAS ERRORS %d", ch.ErrorCount))
			if ch.LastErrorAt != nil {
				lastErr = ch.LastErrorAt.Format("15:04") + " " + truncateRune(ch.LastErrorMsg, 16)
			} else {
				lastErr = "—"
			}
		default:
			funcCell = okStyle.Render("✓ OK")
			lastErr = "—"
		}

		line := fmt.Sprintf("%-22s  %-20s  %-12s  %-22s", name, funcCell, infra, lastErr)
		if i == m.cursor {
			line = selectedBg.Render(line)
		}
		rows = append(rows, line)

		// Expanded view: show full last error message
		if i == m.cursor && m.expanded && ch.State == health.StateHasErrors {
			rows = append(rows, dimStyle.Render("  "+ch.LastErrorMsg))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// sortedNames returns a deterministically ordered list of all container names
// from both the health snapshot and the watch set.
func (m Model) sortedNames() []string {
	seen := make(map[string]struct{})
	for n := range m.snap.Containers {
		seen[n] = struct{}{}
	}
	for _, c := range m.ws.Containers {
		seen[c.Name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// renderEKG returns a 4-row EKG frame by slicing a scrolling window over
// the tiled cardiac-cycle pattern. All tile characters are ASCII so byte
// indexing is safe.
func (m Model) renderEKG(width int) [4]string {
	if width <= 0 {
		width = 80
	}
	repeats := (width / ekgTileWidth) + 3
	var rows [4]string
	for r := 0; r < 4; r++ {
		repeated := strings.Repeat(ekgTile[r], repeats)
		end := m.ekgOffset + width
		if end > len(repeated) {
			end = len(repeated)
		}
		rows[r] = repeated[m.ekgOffset:end]
	}
	return rows
}

func truncateRune(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
