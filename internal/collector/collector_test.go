package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/model"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "logtailer", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func registry(t *testing.T, home, sessionID, status string) {
	pid := os.Getpid()
	write(t, filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", pid)), fmt.Sprintf(
		`{"pid":%d,"sessionId":%q,"cwd":"/Users/x/proj_a","startedAt":1791733816030,"version":"2.1.296","kind":"interactive","name":"proj-a","status":%q}`,
		pid, sessionID, status))
}

func onlyView(t *testing.T, c *Collector, now time.Time) model.AgentView {
	t.Helper()
	views := c.Snapshot(now)
	if len(views) != 1 {
		t.Fatalf("got %d views", len(views))
	}
	return views[0]
}

func TestCollectorEndToEnd(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "-Users-x-proj-a")
	registry(t, home, "s1", "busy")
	write(t, filepath.Join(proj, "s1.jsonl"), fixture(t, "session.jsonl"))
	write(t, filepath.Join(proj, "s1", "subagents", "agent-a98.jsonl"), fixture(t, "agent-a98.jsonl"))
	write(t, filepath.Join(proj, "s1", "subagents", "agent-a98.meta.json"), fixture(t, "agent-a98.meta.json"))

	c := New(backend.NewClaudeCode(home), 0)
	defer c.close()
	now := time.Now()
	c.PollOnce(now)

	v := onlyView(t, c, now)
	if v.PID != int32(os.Getpid()) || v.Name != "proj-a" || v.Title != "rework-agent-discovery" || len(v.Calls) != 4 {
		t.Fatalf("view: pid %d name %q title %q calls %d", v.PID, v.Name, v.Title, len(v.Calls))
	}
	if len(v.SubAgents) != 1 || v.SubAgents[0].Type != "Explore" || v.SubAgents[0].ID != "a98" || v.SubAgents[0].Running {
		t.Fatalf("subagents: %+v", v.SubAgents)
	}
	if v.Tree == nil || v.Tree.PID != int32(os.Getpid()) || v.Tree.Role != model.RoleOrchestrator {
		t.Fatalf("tree: %+v", v.Tree)
	}
	if len(v.Alerts) != 0 {
		t.Fatalf("alerts: %v", v.Alerts)
	}

	// Claude Code appends the result of the Read call.
	f, err := os.OpenFile(filepath.Join(proj, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"type":"user","timestamp":"2026-10-11T15:53:35.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_C","content":"x"}]}}`)
	f.Close()
	c.PollLogs()
	if v := onlyView(t, c, now); v.Calls[2].End == nil {
		t.Fatalf("Read call has no end after its result: %+v", v.Calls[2])
	}

	// /clear gives the same process a new session ID. It has no log yet.
	registry(t, home, "s2", "busy")
	c.PollSessions()
	c.PollLogs()
	v = onlyView(t, c, now)
	if len(v.Calls) != 0 || len(v.SubAgents) != 0 || v.LogPath != "" {
		t.Fatalf("old session data after the session ID changed: %d calls", len(v.Calls))
	}
	if len(v.Alerts) != 1 || !strings.Contains(v.Alerts[0], "No session log") {
		t.Fatalf("a busy session without a log must warn: %v", v.Alerts)
	}

	// A log in a format that clanktop does not know must warn too.
	write(t, filepath.Join(proj, "s2.jsonl"), strings.Repeat(`{"kind":"msg","body":{}}`+"\n", 25))
	c.PollLogs()
	v = onlyView(t, c, now)
	if len(v.Alerts) != 1 || !strings.Contains(v.Alerts[0], "not recognized") {
		t.Fatalf("unknown format must warn: %v", v.Alerts)
	}
}

func TestPIDFilter(t *testing.T) {
	home := t.TempDir()
	registry(t, home, "s1", "idle")
	c := New(backend.NewClaudeCode(home), 1)
	c.PollSessions()
	if got := c.Snapshot(time.Now()); len(got) != 0 {
		t.Fatalf("filter kept %d sessions", len(got))
	}
}
