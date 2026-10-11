package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/map588/clanktop/internal/model"
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Starting…"
	}
	switch {
	case m.showHelp:
		return m.overlay(m.helpText())
	case m.detail != "":
		return m.overlay(m.detail + "\n\n" + dimStyle.Render("Press any key to close"))
	case m.config != nil:
		return m.overlay(m.configText())
	}

	bodyH := m.height - 1
	leftW := max(30, m.width*38/100)
	rightW := m.width - leftW
	actH := bodyH * 55 / 100
	botH := bodyH - actH
	filesW := rightW * 58 / 100
	procW := rightW - filesW

	r, ok := m.selected()
	var act, files, procs []string
	actTitle := "Activity"
	if ok {
		act = m.activityLines(r, rightW-2)
		files = m.fileLines(r)
		procs = m.procLines(r)
		actTitle += " · " + agentName(r)
	}
	agentsCur := 0
	if rows := m.rows(); len(rows) > 0 {
		agentsCur = m.selectedIndex(rows)
	}
	left := m.panel(panelAgents, "Agents", m.agentLines(), agentsCur, leftW, bodyH)
	right := lipgloss.JoinVertical(lipgloss.Left,
		m.panel(panelActivity, actTitle, act, m.cursor[panelActivity], rightW, actH),
		lipgloss.JoinHorizontal(lipgloss.Top,
			m.panel(panelFiles, "Files touched · open", files, m.cursor[panelFiles], filesW, botH),
			m.panel(panelProcs, "Processes", procs, m.cursor[panelProcs], procW, botH)))
	return lipgloss.JoinVertical(lipgloss.Left, m.viewHeader(), lipgloss.JoinHorizontal(lipgloss.Top, left, right))
}

func agentName(r row) string {
	if r.sub != nil {
		return r.sub.Type + ": " + r.sub.Description
	}
	if r.v.Name != "" {
		return r.v.Name
	}
	return filepath.Base(r.v.Cwd)
}

func (m Model) viewHeader() string {
	busy, subs, procs := 0, 0, 0
	var cpu float64
	var rss uint64
	for _, v := range m.views {
		if v.Status == "busy" {
			busy++
		}
		for _, s := range v.SubAgents {
			if s.Running {
				subs++
			}
		}
		for _, it := range procItems(v.Tree) {
			if it.node.ExitTime == nil {
				procs++
				cpu += it.node.CPUPercent
				rss += it.node.RSS
			}
		}
	}
	text := fmt.Sprintf("clanktop · %s · %d agents (%d busy) · %d subagents running · %d procs · CPU %.0f%% · RSS %s",
		m.clientName, len(m.views), busy, subs, procs, cpu, formatBytes(rss))
	if m.paused {
		text += " · PAUSED"
	}
	text += " · ? help"
	return headerStyle.Width(m.width).Render(ansi.Truncate(text, m.width-2, "…"))
}

// panel draws a bordered box of exactly w×h cells. It scrolls the lines to
// keep the cursor line visible and marks it.
func (m Model) panel(p panel, title string, lines []string, cur, w, h int) string {
	style := panelStyle
	if m.focused == p {
		style = focusedPanelStyle
	}
	innerW, innerH := max(w-2, 1), max(h-3, 1)
	if len(lines) == 0 {
		lines = []string{dimStyle.Render("—")}
		cur = -1
	}
	cur = min(cur, len(lines)-1)
	start := 0
	if cur >= innerH {
		start = cur - innerH + 1
	}
	body := make([]string, 0, innerH)
	for i := start; i < len(lines) && len(body) < innerH; i++ {
		line := ansi.Truncate(lines[i], innerW, "…")
		if i == cur {
			pad := strings.Repeat(" ", max(0, innerW-ansi.StringWidth(line)))
			if m.focused == p {
				line = cursorStyle.Render(ansi.Strip(line) + pad)
			} else {
				line = selectedStyle.Render(ansi.Strip(line) + pad)
			}
		}
		body = append(body, line)
	}
	for len(body) < innerH {
		body = append(body, "")
	}
	head := titleStyle.Render(ansi.Truncate(" "+title+" ", innerW, "…"))
	return style.Width(innerW).Render(head + "\n" + strings.Join(body, "\n"))
}

func (m Model) agentLines() []string {
	rows := m.rows()
	if len(rows) == 0 {
		return []string{dimStyle.Render("Waiting for Claude Code sessions (~/.claude/sessions)…")}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		if r.sub != nil {
			name, state := dimStyle.Render(agentName(r)), dimStyle.Render("done")
			if r.sub.Running {
				name, state = subAgentStyle.Render(agentName(r)), runningStyle.Render("running")
				if c := runningCall(r.sub.Calls); c != nil {
					state = m.callState(c)
				}
			}
			lines[i] = fmt.Sprintf("%s%s %s", dimStyle.Render(r.prefix), name, state)
			continue
		}
		v := r.v
		dot, status := dimStyle.Render("○"), dimStyle.Render(v.Status)
		if v.Status == "busy" {
			dot, status = runningStyle.Render("●"), runningStyle.Render("busy")
		}
		kind := ""
		if v.Kind != "" && v.Kind != "interactive" {
			kind = dimStyle.Render(" " + v.Kind)
		}
		what := ""
		if v.Title != agentName(r) {
			what = dimStyle.Render(v.Title)
		}
		if c := runningCall(v.Calls); c != nil {
			what = m.callState(c)
		}
		alert := ""
		if len(v.Alerts) > 0 {
			alert = alertStyle.Render(" ⚠")
		}
		lines[i] = fmt.Sprintf("%s%s %s %s%s%s %s", dimStyle.Render(r.prefix), dot, orchestratorStyle.Render(agentName(r)), status, kind, alert, what)
	}
	return lines
}

// runningCall is the newest tool call without a result.
func runningCall(calls []model.ToolCall) *model.ToolCall {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].End == nil {
			return &calls[i]
		}
	}
	return nil
}

func (m Model) callState(c *model.ToolCall) string {
	return busyStyle.Render(fmt.Sprintf("▶ %s %s", c.Name, formatElapsed(m.now.Sub(c.Start))))
}

func (m Model) activityLines(r row, width int) []string {
	calls := activityItems(r)
	lines := make([]string, len(calls))
	for i, c := range calls {
		mark, dur := " ", ""
		switch {
		case c.End == nil:
			mark, dur = busyStyle.Render("▶"), busyStyle.Render(formatElapsed(m.now.Sub(c.Start)))
		case c.IsError:
			mark, dur = errStyle.Render("✗"), errStyle.Render(formatElapsed(c.End.Sub(c.Start)))
		default:
			dur = dimStyle.Render(formatElapsed(c.End.Sub(c.Start)))
		}
		summary := c.Summary
		if c.Path != "" {
			summary = strings.Replace(summary, c.Path, shortPath(c.Path, r.v.Cwd, m.home), 1)
		}
		lines[i] = fmt.Sprintf("%s %s %-12s %7s  %s", dimStyle.Render(c.Start.Local().Format("15:04:05")), mark,
			ansi.Truncate(toolName(c.Name), 12, "…"), dur, summary)
	}
	return lines
}

// toolName shortens mcp__server__tool to server:tool.
func toolName(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		server, tool, _ := strings.Cut(rest, "__")
		return server + ":" + tool
	}
	return name
}

func (m Model) fileLines(r row) []string {
	items := fileItems(r)
	lines := make([]string, len(items))
	for i, it := range items {
		switch {
		case it.touch != nil:
			t := it.touch
			var ops string
			for op := model.FileOpRead; op <= model.FileOpSearch; op++ {
				if t.Ops[op] {
					ops += op.Tag()
				} else {
					ops += " "
				}
			}
			style := runningStyle
			if t.Ops[model.FileOpWrite] || t.Ops[model.FileOpEdit] {
				style = errStyle
			}
			lines[i] = fmt.Sprintf("%s %3d× %4s  %s", style.Render("["+ops+"]"), t.Count,
				formatAge(m.now.Sub(t.Last)), shortPath(t.Path, r.v.Cwd, m.home))
		case it.more > 0:
			lines[i] = dimStyle.Render(fmt.Sprintf("     +%d more held open by %s (PID %d)", it.more, it.open.Proc, it.open.PID))
		case it.open != nil:
			lines[i] = fmt.Sprintf("%s %s %s", mcpStyle.Render("open"), dimStyle.Render(it.open.Proc),
				shortPath(it.open.Path, r.v.Cwd, m.home))
		default:
			lines[i] = dimStyle.Render("── held open by processes now ──")
		}
	}
	return lines
}

func (m Model) procLines(r row) []string {
	var lines []string
	for _, a := range r.v.Alerts {
		lines = append(lines, alertStyle.Render("⚠ "+a))
	}
	for _, it := range procItems(r.v.Tree) {
		n := it.node
		name := n.Name
		if n.Role == model.RoleOrchestrator {
			name = "claude"
		}
		stats := dimStyle.Render(fmt.Sprintf(" %d", n.PID))
		if n.ExitTime != nil {
			stats += dimStyle.Render(" exited")
		} else {
			stats += fmt.Sprintf(" %.0f%% %s", n.CPUPercent, formatBytes(n.RSS))
			if n.State == "Z" {
				stats += errStyle.Render(" zombie")
			}
		}
		lines = append(lines, dimStyle.Render(it.prefix)+roleStyle(n).Render(name)+stats)
	}
	return lines
}

func roleStyle(n *model.ProcessInfo) lipgloss.Style {
	if n.ExitTime != nil {
		return dimStyle
	}
	switch n.Role {
	case model.RoleOrchestrator:
		return orchestratorStyle
	case model.RoleToolProcess:
		return toolStyle
	case model.RoleMCPServer:
		return mcpStyle
	}
	return lipgloss.NewStyle()
}

func (m Model) overlay(text string) string {
	w := max(40, m.width-8)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		panelStyle.Padding(1, 2).Width(w).Render(ansi.Hardwrap(text, w-4, true)))
}

// detailText describes the item under the cursor of the focused panel.
func (m *Model) detailText() string {
	r, ok := m.selected()
	if !ok {
		return ""
	}
	i := m.cursor[m.focused]
	switch m.focused {
	case panelAgents:
		if s := r.sub; s != nil {
			return fmt.Sprintf("Subagent\n\nID:          %s\nType:        %s\nDescription: %s\nTool use:    %s\nStarted:     %s\nRunning:     %v\nTool calls:  %d",
				s.ID, s.Type, s.Description, s.ToolUseID, s.Start.Local().Format(time.DateTime), s.Running, len(s.Calls))
		}
		v := r.v
		return fmt.Sprintf("Session\n\nName:        %s\nTitle:       %s\nPID:         %d\nSession ID:  %s\nKind:        %s\nStatus:      %s\nCwd:         %s\nVersion:     %s\nStarted:     %s\nLog:         %s\nTool calls:  %d\nLast prompt: %s\nAlerts:      %s",
			v.Name, v.Title, v.PID, v.SessionID, v.Kind, v.Status, v.Cwd, v.Version,
			v.StartedAt.Local().Format(time.DateTime), v.LogPath, len(v.Calls), v.LastPrompt, strings.Join(v.Alerts, "; "))
	case panelActivity:
		calls := activityItems(r)
		if i >= len(calls) {
			return ""
		}
		c := calls[i]
		end := "running"
		if c.End != nil {
			end = c.End.Local().Format("15:04:05.000") + " (" + c.End.Sub(c.Start).String() + ")"
		}
		return fmt.Sprintf("Tool call\n\nTool:    %s\nID:      %s\nStart:   %s\nEnd:     %s\nError:   %v\nSummary: %s\n\nInput:\n%s",
			c.Name, c.ID, c.Start.Local().Format("15:04:05.000"), end, c.IsError, c.Summary, c.Input)
	case panelFiles:
		items := fileItems(r)
		if i >= len(items) {
			return ""
		}
		if t := items[i].touch; t != nil {
			var ops []string
			for op := model.FileOpRead; op <= model.FileOpSearch; op++ {
				if t.Ops[op] {
					ops = append(ops, op.String())
				}
			}
			return fmt.Sprintf("File touched\n\nPath:       %s\nOperations: %s\nCalls:      %d\nLast:       %s\n\nPress e to open it in $EDITOR.",
				absPath(t.Path, r.v.Cwd), strings.Join(ops, ", "), t.Count, t.Last.Local().Format(time.DateTime))
		}
		if o := items[i].open; o != nil {
			return fmt.Sprintf("Open file\n\nPath:    %s\nProcess: %s (PID %d)", o.Path, o.Proc, o.PID)
		}
	case panelProcs:
		items := procItems(r.v.Tree)
		if i >= len(items) {
			return ""
		}
		n := items[i].node
		status := "running"
		if n.ExitTime != nil {
			status = "exited " + n.ExitTime.Local().Format("15:04:05")
		}
		return fmt.Sprintf("Process\n\nPID:     %d\nPPID:    %d\nName:    %s\nRole:    %s\nCPU:     %.1f%%\nRSS:     %s\nStatus:  %s\nStarted: %s\nCmdline: %s",
			n.PID, n.PPID, n.Name, n.Role, n.CPUPercent, formatBytes(n.RSS), status,
			n.Start.Local().Format(time.DateTime), strings.Join(n.Cmdline, " "))
	}
	return ""
}

func (m Model) configText() string {
	lines := []string{titleStyle.Render("Prompt and config files"), ""}
	if len(m.config) == 0 {
		lines = append(lines, dimStyle.Render("None found."))
	}
	for i, f := range m.config {
		line := fmt.Sprintf("%5s  %s", formatAge(m.now.Sub(f.ModTime)), shortPath(f.Path, "", m.home))
		if i == m.configCur {
			line = cursorStyle.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", dimStyle.Render("↑/↓ select · Enter/e open in $EDITOR · Esc/c close"))
	return strings.Join(lines, "\n")
}

func (m Model) helpText() string {
	pairs := []struct{ key, desc string }{
		{"Tab/Shift+Tab", "Change panel"},
		{"↑/↓ j/k", "Select agent (Agents panel) or line"},
		{"Enter", "Details of the selected line"},
		{"e", "Open the selected file in $EDITOR"},
		{"c", "Prompt and config files of the session"},
		{"p", "Pause or resume"},
		{"q/Ctrl+C", "Quit"},
	}
	lines := []string{titleStyle.Render("Keys"), ""}
	for _, p := range pairs {
		lines = append(lines, helpKeyStyle.Render(fmt.Sprintf("%-14s", p.key))+" "+helpDescStyle.Render(p.desc))
	}
	lines = append(lines, "", helpDescStyle.Render("File ops: R read · W write · E edit · S search (Grep/Glob path)"))
	return strings.Join(lines, "\n")
}

// shortPath writes a path relative to cwd when it is inside cwd, else with ~ for home.
func shortPath(p, cwd, home string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(p) {
			return rel
		}
	}
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func formatElapsed(d time.Duration) string {
	switch {
	case d < 0:
		return "0.0s"
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
