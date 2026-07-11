package cmdserver

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/trace"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rusq/thermoprint"
	"github.com/rusq/thermoprint/ippsrv"
)

type physicalSnapshotter interface {
	Snapshot() thermoprint.PrinterSnapshot
}

type tickMsg time.Time
type ctxDoneMsg struct{}
type serverErrMsg struct{ err error }

const (
	dashboardClockFormat = "02-Jan-2006 15:04:05"
	truncationMarker     = ">>"

	minDashboardWidth = 40
	minTopPanelWidth  = 38
	minLogPanelHeight = 8
	minHeaderGap      = 1

	topPanelGap              = "  "
	topPanelGapWidth         = 2
	topPanelWidthReserve     = 4
	logPanelWidthReserve     = 2
	logPanelHeightReserve    = 2
	logHeadingHeight         = 1
	minLogLineWidth          = 20
	logLineWidthReserve      = 6
	dashboardHeightReserve   = 4
	twoColumnWidthAdjustment = topPanelGapWidth
	jobHeadingHeight         = 2
	jobRecordHeight          = 2
	scrollPageSize           = 10
)

type dashboardFocus uint8

const (
	focusNone dashboardFocus = iota
	focusLogs
	focusJobs
)

type dashboardModel struct {
	ctx      context.Context
	server   *ippsrv.Server
	physical physicalSnapshotter
	logs     *logBuffer
	result   *serverResult

	width     int
	height    int
	focus     dashboardFocus
	logOffset int
	jobOffset int
	showHelp  bool

	serverSnap  ippsrv.ServerSnapshot
	printerSnap thermoprint.PrinterSnapshot
	err         error
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("31")).Padding(0, 1)
	panelStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	focusStyle   = panelStyle.BorderForeground(lipgloss.Color("37"))
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("36"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	subtleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	headingStyle = lipgloss.NewStyle().Bold(true)
)

func runDashboard(ctx context.Context, server *ippsrv.Server, physical physicalSnapshotter, logs *logBuffer, result *serverResult) error {
	model := newDashboardModel(ctx, server, physical, logs, result)
	program := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	if m, ok := finalModel.(dashboardModel); ok && m.err != nil {
		return m.err
	}
	return nil
}

func newDashboardModel(ctx context.Context, server *ippsrv.Server, physical physicalSnapshotter, logs *logBuffer, result *serverResult) dashboardModel {
	m := dashboardModel{
		ctx:      ctx,
		server:   server,
		physical: physical,
		logs:     logs,
		result:   result,
		showHelp: true,
	}
	m.refresh(ctx)
	return m
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(tick(), waitContext(m.ctx), waitServer(m.result))
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	ctx, task := trace.NewTask(m.ctx, "dashboard.update")
	defer task.End()

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % 3
		case "?":
			m.showHelp = !m.showHelp
		case "up", "k":
			switch m.focus {
			case focusLogs:
				m.logOffset++
			case focusJobs:
				m.jobOffset = min(m.jobOffset+1, m.maxJobOffset(ctx))
			}
		case "down", "j":
			switch m.focus {
			case focusLogs:
				m.logOffset = max(0, m.logOffset-1)
			case focusJobs:
				m.jobOffset = max(0, m.jobOffset-1)
			}
		case "pgup":
			switch m.focus {
			case focusLogs:
				m.logOffset += scrollPageSize
			case focusJobs:
				m.jobOffset = min(m.jobOffset+scrollPageSize, m.maxJobOffset(ctx))
			}
		case "pgdown":
			switch m.focus {
			case focusLogs:
				m.logOffset = max(0, m.logOffset-scrollPageSize)
			case focusJobs:
				m.jobOffset = max(0, m.jobOffset-scrollPageSize)
			}
		case "end":
			switch m.focus {
			case focusLogs:
				m.logOffset = 0
			case focusJobs:
				m.jobOffset = 0
			}
		}
	case tickMsg:
		m.refresh(ctx)
		return m, tick()
	case ctxDoneMsg:
		return m, tea.Quit
	case serverErrMsg:
		m.err = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m dashboardModel) View() string {
	ctx, task := trace.NewTask(m.ctx, "dashboard.view")
	defer task.End()
	if m.width == 0 {
		return "starting dashboard..."
	}
	contentWidth := max(minDashboardWidth, m.width)
	headerLeft := titleStyle.Render("Thermoprint IPP Server") + " " + subtleStyle.Render("dashboard")
	if m.err != nil {
		headerLeft += " " + errorStyle.Render(m.err.Error())
	}
	headerClock := subtleStyle.Render(time.Now().Format(dashboardClockFormat))
	headerGap := max(minHeaderGap, contentWidth-lipgloss.Width(headerLeft)-lipgloss.Width(headerClock))
	header := headerLeft + strings.Repeat(" ", headerGap) + headerClock

	leftWidth := max(minTopPanelWidth, contentWidth/2-twoColumnWidthAdjustment)
	rightWidth := max(minTopPanelWidth, contentWidth-leftWidth-(topPanelWidthReserve+topPanelGapWidth))
	statePanel := panelStyle.Width(leftWidth).Render(m.renderState(ctx))
	jobsPanelHeight := lipgloss.Height(statePanel)
	jobsPanelStyle := panelStyle
	if m.focus == focusJobs {
		jobsPanelStyle = focusStyle
	}
	jobsContentHeight := jobsPanelHeight - logPanelHeightReserve
	jobsPanel := jobsPanelStyle.Width(rightWidth).Height(jobsContentHeight).Render(m.renderJobs(ctx, jobsContentHeight))
	body := lipgloss.JoinHorizontal(lipgloss.Top, statePanel, topPanelGap, jobsPanel)

	logHeight := max(minLogPanelHeight, m.height-lipgloss.Height(header)-lipgloss.Height(body)-dashboardHeightReserve)
	logPanelStyle := panelStyle
	if m.focus == focusLogs {
		logPanelStyle = focusStyle
	}
	logPanel := logPanelStyle.Width(contentWidth - logPanelWidthReserve).Height(logHeight).Render(m.renderLogs(logHeight - logPanelHeightReserve))

	footer := subtleStyle.Render("? help")
	if m.showHelp {
		footer = subtleStyle.Render("q quit  tab focus logs/jobs  up/down/pgup/pgdown scroll  ? hide help")
	}
	return strings.TrimRight(lipgloss.JoinVertical(lipgloss.Left, header, body, logPanel, footer), "\n")
}

func (m *dashboardModel) refresh(ctx context.Context) {
	rgn := trace.StartRegion(ctx, "refresh")
	defer rgn.End()

	m.serverSnap = m.server.Snapshot()
	if m.physical != nil {
		m.printerSnap = m.physical.Snapshot()
	}
}

func (m dashboardModel) renderState(ctx context.Context) string {
	rgn := trace.StartRegion(ctx, "renderState")
	defer rgn.End()

	ss := m.serverSnap
	ps := m.printerSnap
	lines := []string{
		headingStyle.Render("Server"),
		row("Listen", valueOr(ss.ListenAddr, "starting")),
		row("Base URL", valueOr(ss.BaseURL, "-")),
		row("Uptime", formatDuration(ss.Uptime)),
		row("mDNS", boolText(ss.BonjourEnabled)),
		row("Debug", debugText(ss.Debug, ss.DumpDir)),
		"",
		headingStyle.Render("Printer"),
		row("Connection", connectionText(ps)),
		row("IPP state", firstPrinterState(ss)),
		row("Print FSM", valueOr(ps.State, "Idle")),
		row("Battery", batteryText(ps)),
		row("Power", chargeText(ps)),
		row("Paper/lid", paperText(ps)),
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) renderJobs(ctx context.Context, height int) string {
	rgn := trace.StartRegion(ctx, "renderJobs")
	defer rgn.End()

	if len(m.serverSnap.Jobs) == 0 {
		return strings.Join([]string{headingStyle.Render("Jobs"), subtleStyle.Render("No jobs")}, "\n")
	}
	visible := jobVisibleCount(height)
	maxOffset := max(0, len(m.serverSnap.Jobs)-visible)
	offset := min(m.jobOffset, maxOffset)
	heading := "Jobs"
	if offset > 0 {
		heading = fmt.Sprintf("Jobs (%d newer below)", offset)
	}
	lines := []string{headingStyle.Render(heading), fmt.Sprintf("%-4s %-11s %-18s %-10s", "ID", "State", "Name", "User")}
	end := len(m.serverSnap.Jobs) - offset
	start := max(0, end-visible)
	for _, job := range m.serverSnap.Jobs[start:end] {
		name := truncate(valueOr(job.Name, "-"), 18)
		user := truncate(valueOr(job.Username, "-"), 10)
		lines = append(lines, fmt.Sprintf("%-4d %-11s %-18s %-10s", job.ID, job.State, name, user))
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("     %s  created %s%s",
			valueOr(job.Format, "unknown-format"),
			formatTime(job.Created),
			completedSuffix(job),
		)))
	}
	return strings.Join(lines, "\n")
}

func (m dashboardModel) maxJobOffset(ctx context.Context) int {
	contentWidth := max(minDashboardWidth, m.width)
	leftWidth := max(minTopPanelWidth, contentWidth/2-twoColumnWidthAdjustment)
	statePanel := panelStyle.Width(leftWidth).Render(m.renderState(ctx))
	visible := jobVisibleCount(lipgloss.Height(statePanel) - logPanelHeightReserve)
	return max(0, len(m.serverSnap.Jobs)-visible)
}

func jobVisibleCount(height int) int {
	return max(0, (height-jobHeadingHeight)/jobRecordHeight)
}

func (m dashboardModel) renderLogs(height int) string {
	lines := []string{headingStyle.Render("Logs")}
	entries := m.logs.Entries()
	if len(entries) == 0 {
		lines = append(lines, subtleStyle.Render("No log records"))
		return strings.Join(lines, "\n")
	}
	visible := max(1, height-logHeadingHeight)
	maxOffset := max(0, len(entries)-visible)
	offset := min(m.logOffset, maxOffset)
	end := len(entries) - offset
	start := max(0, end-visible)
	for _, entry := range entries[start:end] {
		lines = append(lines, renderLogLine(entry, max(minLogLineWidth, m.width-logLineWidthReserve)))
	}
	if offset > 0 {
		lines = append(lines, subtleStyle.Render(fmt.Sprintf("%d newer log entries below", offset)))
	}
	return strings.Join(lines, "\n")
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func waitContext(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		<-ctx.Done()
		return ctxDoneMsg{}
	}
}

func waitServer(result *serverResult) tea.Cmd {
	return func() tea.Msg {
		err := result.wait()
		if err == nil {
			return ctxDoneMsg{}
		}
		return serverErrMsg{err: err}
	}
}

func row(label, value string) string {
	return labelStyle.Render(fmt.Sprintf("%-12s", label)) + value
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func boolText(v bool) string {
	if v {
		return okStyle.Render("enabled")
	}
	return subtleStyle.Render("disabled")
}

func debugText(enabled bool, dumpDir string) string {
	if !enabled {
		return subtleStyle.Render("disabled")
	}
	if dumpDir == "" {
		return okStyle.Render("enabled")
	}
	return okStyle.Render("enabled") + " " + subtleStyle.Render(dumpDir)
}

func connectionText(ps thermoprint.PrinterSnapshot) string {
	if ps.DryRun {
		return warnStyle.Render("dry-run")
	}
	if ps.Connected {
		return okStyle.Render("connected")
	}
	return errorStyle.Render("disconnected")
}

func firstPrinterState(ss ippsrv.ServerSnapshot) string {
	if len(ss.Printers) == 0 {
		return "-"
	}
	return ss.Printers[0].State.String()
}

func batteryText(ps thermoprint.PrinterSnapshot) string {
	if ps.LastStatusTime.IsZero() {
		return subtleStyle.Render("unknown")
	}
	text := fmt.Sprintf("%d%%", ps.BatteryLevel)
	if ps.BatteryLevel < 10 {
		return errorStyle.Render(text)
	}
	if ps.BatteryLevel < 20 {
		return warnStyle.Render(text)
	}
	return okStyle.Render(text)
}

func chargeText(ps thermoprint.PrinterSnapshot) string {
	switch {
	case ps.Charged:
		return okStyle.Render("charged")
	case ps.Charging:
		return okStyle.Render("charging")
	default:
		return subtleStyle.Render("not charging")
	}
}

func paperText(ps thermoprint.PrinterSnapshot) string {
	if ps.LastStatusTime.IsZero() {
		return subtleStyle.Render("unknown")
	}
	if ps.NoPaper {
		return errorStyle.Render("paper out / lid open")
	}
	return okStyle.Render("ready")
}

func completedSuffix(job ippsrv.JobSnapshot) string {
	if job.Completed.IsZero() {
		return ""
	}
	return "  completed " + formatTime(job.Completed)
}

func styleLog(level slog.Level) lipgloss.Style {
	switch {
	case level >= slog.LevelError:
		return errorStyle
	case level >= slog.LevelWarn:
		return warnStyle
	case level <= slog.LevelDebug:
		return subtleStyle
	default:
		return lipgloss.NewStyle()
	}
}

func renderLogLine(entry logEntry, width int) string {
	prefix := fmt.Sprintf("%s %-5s %s", entry.Time.Format("15:04:05"), entry.Level, entry.Message)
	line := prefix
	if entry.Attrs != "" {
		line += " " + entry.Attrs
	}
	line = truncateDisplay(line, width)
	if entry.Attrs == "" || len(line) <= len(prefix) {
		return styleLog(entry.Level).Render(line)
	}

	prefixPart := line[:len(prefix)]
	attrPart := strings.TrimPrefix(line[len(prefix):], " ")
	return styleLog(entry.Level).Render(prefixPart) + " " + subtleStyle.Render(attrPart)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	return d.String()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + ">>"
}

func truncateDisplay(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= len(truncationMarker) {
		return truncationMarker[:width]
	}

	var b strings.Builder
	limit := width - len(truncationMarker)
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > limit {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteString(truncationMarker)
	return b.String()
}
