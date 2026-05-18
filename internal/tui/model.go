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

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/learn"
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
	cfgPath  string              // path of the config file to write exclude entries to
	hidden   map[string]struct{} // session-only; [h] adds, [u] clears; never written to disk
	excluded map[string]struct{} // session mirror of [x] disk writes; [u] never touches this
	cursor   int
	expanded       bool
	hScroll        int
	width          int
	height         int
	quitting       bool
	ekgOffset      int
	statusMsg      string

	// Learning-module fields (optional; nil/empty when module is disabled).
	overlay     []learn.LearnedRule // cached from overlayPath on each tick
	overlayPath string              // path to errorprobe.learned.yaml
	applier     *learn.Applier      // nil when ep watch is run without ep up
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	failStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	inferredStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan for learned rules
	borderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	detailStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // steel-blue; expanded panel text
	selectedBg     = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	statusErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// NewModel creates a TUI model. The model polls snapshotPath and watchSetPath
// every second for live updates. grafanaBaseURL is used to build Explore deep
// links when the user presses [g]. cfgPath is the config file to which [x]
// exclude entries are written; pass "" to disable that feature.
func NewModel(snapshotPath, watchSetPath string, snap health.HealthSnapshot, ws discovery.WatchSet, grafanaBaseURL, cfgPath string) Model {
	return Model{
		snap:           snap,
		ws:             ws,
		snapshotPath:   snapshotPath,
		watchSetPath:   watchSetPath,
		grafanaBaseURL: grafanaBaseURL,
		cfgPath:  cfgPath,
		hidden:   make(map[string]struct{}),
		excluded: make(map[string]struct{}),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		tea.Tick(ekgInterval, func(time.Time) tea.Msg { return ekgMsg{} }),
	)
}

// WithApplier attaches the learning-module applier to the model so the [v] and
// [f] keys work. overlayPath is the overlay file to read on each tick for the
// ⚑ ? indicator. Call this after NewModel before passing the model to Bubbletea.
func (m Model) WithApplier(applier *learn.Applier, overlayPath string) Model {
	m.applier = applier
	m.overlayPath = overlayPath
	return m
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
				m.hScroll = 0
			}
		case "down", "j":
			rows := m.sortedNames()
			if m.cursor < len(rows)-1 {
				m.cursor++
				m.hScroll = 0
			}
		case "left":
			if m.expanded {
				m.hScroll -= 10
				if m.hScroll < 0 {
					m.hScroll = 0
				}
			}
		case "right":
			if m.expanded {
				m.hScroll += 10
			}
		case "e":
			m.expanded = !m.expanded
			m.hScroll = 0
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
		case "w":
			if m.grafanaBaseURL != "" {
				url := m.grafanaBaseURL + "/d/errorprobe-watch"
				if err := openBrowser(url); err != nil {
					m.statusMsg = fmt.Sprintf("could not open browser: %v", err)
				} else {
					m.statusMsg = ""
				}
			}
		case "h":
			rows := m.sortedNames()
			if m.cursor < len(rows) {
				name := rows[m.cursor]
				m.hidden[name] = struct{}{}
				m.statusMsg = fmt.Sprintf("hidden %q — [u] to unhide all", healthKeyDisplay(name))
				if m.cursor >= len(rows)-1 && m.cursor > 0 {
					m.cursor--
				}
				m.expanded = false
			}
		case "u":
			if len(m.hidden) > 0 {
				m.hidden = make(map[string]struct{})
				m.statusMsg = "all hidden containers restored"
			}
		case "x":
			rows := m.sortedNames()
			if m.cursor < len(rows) && m.cfgPath != "" {
				name := rows[m.cursor]
				pattern := healthKeyDisplay(name)
				if err := config.AppendExclude(m.cfgPath, pattern); err != nil {
					m.statusMsg = fmt.Sprintf("exclude: %v", err)
				} else {
					m.excluded[name] = struct{}{}
					m.statusMsg = fmt.Sprintf("excluded %q — will be skipped on next ep up", pattern)
					if m.cursor >= len(rows)-1 && m.cursor > 0 {
						m.cursor--
					}
					m.expanded = false
				}
			}
		case "v":
			// Validate: confirm the learned rule matched to the selected container.
			rows := m.sortedNames()
			if m.cursor < len(rows) && m.applier != nil {
				name := rows[m.cursor]
				ch := m.snap.Containers[name]
				if ch.MatchedRule != "" && m.isLearnedRule(ch.MatchedRule) {
					if err := m.applier.ConfirmRule(ch.MatchedRule); err != nil {
						m.statusMsg = fmt.Sprintf("confirm: %v", err)
					} else {
						m.statusMsg = fmt.Sprintf("rule %q confirmed", ch.MatchedRule)
					}
				} else {
					m.statusMsg = "no pending learned rule for this container"
				}
			}
		case "f":
			// False-positive: reject the learned rule and suppress its pattern.
			rows := m.sortedNames()
			if m.cursor < len(rows) && m.applier != nil {
				name := rows[m.cursor]
				ch := m.snap.Containers[name]
				if ch.MatchedRule != "" && m.isLearnedRule(ch.MatchedRule) {
					pattern := m.learnedPattern(ch.MatchedRule)
					if err := m.applier.RejectRule(ch.MatchedRule, pattern); err != nil {
						m.statusMsg = fmt.Sprintf("reject: %v", err)
					} else {
						m.statusMsg = fmt.Sprintf("rule %q rejected and pattern suppressed", ch.MatchedRule)
					}
				} else {
					m.statusMsg = "no pending learned rule for this container"
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
		if m.overlayPath != "" {
			if overlay, err := learn.LoadOverlay(m.overlayPath); err == nil {
				m.overlay = overlay
			}
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
	// Keys are health-snapshot keys (ContainerMeta.HealthKey()), not bare display names.
	infraState := make(map[string]string, len(m.ws.Containers))
	containerRuntime := make(map[string]string, len(m.ws.Containers))
	containerSubtitle := make(map[string]string, len(m.ws.Containers))
	displayNameByKey := make(map[string]string, len(m.ws.Containers))
	restartCount := make(map[string]int, len(m.ws.Containers))
	prevExitMsg := make(map[string]string, len(m.ws.Containers))
	containerPod := make(map[string]string, len(m.ws.Containers))
	containerNamespace := make(map[string]string, len(m.ws.Containers))
	containerNode := make(map[string]string, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		key := c.HealthKey()
		infraState[key] = c.InfraStatus
		containerRuntime[key] = c.Runtime
		restartCount[key] = c.RestartCount
		containerPod[key] = c.Pod
		containerNamespace[key] = c.Namespace
		containerNode[key] = c.Node
		if c.DisplayName != "" {
			displayNameByKey[key] = c.DisplayName
		}
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
	} else {
		// Cyan when at least one container is matched by a learned (not yet confirmed) rule.
		for _, ch := range m.snap.Containers {
			if ch.MatchedRule != "" && m.isLearnedRule(ch.MatchedRule) {
				ekgColor = lipgloss.Color("14") // bright cyan
				break
			}
		}
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

	// innerW is the total visible width between the outer │ borders, shared by all detail lines.
	innerW := col1W + 2 + col2W + 2 + col3W + 2 + col4W + 2

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
				lastErr = ch.LastErrorAt.Format("15:04") + " " + humanMsg(ch.LastErrorMsg)
			} else {
				lastErr = "—"
			}
		case health.StateFailing:
			funcText = fmt.Sprintf("✗ FAILING %d", ch.ErrorCount)
			funcStyled = failStyle.Render(funcText)
			if ch.DominantFingerprintCount > 0 {
				lastErr = fmt.Sprintf("same pattern %d×", ch.DominantFingerprintCount)
			} else if ch.LastErrorAt != nil {
				lastErr = ch.LastErrorAt.Format("15:04") + " " + humanMsg(ch.LastErrorMsg)
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
				if ch.MatchedRule != "" && m.isLearnedRule(ch.MatchedRule) {
					funcText = "✓ OK ⚑ ?"
					funcStyled = inferredStyle.Render(funcText)
				} else {
					funcText = "✓ OK"
					funcStyled = okStyle.Render(funcText)
				}
				lastErr = "—"
			}
		}

		cell := func(s string, w int) string { return " " + truncPad(s, w) + " " }
		dispName := displayNameByKey[name]
		if dispName == "" {
			dispName = healthKeyDisplay(name)
		}
		c1 := cell(dispName, col1W)
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
				if ch.MatchedRule != "" && m.isLearnedRule(ch.MatchedRule) {
					selFuncSty = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Background(lipgloss.Color(selBg))
				} else {
					selFuncSty = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Background(lipgloss.Color(selBg))
				}
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

		// Expanded view: multi-line panel with error summary, identity, and Troubleshoot commands.
		if i == m.cursor && m.expanded {
			rt := containerRuntime[name]
			if rt == "" {
				rt = "docker"
			}
			for _, dl := range m.buildExpandedLines(
				name, ch, infra, innerW, rt,
				containerPod[name], containerNamespace[name], containerNode[name],
				prevExitMsg[name], restartCount[name],
			) {
				contentRows = append(contentRows, dl)
			}
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

// buildExpandedLines returns the rows for the multi-line expanded detail panel.
// Each returned string is a fully bordered row (│...│) ready to append to contentRows.
//
// Layout:
//
//	│ ⚠ last error (N total): <message>           [h-scrollable] │
//	│  pod: <pod>  ns: <ns>  node: <node>                        │  (K8s only)
//	│                                                             │
//	│  Troubleshoot:                                              │
//	│    get logs:     <command>                                  │
//	│    describe pod: <command>                                  │  (K8s)
//	│    errors only:  <command>                                  │
//	│    exec shell:   <command>                                  │
func (m Model) buildExpandedLines(
	name string,
	ch health.ContainerHealth,
	infra string,
	innerW int,
	runtime string,
	pod, namespace, node string,
	prevExit string,
	restarts int,
) []string {
	// padLine pads or truncates plain-text content to exactly innerW visible characters.
	padLine := func(s string) string {
		runes := []rune(s)
		if len(runes) > innerW {
			return string(runes[:innerW-1]) + "…"
		}
		return s + strings.Repeat(" ", innerW-len(runes))
	}
	row := func(content string) string {
		return borderStyle.Render("│") + detailStyle.Render(padLine(content)) + borderStyle.Render("│")
	}

	var lines []string

	// --- Line 1: error summary (h-scrollable) ---
	var summary string
	switch ch.State {
	case health.StateHasErrors:
		if ch.LastErrorMsg != "" {
			summary = fmt.Sprintf("⚠  last error (%d total): %s", ch.ErrorCount, ch.LastErrorMsg)
		} else {
			summary = fmt.Sprintf("⚠  has errors (%d total, no message)", ch.ErrorCount)
		}
		if prevExit != "" {
			summary += "  │  prev exit: " + prevExit
		}
	case health.StateFailing:
		if ch.DominantFingerprintCount > 0 {
			summary = fmt.Sprintf("✗  same pattern %d×: %s", ch.DominantFingerprintCount, ch.LastErrorMsg)
		} else if ch.LastErrorMsg != "" {
			summary = fmt.Sprintf("✗  last error (%d total): %s", ch.ErrorCount, ch.LastErrorMsg)
		} else {
			summary = "✗  failing (no message)"
		}
		if prevExit != "" {
			summary += "  │  prev exit: " + prevExit
		}
	default:
		switch infra {
		case "restarting":
			if ch.ErrorCount > 0 && ch.LastErrorMsg != "" {
				summary = fmt.Sprintf("⚠  current (%d errors): %s", ch.ErrorCount, ch.LastErrorMsg)
				if prevExit != "" {
					summary += "  │  prev exit: " + prevExit
				}
			} else if prevExit != "" {
				summary = fmt.Sprintf("⚠  container restarting  restart count: %d  prev exit: %s", restarts, prevExit)
			} else {
				summary = fmt.Sprintf("⚠  container restarting  restart count: %d  (no prev exit log)", restarts)
			}
		case "failed", "error", "crashed", "terminating":
			summary = fmt.Sprintf("✗  infra status: %s  restart count: %d", infra, restarts)
		default:
			summary = fmt.Sprintf("✓  no errors recorded  infra: %s", infra)
		}
	}
	// Render summary with horizontal scroll (◀ ▶ when content overflows).
	{
		summaryRunes := []rune(summary)
		totalLen := len(summaryRunes)
		hOff := m.hScroll
		if hOff > totalLen {
			hOff = totalLen
		}
		viewW := innerW - 1 // -1 for leading space
		hasMore := totalLen > viewW
		if hasMore {
			viewW -= 2 // room for ◀ ▶
		}
		if viewW < 1 {
			viewW = 1
		}
		end := hOff + viewW
		if end > totalLen {
			end = totalLen
		}
		visible := string(summaryRunes[hOff:end])
		pad := viewW - lipgloss.Width(visible)
		if pad < 0 {
			pad = 0
		}
		var summaryLine string
		if !hasMore {
			summaryLine = " " + visible + strings.Repeat(" ", pad)
		} else {
			leftArr, rightArr := " ", " "
			if hOff > 0 {
				leftArr = "◀"
			}
			if end < totalLen {
				rightArr = "▶"
			}
			summaryLine = " " + leftArr + visible + strings.Repeat(" ", pad) + rightArr
		}
		lines = append(lines, borderStyle.Render("│")+detailStyle.Render(summaryLine)+borderStyle.Render("│"))
	}

	// --- Identity line (K8s only) ---
	if runtime == "k8s" && (pod != "" || namespace != "") {
		identity := "  pod: " + pod
		if namespace != "" {
			identity += "  ns: " + namespace
		}
		if node != "" {
			identity += "  node: " + node
		}
		lines = append(lines, row(identity))
	}

	// --- Blank separator ---
	lines = append(lines, row(""))

	// --- Troubleshoot header ---
	lines = append(lines, row("  Troubleshoot:"))

	// --- Commands ---
	const labelW = 14
	lbl := func(s string) string {
		if len(s) < labelW {
			return s + strings.Repeat(" ", labelW-len(s))
		}
		return s
	}
	containerDisplay := healthKeyDisplay(name)
	if runtime == "k8s" && pod != "" {
		nsFlag := ""
		if namespace != "" {
			nsFlag = " -n " + namespace
		}
		cmds := [][2]string{
			{"get logs:", fmt.Sprintf("kubectl logs %s%s --tail=100 --since=10m", pod, nsFlag)},
			{"describe pod:", fmt.Sprintf("kubectl describe pod %s%s", pod, nsFlag)},
			{"errors only:", fmt.Sprintf("ep logs %s --errors-only", containerDisplay)},
			{"exec shell:", fmt.Sprintf("kubectl exec -it %s%s -- /bin/sh", pod, nsFlag)},
		}
		for _, c := range cmds {
			lines = append(lines, row("    "+lbl(c[0])+" "+c[1]))
		}
	} else {
		cmds := [][2]string{
			{"get logs:", fmt.Sprintf("docker logs %s --tail=100 --since=10m", containerDisplay)},
			{"inspect:", fmt.Sprintf("docker inspect %s", containerDisplay)},
			{"errors only:", fmt.Sprintf("ep logs %s --errors-only", containerDisplay)},
			{"exec shell:", fmt.Sprintf("docker exec -it %s /bin/sh", containerDisplay)},
		}
		for _, c := range cmds {
			lines = append(lines, row("    "+lbl(c[0])+" "+c[1]))
		}
	}

	return lines
}

// renderHeader returns 1 or 2 lines depending on terminal width.
// The hint keys are split across two lines when the terminal is too narrow
// to fit them all on one line beside the title.
func (m Model) renderHeader(n int) []string {
	title := headerStyle.Render(fmt.Sprintf(" ErrorProbe  watching %d containers", n))
	titleW := lipgloss.Width(title)

	hintsAll := "[↑↓] navigate  [e] expand  [←→] scroll  [r] reset  [h] hide  [u] unhide  [x] exclude  [v] confirm rule  [f] false-positive  [g] grafana  [o] overview  [w] watch  [q] quit"
	hintsA := "[↑↓] navigate  [e] expand  [←→] scroll  [r] reset  [h] hide  [u] unhide  [x] exclude  [v] confirm  [f] false-positive"
	hintsB := "[g] grafana explore  [o] overview  [w] watch  [q] quit"

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

// sortedNames returns a deterministically ordered list of container names
// from the current watch set, sorted by runtime then display name.
// Keys are ContainerMeta.HealthKey() values ("namespace/container" for K8s,
// bare name for Docker) — the same keys used in the health snapshot map.
//
// Only the watch set is used as the source of truth for which containers to
// display.  Health snapshot entries for containers that have since been removed
// from the watch set (e.g. via an exclude rule) are intentionally NOT shown —
// they linger in health.json for history but should not appear in the TUI.
func (m Model) sortedNames() []string {
	names := make([]string, 0, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		key := c.HealthKey()
		if _, ok := m.hidden[key]; ok {
			continue
		}
		if _, ok := m.excluded[key]; ok {
			continue
		}
		names = append(names, key)
	}
	// Build runtime and display-name lookups.
	rtByKey := make(map[string]string, len(m.ws.Containers))
	dispByKey := make(map[string]string, len(m.ws.Containers))
	for _, c := range m.ws.Containers {
		key := c.HealthKey()
		rtByKey[key] = c.Runtime
		if c.DisplayName != "" {
			dispByKey[key] = c.DisplayName
		} else {
			dispByKey[key] = healthKeyDisplay(key)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		ri := rtByKey[names[i]]
		rj := rtByKey[names[j]]
		if ri != rj {
			return ri < rj
		}
		return dispByKey[names[i]] < dispByKey[names[j]]
	})
	return names
}

// isLearnedRule reports whether ruleName corresponds to a learned (not yet
// confirmed) rule in the cached overlay.
func (m Model) isLearnedRule(ruleName string) bool {
	for _, r := range m.overlay {
		if r.Name == ruleName && r.Source == learn.SourceLearned {
			return true
		}
	}
	return false
}

// learnedPattern returns the raw pattern string for a learned rule by name.
// Returns an empty string when the rule is not found.
func (m Model) learnedPattern(ruleName string) string {
	for _, r := range m.overlay {
		if r.Name == ruleName {
			if p, ok := r.When["message"]; ok {
				return p
			}
		}
	}
	return ""
}

// healthKeyDisplay returns the human-readable container name portion of a health key.
// For K8s keys ("namespace/container") this is the container part; for Docker keys
// (bare name) this is the whole string.  Suffix stripping is handled upstream by
// ContainerMeta.DisplayName (computed in ApplyPolicy); this is the fallback for
// stale snapshot entries that have no corresponding ContainerMeta.
func healthKeyDisplay(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	return key
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

// humanMsg extracts the most readable part of a log message for inline display.
// It tries each extraction strategy in order:
//  1. logfmt:      msg="..." or message="..."
//  2. JSON:        {"error":"..."} / {"message":"..."} / {"reason":"..."} etc.
//  3. Erlang/OTP:  {reason_atom,[stacktrace]} with optional <<"name">> binaries
//  4. Java/Python: ExceptionClassName: message  (dotted/underscored identifier then ": ")
//  5. fallback:    first 200 runes of the raw message
func humanMsg(raw string) string {
	// 1. logfmt
	for _, prefix := range []string{`msg="`, `message="`} {
		if idx := strings.Index(raw, prefix); idx >= 0 {
			rest := raw[idx+len(prefix):]
			if end := strings.Index(rest, `"`); end >= 0 && end > 0 {
				return rest[:end]
			}
		}
	}
	// 2–4: structured detail
	if detail := extractStructuredDetail(raw); detail != "" {
		return detail
	}
	// 5. fallback
	r := []rune(strings.TrimSpace(raw))
	if len(r) > 200 {
		return string(r[:199]) + "…"
	}
	return string(r)
}

// extractStructuredDetail extracts the most diagnostic part from a log line
// that carries structured error data. It is runtime-agnostic and handles:
//
//   - JSON objects: first of "error", "message", "reason", "msg", "err", "cause" fields
//     (covers Node.js, Java structured logging, Go slog JSON, Python structlog, etc.)
//   - Erlang/OTP tuples: {reason_atom,[stacktrace]} + <<"binary">> resource names
//     (covers CouchDB, RabbitMQ, any BEAM/OTP service)
//   - Java/Python/Go exception lines: "pkg.ExceptionName: detail message"
//     (covers JVM stack traces, Python tracebacks, Go error wrapping)
//
// Returns "" when no structured pattern is recognised so the caller can fall back.
func extractStructuredDetail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// JSON object — scan for common error-bearing field names
	if strings.HasPrefix(s, "{") && strings.Contains(s, `"`) {
		for _, field := range []string{"error", "message", "reason", "msg", "err", "cause"} {
			for _, kv := range []string{`"` + field + `":"`, `"` + field + `": "`} {
				if idx := strings.Index(strings.ToLower(s), kv); idx >= 0 {
					rest := s[idx+len(kv):]
					if end := strings.IndexByte(rest, '"'); end > 0 {
						return rest[:end]
					}
				}
			}
		}
	}
	// Erlang/OTP tuple: {atom_reason,[...]} with optional <<"binary">> resource names
	if strings.HasPrefix(s, "{") {
		if v := extractErlangReason(s); v != "" {
			return v
		}
	}
	// Java/Python/Go: "some.ExceptionName: detail" — identifier chars (letters, digits,
	// dots, underscores) before ": " with no spaces in the prefix.
	if idx := strings.Index(s, ": "); idx > 0 && idx < 80 {
		if looksLikeTypeName(s[:idx]) {
			r := []rune(s)
			if len(r) > 120 {
				return string(r[:119]) + "…"
			}
			return s
		}
	}
	return ""
}

// extractErlangReason extracts the error reason atom and up to two <<"name">> binary
// values from an Erlang error tuple such as:
//
//	{database_does_not_exist,[{mem3_shards,...,[<<"_users">>],...}]}
//
// Returns "" if the input is not a recognisable Erlang error tuple.
func extractErlangReason(s string) string {
	inner := s[1:] // skip leading {
	end := strings.IndexAny(inner, ",}")
	if end < 0 {
		return ""
	}
	atom := strings.TrimSpace(inner[:end])
	if !isErlangAtom(atom) {
		return ""
	}
	var binaries []string
	rest := s
	for len(binaries) < 2 {
		bStart := strings.Index(rest, `<<"`)
		if bStart < 0 {
			break
		}
		bEnd := strings.Index(rest[bStart+3:], `">>`)
		if bEnd < 0 {
			break
		}
		val := rest[bStart+3 : bStart+3+bEnd]
		if val != "" {
			binaries = append(binaries, val)
		}
		rest = rest[bStart+3+bEnd+3:]
	}
	if len(binaries) == 0 {
		return atom
	}
	return atom + " (" + strings.Join(binaries, ", ") + ")"
}

// isErlangAtom reports whether s is a valid Erlang atom: starts with a lowercase
// letter, contains only lowercase letters, digits, and underscores, max 64 chars.
func isErlangAtom(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

// looksLikeTypeName reports whether s could be a Java/Python/Go type, package, or
// error identifier: only letters, digits, dots, and underscores, no spaces.
func looksLikeTypeName(s string) bool {
	if len(s) == 0 || len(s) > 120 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_') {
			return false
		}
	}
	return true
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
