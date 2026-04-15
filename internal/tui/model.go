package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/map588/clanktop/internal/agent"
	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/debug"
	"github.com/map588/clanktop/internal/filetracker"
	"github.com/map588/clanktop/internal/model"
	"github.com/map588/clanktop/internal/process"
)

type panel int

const (
	panelAgentTree panel = iota
	panelPromptFiles
	panelToolCalls
	panelStats
	panelFileActivity
	panelCount
)

type Model struct {
	eventBus     *bus.EventBus
	detector     *agent.Detector
	tracker      *filetracker.Tracker
	leakDetector *process.LeakDetector
	backend      backend.ClientBackend
	width        int
	height       int
	focusedPanel panel
	paused       bool
	showHelp     bool
	sessionStart time.Time

	// Data
	stableTree *stableNode        // persistent, append-only tree
	allProcs   []*model.ProcessInfo
	agents     []*model.AgentNode
	alerts     []bus.AlertEvent
	newPIDs    map[int32]time.Time
	seenPIDs   map[int32]bool     // all PIDs ever seen

	// Buffered snapshot from scanner — merged on tick
	pendingTree  *model.ProcessInfo
	pendingProcs []*model.ProcessInfo

	// Process lifecycle events
	procHistory *model.RingBuffer[bus.ProcLifecycleEvent]

	// Tool call tracking
	activeTools  map[int32]model.ToolCall
	seenToolPID  map[int32]bool
	toolCalls    *model.RingBuffer[model.ToolCall]
	fileEntries  map[string]*fileEntry // path -> entry
	fileOrder    int                   // monotonic counter for insertion order

	// Shell annotation: maps PID → short command label from JSONL
	shellLabels    map[int32]string
	pendingCmds    []pendingCmd // recent JSONL Bash commands awaiting PID match

	// Stats
	totalProcs   int
	runningCount int
	sleepCount   int
	zombieCount  int
	exitedCount  int
	totalCPU     float64
	totalRSS     uint64
	spawnRate    float64
	spawnHistory []time.Time

	// UI state — per-panel cursor
	collapsed  map[string]bool
	cursor     [panelCount]int // selected index per panel
	detailView string          // non-empty = showing detail overlay
	editFile   string          // non-empty = open file in $EDITOR

	// Prompt/config files discovered at startup
	promptFiles []model.FileEvent

	// Process identification
	rootPID    int32
	clientName string
	projectDir string
	uptime     time.Duration
}

// pendingCmd is a JSONL Bash command awaiting PID match from process polling.
type pendingCmd struct {
	Time  time.Time
	Label string // short summary of command
}

// stableNode is an append-only process tree node.
// Once added, never removed or reordered. Only state (exited, RSS) updates.
type stableNode struct {
	PID      int32
	PPID     int32
	Name     string
	Cmdline  []string
	Role     model.ProcessRole
	RSS      uint64
	Exited   bool
	Label    string // annotated command from JSONL (e.g. "sleep 3")
	Children []*stableNode
}

// fileEntry tracks accumulated operations on a single path
type fileEntry struct {
	Path  string
	Ops   map[string]bool // "R", "W", "E", "Grep", "Glob"
	Last  time.Time
	Order int // insertion order for stable sort
}

// Messages
type processTreeMsg bus.ProcessTreeEvent
type procLifecycleMsg bus.ProcLifecycleEvent
type toolCallMsg model.ToolCall
type alertMsg bus.AlertEvent
type tickMsg time.Time

func NewModel(
	eventBus *bus.EventBus,
	rootPID int32,
	clientName string,
	be backend.ClientBackend,
	detector *agent.Detector,
	tracker *filetracker.Tracker,
	leakDetector *process.LeakDetector,
	projectDir string,
) Model {
	// Scan prompt/config files at startup
	promptFiles := filetracker.ScanPromptFiles(projectDir)

	return Model{
		eventBus:     eventBus,
		detector:     detector,
		tracker:      tracker,
		leakDetector: leakDetector,
		backend:      be,
		sessionStart: time.Now(),
		rootPID:      rootPID,
		clientName:   clientName,
		procHistory: model.NewRingBuffer[bus.ProcLifecycleEvent](1000),
		toolCalls:   model.NewRingBuffer[model.ToolCall](2000),
		fileEntries: make(map[string]*fileEntry),
		activeTools: make(map[int32]model.ToolCall),
		seenToolPID: make(map[int32]bool),
		shellLabels: make(map[int32]string),
		collapsed:    make(map[string]bool),
		newPIDs:      make(map[int32]time.Time),
		seenPIDs:     make(map[int32]bool),
		promptFiles:  promptFiles,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.listenProcessTree(),
		m.listenProcLifecycle(),
		m.listenToolCalls(),
		m.listenAlerts(),
		tickCmd(),
	)
}

func (m Model) listenProcLifecycle() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventBus.ProcLifecycle
		if !ok {
			return nil
		}
		return procLifecycleMsg(ev)
	}
}

func (m Model) listenToolCalls() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventBus.ToolCalls
		if !ok {
			return nil
		}
		return toolCallMsg(ev)
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) listenProcessTree() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventBus.ProcessTree
		if !ok {
			return nil
		}
		return processTreeMsg(ev)
	}
}

func (m Model) listenAlerts() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.eventBus.Alerts
		if !ok {
			return nil
		}
		return alertMsg(ev)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.detailView != "" {
			m.detailView = ""
			return m, nil
		}
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focusedPanel = (m.focusedPanel + 1) % panelCount
		case "shift+tab":
			m.focusedPanel = (m.focusedPanel - 1 + panelCount) % panelCount
		case "up", "k":
			m.cursorUp()
		case "down", "j":
			m.cursorDown()
		case "enter":
			m.showDetail()
			if m.editFile != "" {
				path := m.editFile
				m.editFile = ""
				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "vi"
				}
				c := exec.Command(editor, path)
				return m, tea.ExecProcess(c, func(err error) tea.Msg { return nil })
			}
		case "esc":
			m.detailView = ""
		case "p":
			m.paused = !m.paused
		case "?":
			m.showHelp = !m.showHelp
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case processTreeMsg:
		if !m.paused {
			// Buffer latest tree — only rendered on tickMsg
			m.pendingTree = msg.Tree
			m.pendingProcs = msg.AllProcs

			// Classify and check leaks — JSONL handles tool call recording
			m.detector.Update(msg.Tree)
			m.leakDetector.Check(msg.Tree, msg.NewPIDs)

			// Spawn rate tracking
			now := time.Now()
			for range msg.NewPIDs {
				m.spawnHistory = append(m.spawnHistory, now)
			}
			cutoff := now.Add(-5 * time.Second)
			start := 0
			for start < len(m.spawnHistory) && m.spawnHistory[start].Before(cutoff) {
				start++
			}
			m.spawnHistory = m.spawnHistory[start:]
			m.spawnRate = float64(len(m.spawnHistory)) / 5.0

			// Track new PIDs for flash
			for _, pid := range msg.NewPIDs {
				m.newPIDs[pid] = now
			}
			for pid, t := range m.newPIDs {
				if now.Sub(t) > 2*time.Second {
					delete(m.newPIDs, pid)
				}
			}
		}
		return m, m.listenProcessTree()

	case procLifecycleMsg:
		if !m.paused {
			ev := bus.ProcLifecycleEvent(msg)
			m.procHistory.Push(ev)

			// Classify for tree display only — JSONL is source of truth for tool calls
			if ev.Info != nil {
				ev.Info.Role = m.backend.ClassifyProcess(ev.Info)
			}
		}
		return m, m.listenProcLifecycle()

	case toolCallMsg:
		if !m.paused {
			tc := model.ToolCall(msg)

			switch tc.ToolName {
			case "Read", "Write", "Edit", "Grep", "Glob":
				// File ops → File Activity panel only
				path := ""
				for _, arg := range tc.Args {
					path = arg
					break
				}
				if path != "" {
					tag := toolTag(tc.ToolName)
					entry, ok := m.fileEntries[path]
					if !ok {
						entry = &fileEntry{
							Path:  path,
							Ops:   make(map[string]bool),
							Order: m.fileOrder,
						}
						m.fileOrder++
						m.fileEntries[path] = entry
					}
					entry.Ops[tag] = true
					entry.Last = tc.Timestamp
				}
			default:
				// Everything else → Live Tool Calls
				m.toolCalls.Push(tc)
				// Queue Bash commands for tree annotation
				if tc.ToolName == "Bash" && len(tc.Args) > 0 {
					cmd := tc.Args[0]
					label := cmd
					if len(label) > 60 {
						label = label[:57] + "..."
					}
					m.pendingCmds = append(m.pendingCmds, pendingCmd{
						Time:  tc.Timestamp,
						Label: label,
					})
					// Keep last 20
					if len(m.pendingCmds) > 20 {
						m.pendingCmds = m.pendingCmds[len(m.pendingCmds)-20:]
					}
				}
			}
		}
		return m, m.listenToolCalls()

	case alertMsg:
		if !m.paused {
			m.alerts = append(m.alerts, bus.AlertEvent(msg))
			if len(m.alerts) > 10 {
				m.alerts = m.alerts[len(m.alerts)-10:]
			}
		}
		return m, m.listenAlerts()

	case tickMsg:
		m.uptime = time.Since(m.sessionStart)
		// Merge buffered snapshot into stable tree
		if m.pendingTree != nil {
			m.mergeIntoStableTree(m.pendingTree)
			m.allProcs = m.pendingProcs
			m.updateStats()
			m.pendingTree = nil
			m.pendingProcs = nil
		}
		return m, tickCmd()
	}

	return m, nil
}

func (m *Model) updateToolCalls(allProcs []*model.ProcessInfo, newPIDs, exitedPIDs []int32) {
	now := time.Now()

	// New tool processes become active tool calls
	newSet := make(map[int32]bool, len(newPIDs))
	for _, pid := range newPIDs {
		newSet[pid] = true
	}

	for _, proc := range allProcs {
		if newSet[proc.PID] {
			debug.Log("  NEW PID=%d Role=%s Name=%s", proc.PID, proc.Role, displayName(proc))
		}
		if proc.Role == model.RoleToolProcess && newSet[proc.PID] {
			toolName := displayName(proc)
			var args []string

			// Check if this is a Claude shell wrapper — extract actual command
			if extracted := backend.ExtractToolFromWrapper(proc.Cmdline); extracted != "" {
				parts := strings.Fields(extracted)
				if len(parts) > 0 {
					toolName = filepath.Base(parts[0])
					if len(parts) > 1 {
						args = parts[1:]
					}
				}
			} else {
				if len(proc.Cmdline) > 1 {
					args = proc.Cmdline[1:]
				}
			}

			tc := model.ToolCall{
				Timestamp: now,
				AgentID:   proc.AgentID,
				ToolName:  toolName,
				Args:      args,
				PID:       proc.PID,
			}
			m.activeTools[proc.PID] = tc
		}
	}

	// Exited tool processes complete their tool call
	for _, pid := range exitedPIDs {
		if tc, ok := m.activeTools[pid]; ok {
			dur := now.Sub(tc.Timestamp)
			tc.Duration = &dur
			exitCode := 0
			tc.ExitCode = &exitCode
			m.toolCalls.Push(tc)
			delete(m.activeTools, pid)
		}
	}
}

func (m *Model) cursorUp() {
	if m.cursor[m.focusedPanel] > 0 {
		m.cursor[m.focusedPanel]--
	}
}

func (m *Model) cursorDown() {
	m.cursor[m.focusedPanel]++
	// Clamped in view functions
}


func (m *Model) showDetail() {
	idx := m.cursor[m.focusedPanel]
	switch m.focusedPanel {
	case panelAgentTree:
		nodes := m.flattenStableTree()
		if idx < len(nodes) {
			n := nodes[idx]
			status := "running"
			if n.Exited {
				status = "exited"
			}
			m.detailView = fmt.Sprintf(
				"Process Detail\n\n"+
					"PID:     %d\n"+
					"PPID:    %d\n"+
					"Name:    %s\n"+
					"Role:    %s\n"+
					"RSS:     %s\n"+
					"Status:  %s\n"+
					"Cmdline: %s",
				n.PID, n.PPID, stableDisplayName(n), n.Role,
				formatBytes(n.RSS), status,
				strings.Join(n.Cmdline, " "))
		}
	case panelPromptFiles:
		if idx < len(m.promptFiles) {
			m.editFile = m.promptFiles[idx].Path
		}
	case panelToolCalls:
		calls := m.toolCalls.All()
		if idx < len(calls) {
			tc := calls[idx]
			durStr := "running"
			if tc.Duration != nil {
				durStr = fmt.Sprintf("%.1fs", tc.Duration.Seconds())
			}
			m.detailView = fmt.Sprintf(
				"Tool Call Detail\n\n"+
					"Tool:     %s\n"+
					"Agent:    %s\n"+
					"Time:     %s\n"+
					"Duration: %s\n"+
					"Args:\n  %s",
				tc.ToolName, tc.AgentID,
				tc.Timestamp.Format("15:04:05"),
				durStr,
				strings.Join(tc.Args, "\n  "))
		}
	case panelFileActivity:
		entries := m.sortedFileEntries()
		if idx < len(entries) {
			e := entries[idx]
			var tags []string
			for _, t := range []string{"R", "W", "E", "G", "F"} {
				if e.Ops[t] {
					full := map[string]string{"R": "Read", "W": "Write", "E": "Edit", "G": "Grep", "F": "Glob"}
					tags = append(tags, full[t])
				}
			}
			m.detailView = fmt.Sprintf(
				"File Detail\n\n"+
					"Path:       %s\n"+
					"Operations: %s\n"+
					"Last:       %s",
				e.Path, strings.Join(tags, ", "),
				e.Last.Format("15:04:05"))
		}
	}
}

func (m *Model) updateStats() {
	m.totalProcs = len(m.allProcs)
	m.runningCount = 0
	m.sleepCount = 0
	m.zombieCount = 0
	m.exitedCount = 0
	m.totalCPU = 0
	m.totalRSS = 0

	for _, p := range m.allProcs {
		m.totalCPU += p.CPUPercent
		m.totalRSS += p.RSS

		state := strings.ToLower(p.State)
		switch {
		case p.ExitTime != nil:
			m.exitedCount++
		case strings.Contains(state, "zombie"):
			m.zombieCount++
		case strings.Contains(state, "run"):
			m.runningCount++
		case strings.Contains(state, "sleep") || strings.Contains(state, "idle"):
			m.sleepCount++
		}
	}
}

// displayName returns a human-readable name for a process,
// preferring cmdline basename over Name (which can be a version string on macOS).
// For node processes running scripts, shows the script name instead.
func displayName(proc *model.ProcessInfo) string {
	if len(proc.Cmdline) == 0 {
		return proc.Name
	}

	base := filepath.Base(proc.Cmdline[0])

	// For node/python running a script, show the script name
	if (base == "node" || base == "python" || base == "python3") && len(proc.Cmdline) > 1 {
		scriptBase := filepath.Base(proc.Cmdline[1])
		return base + ":" + scriptBase
	}

	// For shell wrappers, extract the actual command
	if base == "zsh" || base == "bash" || base == "sh" {
		if extracted := backend.ExtractToolFromWrapper(proc.Cmdline); extracted != "" {
			parts := strings.Fields(extracted)
			if len(parts) > 0 {
				return filepath.Base(parts[0])
			}
		}
	}

	if base != "" && base != "." {
		return base
	}
	return proc.Name
}

// mergeIntoStableTree adds new processes from snapshot, marks exited ones.
// Never removes or reorders existing nodes.
func (m *Model) mergeIntoStableTree(snapshot *model.ProcessInfo) {
	if m.stableTree == nil {
		// First snapshot — initialize
		m.stableTree = m.snapshotToStable(snapshot)
		m.markAllSeen(m.stableTree)
		return
	}

	// Build PID set from current live snapshot
	livePIDs := make(map[int32]bool)
	var collectLive func(*model.ProcessInfo)
	collectLive = func(node *model.ProcessInfo) {
		livePIDs[node.PID] = true
		for _, c := range node.Children {
			collectLive(c)
		}
	}
	collectLive(snapshot)

	// Walk snapshot, add any new PIDs to stable tree
	var addNew func(snapNode *model.ProcessInfo, stableParent *stableNode)
	addNew = func(snapNode *model.ProcessInfo, stableParent *stableNode) {
		for _, child := range snapNode.Children {
			if !m.seenPIDs[child.PID] {
				newNode := &stableNode{
					PID:     child.PID,
					PPID:    child.PPID,
					Name:    child.Name,
					Cmdline: child.Cmdline,
					Role:    m.backend.ClassifyProcess(child),
					RSS:     child.RSS,
				}
				// Annotate shell processes with JSONL command
				name := child.Name
				if len(child.Cmdline) > 0 {
					name = filepath.Base(child.Cmdline[0])
				}
				if (name == "zsh" || name == "bash" || name == "sh") && len(m.pendingCmds) > 0 {
					newNode.Label = m.pendingCmds[0].Label
					m.pendingCmds = m.pendingCmds[1:]
				}
				stableParent.Children = append(stableParent.Children, newNode)
				m.seenPIDs[child.PID] = true
			}
			// Recurse — find matching stable child
			if sc := findStableNode(stableParent, child.PID); sc != nil {
				// Update live stats
				sc.RSS = child.RSS
				sc.Exited = false
				addNew(child, sc)
			}
		}
	}
	addNew(snapshot, m.stableTree)

	// Update root stats
	m.stableTree.RSS = snapshot.RSS

	// Mark exited: anything in stable tree but not in live snapshot
	var markExited func(node *stableNode)
	markExited = func(node *stableNode) {
		if !livePIDs[node.PID] && node.PID != m.rootPID {
			node.Exited = true
		}
		for _, c := range node.Children {
			markExited(c)
		}
	}
	markExited(m.stableTree)
}

func (m *Model) snapshotToStable(node *model.ProcessInfo) *stableNode {
	sn := &stableNode{
		PID:     node.PID,
		PPID:    node.PPID,
		Name:    node.Name,
		Cmdline: node.Cmdline,
		Role:    m.backend.ClassifyProcess(node),
		RSS:     node.RSS,
		Exited:  node.ExitTime != nil,
	}
	for _, c := range node.Children {
		sn.Children = append(sn.Children, m.snapshotToStable(c))
	}
	return sn
}

func (m *Model) markAllSeen(node *stableNode) {
	m.seenPIDs[node.PID] = true
	for _, c := range node.Children {
		m.markAllSeen(c)
	}
}

func findStableNode(root *stableNode, pid int32) *stableNode {
	if root.PID == pid {
		return root
	}
	for _, c := range root.Children {
		if found := findStableNode(c, pid); found != nil {
			return found
		}
	}
	return nil
}

func (m *Model) flattenStableTree() []*stableNode {
	if m.stableTree == nil {
		return nil
	}
	var result []*stableNode
	var walk func(*stableNode)
	walk = func(node *stableNode) {
		result = append(result, node)
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(m.stableTree)
	return result
}

func (m *Model) sortedFileEntries() []*fileEntry {
	entries := make([]*fileEntry, 0, len(m.fileEntries))
	for _, e := range m.fileEntries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Order < entries[j].Order
	})
	return entries
}

// applyScroll clamps cursor, applies scroll window, highlights selected line.
func (m *Model) applyScroll(lines []string, p panel, height int) string {
	if len(lines) == 0 {
		return ""
	}

	// Clamp cursor
	if m.cursor[p] >= len(lines) {
		m.cursor[p] = len(lines) - 1
	}
	if m.cursor[p] < 0 {
		m.cursor[p] = 0
	}

	cur := m.cursor[p]

	// Compute visible window around cursor
	start := 0
	if len(lines) > height && height > 0 {
		// Keep cursor visible
		if cur >= start+height {
			start = cur - height + 1
		}
		if cur < start {
			start = cur
		}
		// Default: show end for bottom-anchored panels
	}

	end := start + height
	if end > len(lines) {
		end = len(lines)
		start = max(0, end-height)
	}

	visible := lines[start:end]

	// Highlight cursor line
	var result []string
	for i, line := range visible {
		if i+start == cur && m.focusedPanel == p {
			result = append(result, cursorStyle.Render(line))
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}


func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	if m.detailView != "" {
		w := m.width - 6
		if w < 40 {
			w = 40
		}
		// Word-wrap detail content to fit
		wrapped := wordWrap(m.detailView, w-6)
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			panelStyle.Padding(1, 2).Width(w).Render(wrapped+"\n\n"+helpDescStyle.Render("Press any key to close")))
	}

	if m.showHelp {
		return m.viewHelp()
	}

	header := m.viewHeader()

	contentHeight := m.height - 3
	topHeight := contentHeight * 50 / 100
	midHeight := contentHeight * 30 / 100
	botHeight := contentHeight - topHeight - midHeight

	leftWidth := m.width * 60 / 100
	rightWidth := m.width - leftWidth

	// Inner content height = panel height - border(2) - title(1)
	innerH := func(h int) int {
		if h-3 < 1 {
			return 1
		}
		return h - 3
	}

	agentTree := m.viewAgentTree(leftWidth-4, innerH(topHeight))
	promptFiles := m.viewPromptFiles(rightWidth-4, innerH(topHeight))

	topLeft := m.renderPanel("Agent Tree", agentTree, leftWidth, topHeight, panelAgentTree)
	topRight := m.renderPanel("Config & Prompts", promptFiles, rightWidth, topHeight, panelPromptFiles)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, topLeft, topRight)

	toolCalls := m.viewToolCalls(m.width-4, innerH(midHeight))
	midRow := m.renderPanel("Live Tool Calls", toolCalls, m.width, midHeight, panelToolCalls)

	// Bottom row: Stats (left) + File Activity (right)
	botLeftW := m.width * 40 / 100
	botRightW := m.width - botLeftW

	stats := m.viewStats(botLeftW-4, innerH(botHeight))
	fileAct := m.viewFileActivity(botRightW-4, innerH(botHeight))

	botLeft := m.renderPanel("Process Stats", stats, botLeftW, botHeight, panelStats)
	botRight := m.renderPanel("File Activity", fileAct, botRightW, botHeight, panelFileActivity)
	botRow := lipgloss.JoinHorizontal(lipgloss.Top, botLeft, botRight)

	return lipgloss.JoinVertical(lipgloss.Left, header, topRow, midRow, botRow)
}

func (m Model) viewHeader() string {
	agentCount := len(m.agents)
	uptime := formatDuration(m.uptime)
	pauseIndicator := ""
	if m.paused {
		pauseIndicator = " [PAUSED]"
	}

	text := fmt.Sprintf(" clanktop -- %s (PID %d) -- uptime %s -- %d agents -- %d procs%s ",
		m.clientName, m.rootPID, uptime, agentCount, m.totalProcs, pauseIndicator)

	return headerStyle.Width(m.width).Render(text)
}

func (m Model) renderPanel(title, content string, width, height int, p panel) string {
	style := panelStyle
	if m.focusedPanel == p {
		style = focusedPanelStyle
	}

	titleRendered := titleStyle.Render(fmt.Sprintf(" %s ", title))

	// Pad or clip content to exact height (minus border 2, minus title 1)
	innerH := height - 3
	if innerH < 1 {
		innerH = 1
	}
	lines := strings.Split(content, "\n")
	for len(lines) < innerH {
		lines = append(lines, "")
	}
	if len(lines) > innerH {
		lines = lines[:innerH]
	}

	return style.
		Width(width - 2).
		Render(titleRendered + "\n" + strings.Join(lines, "\n"))
}

func (m Model) viewAgentTree(width, height int) string {
	if m.stableTree == nil {
		return "Waiting for process data..."
	}

	var lines []string

	var renderNode func(node *stableNode, prefix string, isLast bool)
	renderNode = func(node *stableNode, prefix string, isLast bool) {
		connector := "├─"
		if isLast {
			connector = "└─"
		}
		if prefix == "" {
			connector = "▼"
		}

		name := stableDisplayName(node)
		nameStr := m.styleForStableRole(node, name)

		memStr := ""
		if node.RSS > 0 {
			memStr = fmt.Sprintf(" %s", formatBytes(node.RSS))
		}
		statusStr := ""
		if node.Exited {
			statusStr = exitOkStyle.Render(" [exited]")
		}

		line := fmt.Sprintf("%s%s %s (PID %d)%s%s", prefix, connector, nameStr, node.PID, memStr, statusStr)
		lines = append(lines, line)

		childPrefix := prefix
		if prefix == "" {
			childPrefix = "  "
		} else if isLast {
			childPrefix = prefix + "   "
		} else {
			childPrefix = prefix + "│  "
		}

		for i, child := range node.Children {
			renderNode(child, childPrefix, i == len(node.Children)-1)
		}
	}

	renderNode(m.stableTree, "", true)
	return m.applyScroll(lines, panelAgentTree, height)
}

func stableDisplayName(node *stableNode) string {
	// Use JSONL-derived label for shell wrappers
	if node.Label != "" {
		return node.Label
	}
	if len(node.Cmdline) > 0 {
		base := filepath.Base(node.Cmdline[0])
		if (base == "node" || base == "python" || base == "python3") && len(node.Cmdline) > 1 {
			return base + ":" + filepath.Base(node.Cmdline[1])
		}
		if base == "zsh" || base == "bash" || base == "sh" {
			if extracted := backend.ExtractToolFromWrapper(node.Cmdline); extracted != "" {
				parts := strings.Fields(extracted)
				if len(parts) > 0 {
					return filepath.Base(parts[0])
				}
			}
		}
		if base != "" && base != "." {
			return base
		}
	}
	return node.Name
}

func (m Model) styleForStableRole(node *stableNode, name string) string {
	switch node.Role {
	case model.RoleOrchestrator:
		return orchestratorStyle.Render(name)
	case model.RoleSubAgent:
		return subAgentStyle.Render(name)
	case model.RoleToolProcess:
		return toolStyle.Render(name)
	case model.RoleRuntime:
		return runtimeStyle.Render(name)
	case model.RoleInfra:
		return infraStyle.Render(name)
	case model.RoleMCPServer:
		return mcpStyle.Render(name)
	default:
		return runningStyle.Render(name)
	}
}

func (m Model) styleForRole(proc *model.ProcessInfo) string {
	name := displayName(proc)

	switch proc.Role {
	case model.RoleOrchestrator:
		return orchestratorStyle.Render(name)
	case model.RoleSubAgent:
		return subAgentStyle.Render(name)
	case model.RoleToolProcess:
		return toolStyle.Render(name)
	case model.RoleRuntime:
		return runtimeStyle.Render(name)
	case model.RoleInfra:
		return infraStyle.Render(name)
	case model.RoleMCPServer:
		return mcpStyle.Render(name)
	default:
		// Spawned child process — green
		return runningStyle.Render(name)
	}
}

func (m Model) viewPromptFiles(width, height int) string {
	if len(m.promptFiles) == 0 {
		return "No prompt/config files found"
	}

	home, _ := os.UserHomeDir()
	var lines []string
	for _, f := range m.promptFiles {
		path := f.Path
		// Shorten home prefix
		if home != "" && strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}
		modAge := time.Since(f.Timestamp)
		ageStr := formatAge(modAge)
		srcTag := ""
		if f.Source == "scan" {
			srcTag = ""
		}
		line := fmt.Sprintf("  %s  %-6s %s", ageStr, srcTag, path)
		if len(line) > width {
			line = line[:width]
		}
		lines = append(lines, line)
	}

	return m.applyScroll(lines, panelPromptFiles, height)
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (m Model) viewToolCalls(width, height int) string {
	completed := m.toolCalls.All()

	// Build combined list: completed + active
	var lines []string
	for _, tc := range completed {
		line := m.formatToolCall(tc)
		lines = append(lines, line)
	}
	for _, tc := range m.activeTools {
		line := runningStyle.Render(m.formatToolCall(tc))
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return "No tool calls yet..."
	}

	// Reverse — latest at top
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	return m.applyScroll(lines, panelToolCalls, height)
}

func (m Model) formatToolCall(tc model.ToolCall) string {
	timeStr := tc.Timestamp.Format("15:04:05")
	argsStr := strings.Join(tc.Args, " ")

	durStr := ""
	if tc.Duration != nil {
		durStr = fmt.Sprintf(" %.1fs", tc.Duration.Seconds())
	}

	// Use available width: time(8) + space + tool(variable) + space + args + duration
	prefix := fmt.Sprintf("  %s %-10s ", timeStr, tc.ToolName)
	maxArgs := m.width - len(prefix) - len(durStr) - 4
	if maxArgs < 20 {
		maxArgs = 20
	}
	if len(argsStr) > maxArgs {
		argsStr = argsStr[:maxArgs-3] + "..."
	}

	return prefix + argsStr + durStr
}

func toolTag(name string) string {
	switch name {
	case "Read":
		return "R"
	case "Write":
		return "W"
	case "Edit":
		return "E"
	case "Grep":
		return "G"
	case "Glob":
		return "F"
	default:
		return "?"
	}
}

func (m Model) viewFileActivity(width, height int) string {
	if len(m.fileEntries) == 0 {
		return "No file activity yet..."
	}

	// Sort by last activity, most recent first
	entries := make([]*fileEntry, 0, len(m.fileEntries))
	for _, e := range m.fileEntries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Last.After(entries[j].Last)
	})

	home, _ := os.UserHomeDir()
	var lines []string
	for _, e := range entries {
		path := e.Path
		if home != "" && strings.HasPrefix(path, home) {
			path = "~" + path[len(home):]
		}

		// Build tag string like [RWE] or [RG]
		var tags string
		for _, t := range []string{"R", "W", "E", "G", "F"} {
			if e.Ops[t] {
				tags += t
			}
		}

		// Color tags: writes red, reads green
		var tagStr string
		if e.Ops["W"] || e.Ops["E"] {
			tagStr = exitErrStyle.Render("[" + tags + "]")
		} else {
			tagStr = runningStyle.Render("[" + tags + "]")
		}

		line := fmt.Sprintf("  %-7s %s", tagStr, path)
		if len(line) > width+4 {
			line = line[:width+1] + "..."
		}
		lines = append(lines, line)
	}

	return m.applyScroll(lines, panelFileActivity, height)
}

func (m Model) viewStats(width, height int) string {
	statsLine := fmt.Sprintf("  Total: %d  Running: %d  Sleeping: %d  Zombie: %d  Exited: %d",
		m.totalProcs, m.runningCount, m.sleepCount, m.zombieCount, m.exitedCount)

	resourceLine := fmt.Sprintf("  CPU: %.1f%%  RSS: %s  Spawn rate: %.1f/s",
		m.totalCPU, formatBytes(m.totalRSS), m.spawnRate)

	result := statsLine + "\n" + resourceLine

	if len(m.alerts) > 0 {
		latest := m.alerts[len(m.alerts)-1]
		result += "\n" + alertStyle.Render("  ⚠ "+latest.Message)
	}

	return result
}

func (m Model) viewHelp() string {
	pairs := []struct{ key, desc string }{
		{"q/Ctrl+C", "Quit"},
		{"Tab", "Cycle panel focus"},
		{"↑/↓", "Navigate within panel"},
		{"Enter", "Expand/collapse node"},
		{"p", "Pause/resume display"},
		{"?", "Toggle help"},
	}

	var lines []string
	lines = append(lines, titleStyle.Render("  Keybindings"))
	lines = append(lines, "")
	for _, p := range pairs {
		lines = append(lines, fmt.Sprintf("  %s  %s",
			helpKeyStyle.Render(fmt.Sprintf("%-12s", p.key)),
			helpDescStyle.Render(p.desc)))
	}
	lines = append(lines, "")
	lines = append(lines, helpDescStyle.Render("  Press any key to close"))

	content := strings.Join(lines, "\n")

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		panelStyle.Padding(1, 2).Render(content))
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func wordWrap(s string, width int) string {
	if width <= 0 {
		return s
	}
	var result strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			if result.Len() > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(line)
			continue
		}
		for len(line) > 0 {
			if result.Len() > 0 {
				result.WriteByte('\n')
			}
			cut := width
			if cut > len(line) {
				cut = len(line)
			}
			// Try to break at space
			if cut < len(line) {
				if idx := strings.LastIndex(line[:cut], " "); idx > width/3 {
					cut = idx + 1
				}
			}
			result.WriteString(line[:cut])
			line = line[cut:]
		}
	}
	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
