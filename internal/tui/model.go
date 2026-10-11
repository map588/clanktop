// Package tui shows the agent sessions from the collector.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/map588/clanktop/internal/collector"
	"github.com/map588/clanktop/internal/filetracker"
	"github.com/map588/clanktop/internal/model"
)

type panel int

const (
	panelAgents panel = iota
	panelActivity
	panelFiles
	panelProcs
	panelCount
)

// refreshInterval is how often the TUI takes a snapshot from the collector.
const refreshInterval = 500 * time.Millisecond

// recentSubAgent is how long the roster shows a subagent after it stops.
const recentSubAgent = 10 * time.Minute

type tickMsg time.Time

type Model struct {
	c          *collector.Collector
	clientName string
	home       string

	width, height int
	focused       panel
	paused        bool
	showHelp      bool
	detail        string                   // Non-empty: the detail overlay is open
	config        []filetracker.PromptFile // Non-nil: the config overlay is open
	configCur     int

	views  []model.AgentView
	now    time.Time
	sel    string // Key of the selected roster row
	cursor [panelCount]int
}

func NewModel(c *collector.Collector, clientName, home string) Model {
	return Model{c: c, clientName: clientName, home: home, now: time.Now()}
}

func (m Model) Init() tea.Cmd {
	return func() tea.Msg { return tickMsg(time.Now()) }
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		if !m.paused {
			m.now = time.Time(msg)
			m.views = m.c.Snapshot(m.now)
		}
		return m, tickCmd()
	case tea.KeyMsg:
		// Keys that arrive in one read (key repeat, fast typing) come as one
		// message with many runes. Handle each rune as one key press.
		if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				next, cmd := m.handleKey(string(r))
				m = next.(Model)
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}
		return m.handleKey(msg.String())
	}
	return m, nil
}

func (m Model) handleKey(k string) (tea.Model, tea.Cmd) {
	if k == "ctrl+c" || k == "q" {
		return m, tea.Quit
	}
	if m.showHelp || m.detail != "" {
		m.showHelp, m.detail = false, ""
		return m, nil
	}
	if m.config != nil {
		switch k {
		case "esc", "c":
			m.config = nil
		case "up", "k":
			m.configCur = max(0, m.configCur-1)
		case "down", "j":
			m.configCur = min(len(m.config)-1, m.configCur+1)
		case "enter", "e":
			if m.configCur < len(m.config) {
				return m, editCmd(m.config[m.configCur].Path)
			}
		}
		return m, nil
	}

	switch k {
	case "tab":
		m.focused = (m.focused + 1) % panelCount
	case "shift+tab":
		m.focused = (m.focused + panelCount - 1) % panelCount
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter":
		m.detail = m.detailText()
	case "e":
		if path := m.selectedFile(); path != "" {
			return m, editCmd(path)
		}
	case "c":
		if r, ok := m.selected(); ok {
			logDir := ""
			if r.v.LogPath != "" {
				logDir = filepath.Dir(r.v.LogPath)
			}
			m.config = filetracker.ScanPromptFiles(m.home, r.v.Cwd, logDir)
			if m.config == nil {
				m.config = []filetracker.PromptFile{}
			}
			m.configCur = 0
		}
	case "p":
		m.paused = !m.paused
	case "?":
		m.showHelp = true
	}
	return m, nil
}

func editCmd(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	return tea.ExecProcess(exec.Command(editor, path), func(error) tea.Msg { return nil })
}

// row is one line of the agent roster: a session, or a subagent of a session.
// prefix holds the tree lines before the row, as in the process tree of htop.
type row struct {
	key    string
	v      *model.AgentView
	sub    *model.SubAgent
	prefix string
}

// rows builds the roster tree. Under a session are the subagents that it
// started, each under the subagent whose Agent call started it, and then the
// background session that it parked its work in.
func (m *Model) rows() []row {
	byJob := map[string]*model.AgentView{}
	for i := range m.views {
		if j := m.views[i].JobID; j != "" {
			byJob[j] = &m.views[i]
		}
	}
	parkedIn := map[*model.AgentView]*model.AgentView{}
	isParked := map[*model.AgentView]bool{}
	for i := range m.views {
		v := &m.views[i]
		if p := byJob[v.ParkedJobID]; v.ParkedJobID != "" && p != nil && p != v {
			parkedIn[v], isParked[p] = p, true
		}
	}

	type node struct {
		sub  *model.SubAgent
		sess *model.AgentView
	}
	var out []row
	done := map[string]bool{} // Row keys already in out. Stops a cycle in bad data.
	var addSession func(v *model.AgentView, prefix, indent string)
	var addChildren func(v *model.AgentView, kids []node, indent string)
	addChildren = func(v *model.AgentView, kids []node, indent string) {
		for i, k := range kids {
			branch, next := "├─ ", "│  "
			if i == len(kids)-1 {
				branch, next = "└─ ", "   "
			}
			if k.sess != nil {
				addSession(k.sess, indent+branch, indent+next)
				continue
			}
			key := fmt.Sprintf("%d/%s", v.PID, k.sub.ID)
			if done[key] {
				continue
			}
			done[key] = true
			out = append(out, row{key: key, v: v, sub: k.sub, prefix: indent + branch})
			var sub []node
			for _, s := range m.subAgents(v, k.sub.ID) {
				sub = append(sub, node{sub: s})
			}
			addChildren(v, sub, indent+next)
		}
	}
	addSession = func(v *model.AgentView, prefix, indent string) {
		key := strconv.Itoa(int(v.PID))
		if done[key] {
			return
		}
		done[key] = true
		out = append(out, row{key: key, v: v, prefix: prefix})
		var kids []node
		for _, s := range m.subAgents(v, "") {
			kids = append(kids, node{sub: s})
		}
		if p := parkedIn[v]; p != nil {
			kids = append(kids, node{sess: p})
		}
		addChildren(v, kids, indent)
	}
	for i := range m.views {
		if !isParked[&m.views[i]] {
			addSession(&m.views[i], "", "")
		}
	}
	// A cycle of parked sessions has no top. Show such sessions at the top level.
	for i := range m.views {
		addSession(&m.views[i], "", "")
	}
	return out
}

// subAgents returns the shown subagents of v whose parent is parentID. A
// subagent is shown while it runs, for a time after it stops, and while one
// of its own subagents is shown.
func (m *Model) subAgents(v *model.AgentView, parentID string) []*model.SubAgent {
	var out []*model.SubAgent
	for i := range v.SubAgents {
		if s := &v.SubAgents[i]; s.ParentID == parentID && s.ID != parentID && m.shown(v, s, 0) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Model) shown(v *model.AgentView, s *model.SubAgent, depth int) bool {
	if s.Running || m.now.Sub(lastActivity(s)) < recentSubAgent {
		return true
	}
	if depth > len(v.SubAgents) { // A cycle in bad data.
		return false
	}
	for i := range v.SubAgents {
		if c := &v.SubAgents[i]; c.ParentID == s.ID && c != s && m.shown(v, c, depth+1) {
			return true
		}
	}
	return false
}

func lastActivity(s *model.SubAgent) time.Time {
	if n := len(s.Calls); n > 0 {
		return s.Calls[n-1].Start
	}
	return s.Start
}

// selected returns the selected roster row. When the selected agent is gone,
// the first row is selected.
func (m *Model) selected() (row, bool) {
	rows := m.rows()
	if len(rows) == 0 {
		return row{}, false
	}
	return rows[m.selectedIndex(rows)], true
}

func (m *Model) selectedIndex(rows []row) int {
	for i, r := range rows {
		if r.key == m.sel {
			return i
		}
	}
	return 0
}

func (m *Model) move(d int) {
	if m.focused == panelAgents {
		rows := m.rows()
		if len(rows) == 0 {
			return
		}
		i := min(max(m.selectedIndex(rows)+d, 0), len(rows)-1)
		if rows[i].key != m.sel {
			m.sel = rows[i].key
			m.cursor = [panelCount]int{}
		}
		return
	}
	n := m.itemCount(m.focused)
	m.cursor[m.focused] = min(max(m.cursor[m.focused]+d, 0), max(n-1, 0))
}

func (m *Model) itemCount(p panel) int {
	r, ok := m.selected()
	if !ok {
		return 0
	}
	switch p {
	case panelActivity:
		return len(activityItems(r))
	case panelFiles:
		return len(fileItems(r))
	case panelProcs:
		return len(procItems(r.v.Tree))
	}
	return 0
}

// activityItems are the tool calls of the selected agent, the newest first.
func activityItems(r row) []model.ToolCall {
	calls := r.v.Calls
	if r.sub != nil {
		calls = r.sub.Calls
	}
	out := slices.Clone(calls)
	slices.Reverse(out)
	return out
}

// openPerProc is the number of open files shown per process. A language
// server can hold more than a thousand files open.
const openPerProc = 5

// fileItem is a touched file, an open file, the count of open files not shown
// (more > 0), or else the separator line.
type fileItem struct {
	touch *model.FileTouch
	open  *model.OpenFile
	more  int
}

// fileItems are the files that the selected agent touched, then the files that
// the processes of its session hold open. A session includes its subagents.
func fileItems(r row) []fileItem {
	calls := r.v.Calls
	if r.sub != nil {
		calls = r.sub.Calls
	} else {
		for _, s := range r.v.SubAgents {
			calls = append(slices.Clip(calls), s.Calls...)
		}
	}
	var out []fileItem
	for _, t := range model.FilesTouched(calls) {
		out = append(out, fileItem{touch: &t})
	}
	if len(r.v.OpenFiles) > 0 {
		out = append(out, fileItem{})
	}
	shown := map[int32]int{}
	for i := range r.v.OpenFiles {
		f := &r.v.OpenFiles[i]
		shown[f.PID]++
		switch n := shown[f.PID]; {
		case n <= openPerProc:
			out = append(out, fileItem{open: f})
		case n == openPerProc+1:
			out = append(out, fileItem{open: f, more: countPID(r.v.OpenFiles, f.PID) - openPerProc})
		}
	}
	return out
}

func countPID(files []model.OpenFile, pid int32) int {
	n := 0
	for _, f := range files {
		if f.PID == pid {
			n++
		}
	}
	return n
}

type procItem struct {
	node   *model.ProcessInfo
	prefix string
}

func procItems(root *model.ProcessInfo) []procItem {
	if root == nil {
		return nil
	}
	var out []procItem
	var walk func(n *model.ProcessInfo, prefix, childPrefix string)
	walk = func(n *model.ProcessInfo, prefix, childPrefix string) {
		out = append(out, procItem{node: n, prefix: prefix})
		for i, c := range n.Children {
			if i == len(n.Children)-1 {
				walk(c, childPrefix+"└─ ", childPrefix+"   ")
			} else {
				walk(c, childPrefix+"├─ ", childPrefix+"│  ")
			}
		}
	}
	walk(root, "", "")
	return out
}

func (m *Model) selectedFile() string {
	r, ok := m.selected()
	if !ok || m.focused != panelFiles {
		return ""
	}
	items := fileItems(r)
	i := m.cursor[panelFiles]
	if i >= len(items) {
		return ""
	}
	switch it := items[i]; {
	case it.touch != nil:
		return absPath(it.touch.Path, r.v.Cwd)
	case it.open != nil:
		return it.open.Path
	}
	return ""
}

func absPath(p, cwd string) string {
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}
