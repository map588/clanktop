package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/map588/clanktop/internal/model"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// deadPID returns the PID of a process that has exited and was reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestListSessions(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	live := os.Getpid()
	dead := deadPID(t)
	// Real registry record (trimmed), with the live PID of this test.
	writeFile(t, filepath.Join(dir, fmt.Sprintf("%d.json", live)), fmt.Sprintf(
		`{"pid":%d,"sessionId":"2c180fbe-0809-42cf-90e8-6a37acb4ab73","cwd":"/Users/x/git/tui/clanktop","startedAt":1791733816030,"version":"2.1.296","kind":"interactive","entrypoint":"cli","name":"clanktop-cf","status":"busy","updatedAt":1791733996615}`, live))
	writeFile(t, filepath.Join(dir, fmt.Sprintf("%d.json", dead)), fmt.Sprintf(
		`{"pid":%d,"sessionId":"dead","cwd":"/x","startedAt":1,"kind":"interactive","status":"idle"}`, dead))
	// Key files are secrets. ListSessions must not read them.
	writeFile(t, filepath.Join(dir, fmt.Sprintf("%d.abc.key", live)), "not json")
	writeFile(t, filepath.Join(dir, "broken.json"), "{")

	got := NewClaudeCode(home).Sessions()
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.PID != int32(live) || s.SessionID != "2c180fbe-0809-42cf-90e8-6a37acb4ab73" ||
		s.Cwd != "/Users/x/git/tui/clanktop" || s.Kind != "interactive" || s.Status != "busy" ||
		s.Name != "clanktop-cf" || s.StartedAt.UnixMilli() != 1791733816030 {
		t.Fatalf("wrong session: %+v", s)
	}
}

// Claude Code replaces '_' and '.' in the cwd with '-' in the project directory
// name. The old encoder only replaced '/', so it did not find this log.
func TestLogPathWithUnderscoreCwd(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "-Users-x-modified-sx1302-hal")
	writeFile(t, filepath.Join(proj, "s1.jsonl"), "")
	writeFile(t, filepath.Join(proj, "s1", "subagents", "agent-a1.jsonl"), "")
	writeFile(t, filepath.Join(proj, "s1", "subagents", "agent-a1.meta.json"), "{}")
	writeFile(t, filepath.Join(proj, "s2.jsonl"), "")

	cc := NewClaudeCode(home)
	log := cc.LogPath("s1")
	if log != filepath.Join(proj, "s1.jsonl") {
		t.Fatalf("LogPath = %q", log)
	}
	if cc.LogPath("missing") != "" {
		t.Fatal("LogPath of a missing session must be empty")
	}
	subs := cc.SubAgentLogs(log, "s1")
	if len(subs) != 1 || filepath.Base(subs[0]) != "agent-a1.jsonl" {
		t.Fatalf("SubAgentLogs = %v", subs)
	}
}

func TestClassifyChildAndLabel(t *testing.T) {
	cc := NewClaudeCode("")
	bash := []string{"/bin/zsh", "-c", "source /Users/x/.claude/shell-snapshots/snap.sh 2>/dev/null || true && eval 'go test ./...\necho done' < /dev/null"}
	tests := []struct {
		name      string
		cmdline   []string
		psName    string
		wantRole  model.ProcessRole
		wantLabel string
		hidden    bool
	}{
		{"bash tool", bash, "zsh", model.RoleToolProcess, "go test ./...", false},
		{"hook shell", []string{"/bin/sh", "-c", "echo hi"}, "sh", model.RoleToolProcess, "sh", false},
		{"node mcp server", []string{"node", "/Users/x/.nvm/bin/memstate-mcp", "--embed-model", "q"}, "node", model.RoleMCPServer, "memstate-mcp", false},
		{"npm exec server", []string{"npm", "exec", "cclsp@latest"}, "npm", model.RoleMCPServer, "cclsp@latest", false},
		{"native server", []string{"/Users/x/.cargo/bin/tokensave", "serve"}, "tokensave", model.RoleMCPServer, "tokensave", false},
		{"no cmdline", nil, "/usr/bin/caffeinate", model.RoleMCPServer, "caffeinate", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &model.ProcessInfo{Name: tt.psName, Cmdline: tt.cmdline}
			if got := cc.ClassifyChild(p); got != tt.wantRole {
				t.Errorf("ClassifyChild = %v, want %v", got, tt.wantRole)
			}
			if got := cc.Label(p); got != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got, tt.wantLabel)
			}
			if got := cc.Hidden(p); got != tt.hidden {
				t.Errorf("Hidden = %v, want %v", got, tt.hidden)
			}
		})
	}
}

func TestFileAccessFilter(t *testing.T) {
	cc := NewClaudeCode("")
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/x/src/main.go", true},
		{"/Users/x/app/node_modules/chalk/index.js", false},
		{"/usr/lib/libSystem.dylib", false},
		{"/System/Library/Frameworks/foo", false},
		{"/private/var/db/timezone/tz", false},
		{"/dev/null", false},
	}
	for _, tt := range tests {
		if got := cc.FileAccessFilter(tt.path); got != tt.want {
			t.Errorf("FileAccessFilter(%s) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtractToolFromWrapper(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    string
	}{
		{"wrapper", []string{"/bin/zsh", "-c", "source /Users/x/.claude/shell-snapshots/snap.sh 2>/dev/null || true && eval 'cat /home/user/file.txt' < /dev/null"}, "cat /home/user/file.txt"},
		{"not a wrapper", []string{"zsh", "-c", "echo hello"}, ""},
		{"not a shell", []string{"cat", "foo.txt"}, ""},
	}
	for _, tt := range tests {
		if got := ExtractToolFromWrapper(tt.cmdline); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
