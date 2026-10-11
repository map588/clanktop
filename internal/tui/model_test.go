package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/map588/clanktop/internal/model"
)

func testModel(now time.Time) Model {
	end := now.Add(-time.Minute)
	var open []model.OpenFile
	for i := range 12 {
		open = append(open, model.OpenFile{PID: 500, Proc: "gopls-mcp", Path: fmt.Sprintf("/Users/x/p/f%d.go", i)})
	}
	m := NewModel(nil, "claude-code", "/Users/x")
	m.now = now
	m.width, m.height = 120, 30
	m.views = []model.AgentView{
		{
			Session: model.Session{PID: 100, Name: "proj-a", Status: "busy", Kind: "interactive", Cwd: "/Users/x/p"},
			Title:   "a title that is long enough to need truncation in a narrow roster panel",
			Calls: []model.ToolCall{
				{ID: "1", Name: "Read", Summary: "/Users/x/p/main.go", Path: "/Users/x/p/main.go", Op: model.FileOpRead, Start: end.Add(-time.Second), End: &end},
				{ID: "2", Name: "Bash", Summary: strings.Repeat("go test ./... ", 20), Start: now.Add(-3 * time.Second)},
			},
			SubAgents: []model.SubAgent{
				{ID: "a1", Type: "Explore", Description: "map repo", Running: true, Start: now,
					Calls: []model.ToolCall{{ID: "s1", Name: "Grep", Summary: "x in /Users/x/p", Path: "/Users/x/p", Op: model.FileOpSearch, Start: now}}},
				{ID: "a0", Type: "Plan", Description: "old", Start: now.Add(-time.Hour)},
			},
			OpenFiles: open,
			Tree: &model.ProcessInfo{PID: 100, Role: model.RoleOrchestrator, Children: []*model.ProcessInfo{
				{PID: 500, Name: "gopls-mcp", Role: model.RoleMCPServer},
			}},
		},
		{Session: model.Session{PID: 200, Name: "proj-b", Status: "shell", Kind: "bg"}},
	}
	return m
}

func TestViewFitsTheTerminal(t *testing.T) {
	m := testModel(time.Now())
	for _, size := range [][2]int{{120, 30}, {80, 24}, {200, 50}} {
		m.width, m.height = size[0], size[1]
		lines := strings.Split(m.View(), "\n")
		if len(lines) != m.height {
			t.Fatalf("%v: %d lines, want %d", size, len(lines), m.height)
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > m.width {
				t.Fatalf("%v: line %d is %d cells wide: %q", size, i, w, ansi.Strip(l))
			}
		}
	}
}

func TestRosterAndSelection(t *testing.T) {
	m := testModel(time.Now())
	rows := m.rows()
	// The subagent that stopped an hour ago is not in the roster.
	if len(rows) != 3 || rows[1].sub == nil || rows[1].sub.ID != "a1" || rows[2].v.PID != 200 {
		t.Fatalf("rows: %+v", rows)
	}
	m.move(1)
	r, _ := m.selected()
	if r.sub == nil || len(activityItems(r)) != 1 || activityItems(r)[0].Name != "Grep" {
		t.Fatalf("selecting the subagent must show its calls: %+v", r)
	}
	m.move(5)
	if r, _ := m.selected(); r.v.PID != 200 {
		t.Fatalf("move past the end selects the last row, got %d", r.v.PID)
	}
}

func TestOpenFilesCappedPerProcess(t *testing.T) {
	m := testModel(time.Now())
	r, _ := m.selected()
	items := fileItems(r)
	// Touched: main.go and the Grep path of the subagent. Then the separator,
	// five open files and one "+7 more" line.
	if len(items) != 2+1+openPerProc+1 {
		t.Fatalf("got %d items", len(items))
	}
	if last := items[len(items)-1]; last.more != 7 {
		t.Fatalf("last item: %+v", last)
	}
}

// Key repeat can deliver "jj" as one message. Each rune is one key press.
func TestKeyBurstMovesOncePerRune(t *testing.T) {
	m := testModel(time.Now())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	m = next.(Model)
	if r, _ := m.selected(); r.v.PID != 200 {
		t.Fatalf("after jj the third row must be selected, got PID %d sub %v", r.v.PID, r.sub != nil)
	}
}

// The roster is a tree like the process tree of htop: a session, the
// subagents it started (nested by the call that started them), and the
// background session that it parked its work in.
func TestRosterTree(t *testing.T) {
	now := time.Now()
	m := NewModel(nil, "claude-code", "/Users/x")
	m.now = now
	m.views = []model.AgentView{
		{Session: model.Session{PID: 100, Name: "a", ParkedJobID: "j1"}, SubAgents: []model.SubAgent{
			// p1 stopped long ago, but its subagent q1 runs, so p1 stays.
			{ID: "p1", Start: now.Add(-time.Hour)},
			{ID: "q1", ParentID: "p1", Running: true, Start: now.Add(-time.Minute)},
			{ID: "r1", Running: true, Start: now},
			{ID: "old", Start: now.Add(-time.Hour)},
		}},
		{Session: model.Session{PID: 200, Name: "b", Kind: "bg", JobID: "j1"}},
		{Session: model.Session{PID: 300, Name: "c"}},
	}
	var got []string
	for _, r := range m.rows() {
		got = append(got, r.prefix+r.key)
	}
	want := []string{
		"100",
		"├─ 100/p1",
		"│  └─ 100/q1",
		"├─ 100/r1",
		"└─ 200",
		"300",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Bad data with cycles must not hang the TUI or hide a session.
func TestRosterTreeCycles(t *testing.T) {
	now := time.Now()
	m := NewModel(nil, "claude-code", "/Users/x")
	m.now = now
	m.views = []model.AgentView{
		{Session: model.Session{PID: 100, JobID: "j1", ParkedJobID: "j2"}, SubAgents: []model.SubAgent{
			{ID: "a", ParentID: "b", Start: now.Add(-time.Hour)},
			{ID: "b", ParentID: "a", Start: now.Add(-time.Hour)},
		}},
		{Session: model.Session{PID: 200, JobID: "j2", ParkedJobID: "j1"}},
	}
	keys := map[string]bool{}
	for _, r := range m.rows() {
		keys[r.key] = true
	}
	if !keys["100"] || !keys["200"] {
		t.Fatalf("sessions in a parking cycle must be shown: %v", keys)
	}
}
