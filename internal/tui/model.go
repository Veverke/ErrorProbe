package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/links"
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

const ekgInterval = 120 * time.Millisecond

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
	snap           health.HealthSnapshot
	ws             discovery.WatchSet
	snapshotPath   string
	watchSetPath   string
	grafanaBaseURL string
	cursor         int
	expanded       bool
	width          int
	height         int
	quitting       bool
	ekgOffset      int
	statusMsg      string
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	failStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	borderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectedBg     = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	statusErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// NewModel creates a TUI model. The model polls snapshotPath and watchSetPath
// every second for live updates. grafanaBaseURL is used to build Explore deep
// links when the user presses [g].
func NewModel(snapshotPath, watchSetPath string, snap health.HealthSnapshot, ws discovery.WatchSet, grafanaBaseURL string) Model {
	return Model{
		snap:           snap,
		ws:             ws,
		snapshotPath:   snapshotPath,
		watchSetPath:   watchSetPath,
		grafanaBaseURL: grafanaBaseURL,
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
				if err := health.SaveSnapshot(m.snapshotPath, m.snap); err != nil {
					m.statusMsg = fmt.Sprintf("error saving snapshot: %v", err)
				} else {
					m.statusMsg = ""
				}
			}
		case "g":
			rows := m.sortedNames()
			if m.cursor < len(rows) && m.grafanaBaseURL != "" {
				name := rows[m.cursor]
				url := links.BuildExploreURL(m.grafanaBaseURL, name, time.Time{}, time.Time{})
				if err := openBrowser(url); err != nil {
					m.statusMsg = fmt.Sprintf("could not open browser: %v", err)
				} else {
					m.statusMsg = ""
				}
			}
		case "o":
			if m.grafanaBaseURL != "" {
				url := m.grafanaBaseURL + "/d/errorprobe-overview"
				if err := openBrowser(url); err != nil {
					m.statusMsg = fmt.Sprintf("could not open browser: %v", err)
				} else {
					m.statusMsg = ""
				}
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

	// Build lookup maps from watch set for infra state and K8s metadata.
	infraState := make(map[string]string, len(m.ws.Containers))
	containerRuntime := make(map[string]string, len(m.ws.Containers))
	containerSubtitle := make(map[string]string, len(m.ws.Containers))
	restartCount := make(map[string]int, len(m.ws.Containers))
	prevExitMsg := make(map[string]string, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		key := normaliseContainerName(c.Name)
		infraState[key] = c.InfraStatus
		containerRuntime[key] = c.Runtime
		restartCount[key] = c.RestartCount
		if c.PrevExitMsg != "" {
			prevExitMsg[key] = c.PrevExitMsg
		}
		if c.Runtime == "k8s" && (c.Pod != "" || c.Namespace != "") {
			containerSubtitle[key] = c.Pod + "  ns=" + c.Namespace
		}
	}

	header := m.renderHeader(n)

	// EKG color reflects overall health (log errors + infra state).
	hasErrors := false
	isFailing := false
	for _, ch := range m.snap.Containers {
		if ch.State == health.StateFailing {
			isFailing = true
		} else if ch.State == health.StateHasErrors {
			hasErrors = true
		}
	}
	for _, st := range infraState {
		switch st {
		case "failed", "error", "crashed", "terminating":
			isFailing = true
		case "restarting", "pending", "waiting":
			if !isFailing {
				hasErrors = true
			}
		}
	}
	ekgColor := lipgloss.Color("10") // bright green
	if isFailing {
		ekgColor = lipgloss.Color("9") // bright red
	} else if hasErrors {
		ekgColor = lipgloss.Color("11") // bright yellow
	}
	ekgSty := lipgloss.NewStyle().Foreground(ekgColor)
	ekgRows := m.renderEKG(m.width)

	const col2W, col3W = 20, 12
	// Distribute remaining terminal width evenly between CONTAINER and LAST ERROR.
	// Table format: │ col1 │ col2 │ col3 │ col4 │  →  5 borders + 8 spaces (1 each side per col)
	termW := m.width
	if termW <= 0 {
		termW = 120
	}
	remain := termW - col2W - col3W - 13 // 5 │ + 8 spaces
	if remain < 44 {
		remain = 44
	}
	col1W := remain / 2
	col4W := remain - col1W

	padRight := func(s string, w int) string {
		vis := lipgloss.Width(s)
		if vis >= w {
			return s
		}
		return s + strings.Repeat(" ", w-vis)
	}
	// truncPad truncates s to w runes (adding … if needed) then right-pads to exactly w.
	truncPad := func(s string, w int) string {
		runes := []rune(s)
		if len(runes) > w {
			s = string(runes[:w-1]) + "…"
		}
		vis := lipgloss.Width(s)
		if vis < w {
			s = s + strings.Repeat(" ", w-vis)
		}
		return s
	}

	bar := func(col string, w int) string { return " " + padRight(col, w) + " " }
	colHeader := borderStyle.Render("│") + bar("[ CONTAINER ]", col1W) +
		borderStyle.Render("│") + bar("[ STATUS ]", col2W) +
		borderStyle.Render("│") + bar("[ INFRA ]", col3W) +
		borderStyle.Render("│") + bar("[ LAST EVENT ]", col4W) +
		borderStyle.Render("│")

	// Fixed top section: always visible regardless of terminal height.
	fixedRows := make([]string, 0, len(header)+8)
	for _, h := range header {
		fixedRows = append(fixedRows, h)
	}
	if m.statusMsg != "" {
		fixedRows = append(fixedRows, statusErrStyle.Render("⚠ "+m.statusMsg))
	}
	for _, row := range ekgRows {
		fixedRows = append(fixedRows, ekgSty.Render(row))
	}
	// sepW = total visible width of the table including borders and padding
	sepW := 1 + (col1W + 2) + 1 + (col2W + 2) + 1 + (col3W + 2) + 1 + (col4W + 2) + 1
	hline := func(left, mid, right, fill string) string {
		return borderStyle.Render(
			left + repeat(fill, col1W+2) +
				mid + repeat(fill, col2W+2) +
				mid + repeat(fill, col3W+2) +
				mid + repeat(fill, col4W+2) +
				right)
	}
	fixedRows = append(fixedRows, hline("┌", "┬", "┐", "─"))
	fixedRows = append(fixedRows, colHeader)
	fixedRows = append(fixedRows, hline("├", "┼", "┤", "─"))

	// Detect whether we have both runtimes for section headers.
	hasDocker, hasK8s := false, false
	for _, name := range names {
		rt := containerRuntime[name]
		if rt == "docker" {
			hasDocker = true
		} else if rt == "k8s" {
			hasK8s = true
		}
	}
	showSectionHeaders := hasDocker && hasK8s
	lastRuntime := ""

	// Scrollable content: container rows clipped to the available height.
	contentRows := make([]string, 0, len(names)*2)
	cursorStartLine := 0

	for i, name := range names {
		rt := containerRuntime[name]
		if rt == "" {
			rt = "docker"
		}

		// Emit runtime section header when both runtimes are present.
		if showSectionHeaders && rt != lastRuntime {
			var prefix string
			if rt == "k8s" {
				prefix = "── kubernetes "
			} else {
				prefix = "── docker "
			}
			// span full table width; sepW includes the outer │ borders
			inner := sepW - 2
			label := "│" + prefix + repeat("─", inner-len([]rune(prefix))) + "│"
			contentRows = append(contentRows, dimStyle.Render(label))
			lastRuntime = rt
		}

		if i == m.cursor {
			cursorStartLine = len(contentRows)
		}

		ch := m.snap.Containers[name]
		infra := infraState[name]
		if infra == "" {
			infra = "unknown"
		}

		var funcText string
		var funcStyled string
		var lastErr string
		switch ch.State {
		case health.StateHasErrors:
			funcText = fmt.Sprintf("⚠ HAS ERRORS %d", ch.ErrorCount)
			funcStyled = errStyle.Render(funcText)
			if ch.LastErrorAt != nil {
				lastErr = ch.LastErrorAt.Format("15:04") + " " + truncateRune(ch.LastErrorMsg, 16)
			} else {
				lastErr = "—"
			}
		case health.StateFailing:
			funcText = fmt.Sprintf("✗ FAILING %d", ch.ErrorCount)
			funcStyled = failStyle.Render(funcText)
			if ch.DominantFingerprintCount > 0 {
				lastErr = fmt.Sprintf("same pattern %d×", ch.DominantFingerprintCount)
			} else if ch.LastErrorAt != nil {
				lastErr = ch.LastErrorAt.Format("15:04") + " " + truncateRune(ch.LastErrorMsg, 16)
			} else {
				lastErr = "—"
			}
		default:
			// Even if no log errors recorded, flag infra-level problems in functional.
			switch infra {
			case "restarting":
				funcText = "⚠ RESTARTING"
				funcStyled = errStyle.Render(funcText)
				if pem := prevExitMsg[name]; pem != "" {
					lastErr = "prev: " + pem
				} else {
					lastErr = "infra restart"
				}
			case "failed", "error", "crashed", "terminating":
				funcText = "⚠ INFRA " + strings.ToUpper(infra)
				funcStyled = failStyle.Render(funcText)
				lastErr = "infra: " + infra
			default:
				funcText = "✓ OK"
				funcStyled = okStyle.Render(funcText)
				lastErr = "—"
			}
		}

		cell := func(s string, w int) string { return " " + truncPad(s, w) + " " }
		c1 := cell(name, col1W)
		c2 := " " + padRight(funcStyled, col2W) + " "
		// Color infra status: green=running, yellow=restarting/pending, red=error/failed/crashed/unknown
		var infraStyled string
		switch infra {
		case "running":
			infraStyled = okStyle.Render(infra)
		case "restarting", "pending", "waiting":
			infraStyled = errStyle.Render(infra)
		case "unknown":
			infraStyled = dimStyle.Render(infra)
		default:
			infraStyled = failStyle.Render(infra) // error, failed, crashed, terminating, etc.
		}
		c3 := " " + padRight(infraStyled, col3W) + " "
		c4 := cell(lastErr, col4W)
		sep := borderStyle.Render("│")
		if i == m.cursor {
			const selBg = "237"
			// Use combined fg+bg styles so inner ANSI resets don't strip the background.
			c1 = selectedBg.Render(c1)
			var selFuncSty lipgloss.Style
			switch ch.State {
			case health.StateHasErrors:
				selFuncSty = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Background(lipgloss.Color(selBg))
			case health.StateFailing:
				selFuncSty = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Background(lipgloss.Color(selBg))
			default:
				selFuncSty = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Background(lipgloss.Color(selBg))
			}
			c2 = selFuncSty.Render(" " + truncPad(funcText, col2W) + " ")
			var selInfraSty lipgloss.Style
			switch infra {
			case "running":
				selInfraSty = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Background(lipgloss.Color(selBg))
			case "restarting", "pending", "waiting":
				selInfraSty = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Background(lipgloss.Color(selBg))
			case "unknown":
				selInfraSty = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color(selBg))
			default:
				selInfraSty = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Background(lipgloss.Color(selBg))
			}
			c3 = selInfraSty.Render(" " + truncPad(infra, col3W) + " ")
			c4 = selectedBg.Render(c4)
			sep = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Background(lipgloss.Color(selBg)).Render("│")
		}
		line := sep + c1 + sep + c2 + sep + c3 + sep + c4 + sep
		contentRows = append(contentRows, line)

		// Expanded view: show full last error message, infra detail, or restart count.
		if i == m.cursor && m.expanded {
			var detail string
			switch ch.State {
			case health.StateHasErrors:
				if ch.LastErrorMsg != "" {
					detail = fmt.Sprintf("⚠  last error (%d total): %s", ch.ErrorCount, ch.LastErrorMsg)
				} else {
					detail = fmt.Sprintf("⚠  has errors (%d total, no message)", ch.ErrorCount)
				}
			case health.StateFailing:
				if ch.DominantFingerprintCount > 0 {
					detail = fmt.Sprintf("✗  same pattern %d×: %s", ch.DominantFingerprintCount, ch.LastErrorMsg)
				} else if ch.LastErrorMsg != "" {
					detail = fmt.Sprintf("✗  last error (%d total): %s", ch.ErrorCount, ch.LastErrorMsg)
				} else {
					detail = "✗  failing (no message)"
				}
			default:
				// Show infra detail even if no log errors.
				rc := restartCount[name]
				switch infra {
				case "restarting":
					pem := prevExitMsg[name]
					if pem != "" {
						detail = fmt.Sprintf("⚠  container restarting  restart count: %d  prev exit: %s", rc, pem)
					} else {
						detail = fmt.Sprintf("⚠  container restarting  restart count: %d  (no prev exit log)", rc)
					}
				case "failed", "error", "crashed", "terminating":
					detail = fmt.Sprintf("✗  infra status: %s  restart count: %d", infra, rc)
				default:
					detail = fmt.Sprintf("✓  no errors recorded  infra: %s", infra)
				}
			}
			// Append K8s pod/namespace subtitle when available.
			if sub := containerSubtitle[name]; sub != "" {
				detail += "  ·  " + sub
			}
			innerW := col1W + 2 + col2W + 2 + col3W + 2 + col4W + 2 // content width between outer │
			detailRunes := []rune(detail)
			if len(detailRunes) > innerW {
				detail = string(detailRunes[:innerW-1]) + "…"
			}
			detailPadded := " " + detail + strings.Repeat(" ", innerW-lipgloss.Width(detail))
			contentRows = append(contentRows, borderStyle.Render("│")+dimStyle.Render(detailPadded)+borderStyle.Render("│"))
		}
	}

	// Clip content rows to available height, scrolling to keep cursor visible.
	// Reserve 1 row for the scroll indicator when content overflows.
	maxContent := m.height - len(fixedRows)
	if maxContent < 1 {
		maxContent = 1
	}
	needScroll := len(contentRows) > maxContent
	availH := maxContent
	if needScroll {
		availH = maxContent - 1
		if availH < 1 {
			availH = 1
		}
	}

	scrollTop := 0
	if len(contentRows) > availH {
		if cursorStartLine >= availH {
			scrollTop = cursorStartLine - availH + 1
		}
	}

	end := scrollTop + availH
	if end > len(contentRows) {
		end = len(contentRows)
	}

	allRows := make([]string, 0, len(fixedRows)+availH+2)
	allRows = append(allRows, fixedRows...)
	allRows = append(allRows, contentRows[scrollTop:end]...)
	if needScroll {
		total := len(contentRows)
		scrollLine := fmt.Sprintf("  ↑↓  %d–%d of %d", scrollTop+1, end, total)
		allRows = append(allRows, hline("├", "┴", "┤", "─")+" "+dimStyle.Render(scrollLine))
	} else {
		allRows = append(allRows, hline("└", "┴", "┘", "─"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, allRows...)
}

// renderHeader returns 1 or 2 lines depending on terminal width.
// The hint keys are split across two lines when the terminal is too narrow
// to fit them all on one line beside the title.
func (m Model) renderHeader(n int) []string {
	title := headerStyle.Render(fmt.Sprintf(" ErrorProbe  watching %d containers", n))
	titleW := lipgloss.Width(title)

	hintsAll := "[↑↓] navigate  [e] expand  [r] reset  [g] grafana explore  [o] overview  [q] quit"
	hintsA := "[↑↓] navigate  [e] expand  [r] reset"
	hintsB := "[g] grafana explore  [o] overview  [q] quit"

	w := m.width
	if w <= 0 {
		w = 80
	}

	// Try single line: title + 2 spaces + full hint.
	if titleW+2+len(hintsAll) <= w {
		return []string{title + "  " + dimStyle.Render(hintsAll)}
	}

	// Two-line fallback.
	return []string{
		title + "  " + dimStyle.Render(hintsA),
		strings.Repeat(" ", titleW+2) + dimStyle.Render(hintsB),
	}
}

// sortedNames returns a deterministically ordered list of all container names
// from both the health snapshot and the watch set, sorted by runtime then name.
// Names in pod/container format (stale watch set data) are normalised to just
// the container part.
func (m Model) sortedNames() []string {
	seen := make(map[string]struct{})
	for n := range m.snap.Containers {
		seen[normaliseContainerName(n)] = struct{}{}
	}
	for _, c := range m.ws.Containers {
		seen[normaliseContainerName(c.Name)] = struct{}{}
	}
	// Build runtime lookup.
	rtByName := make(map[string]string, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		rtByName[normaliseContainerName(c.Name)] = c.Runtime
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		ri := rtByName[names[i]]
		if ri == "" {
			ri = "docker"
		}
		rj := rtByName[names[j]]
		if rj == "" {
			rj = "docker"
		}
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	return names
}

// normaliseContainerName strips the pod/ prefix from stale pod/container format names.
func normaliseContainerName(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
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

// openBrowser opens url in the system default browser.
// On Windows it uses "cmd /c start <url>"; on macOS "open"; on Linux "xdg-open".
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if cmd.Process != nil {
		return cmd.Process.Release()
	}
	return nil
}
