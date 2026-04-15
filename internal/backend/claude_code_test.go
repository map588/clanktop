package backend

import (
	"testing"

	"github.com/map588/clanktop/internal/model"
)

func TestClassifyProcess(t *testing.T) {
	cc := NewClaudeCode("")

	tests := []struct {
		name    string
		proc    model.ProcessInfo
		want    model.ProcessRole
	}{
		{
			name: "bash is tool process",
			proc: model.ProcessInfo{Name: "bash", Cmdline: []string{"bash", "-c", "echo hello"}},
			want: model.RoleToolProcess,
		},
		{
			name: "cat is tool process",
			proc: model.ProcessInfo{Name: "cat", Cmdline: []string{"cat", "foo.txt"}},
			want: model.RoleToolProcess,
		},
		{
			name: "claude binary is orchestrator",
			proc: model.ProcessInfo{Name: "claude", Cmdline: []string{"claude"}},
			want: model.RoleOrchestrator,
		},
		{
			name: "claude binary with subagent flag is sub-agent",
			proc: model.ProcessInfo{Name: "claude", Cmdline: []string{"claude", "--subagent"}},
			want: model.RoleSubAgent,
		},
		// node with claude in argv no longer gets special treatment
		// (native claude binary is the expected entrypoint)
		{
			name: "plain node is unknown",
			proc: model.ProcessInfo{Name: "node", Cmdline: []string{"node", "script.js"}},
			want: model.RoleUnknown,
		},
		{
			name: "node running aidex is MCP server",
			proc: model.ProcessInfo{Name: "node", Cmdline: []string{"node", "/opt/homebrew/bin/aidex"}},
			want: model.RoleMCPServer,
		},
		{
			name: "gopls-mcp is MCP server",
			proc: model.ProcessInfo{Name: "gopls-mcp", Cmdline: []string{"gopls-mcp"}},
			want: model.RoleMCPServer,
		},
		{
			name: "caffeinate is unknown (hidden from tree separately)",
			proc: model.ProcessInfo{Name: "caffeinate", Cmdline: []string{"caffeinate", "-i", "-t", "300"}},
			want: model.RoleUnknown,
		},
		{
			name: "zsh shell wrapper is tool process",
			proc: model.ProcessInfo{Name: "zsh", Cmdline: []string{"/bin/zsh", "-c", "source /Users/x/.claude/shell-snapshots/snap.sh && eval 'cat foo.txt'"}},
			want: model.RoleToolProcess,
		},
		{
			name: "unknown process",
			proc: model.ProcessInfo{Name: "obscure-binary", Cmdline: []string{"obscure-binary"}},
			want: model.RoleUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cc.ClassifyProcess(&tt.proc)
			if got != tt.want {
				t.Errorf("ClassifyProcess(%s): got %v, want %v", tt.proc.Name, got, tt.want)
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
		{"src/main.go", true},
		{"node_modules/chalk/index.js", false},
		{"/usr/lib/libSystem.dylib", false},
		{"/System/Library/Frameworks/foo", false},
		{"/tmp/something", false},
		{"CLAUDE.md", true},
		{"internal/model/types.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := cc.FileAccessFilter(tt.path)
			if got != tt.want {
				t.Errorf("FileAccessFilter(%s): got %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractToolFromWrapper(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    string
	}{
		{
			name:    "claude shell wrapper with eval",
			cmdline: []string{"/bin/zsh", "-c", "source /Users/x/.claude/shell-snapshots/snap.sh 2>/dev/null || true && eval 'cat /home/user/file.txt' < /dev/null"},
			want:    "cat /home/user/file.txt",
		},
		{
			name:    "not a wrapper",
			cmdline: []string{"zsh", "-c", "echo hello"},
			want:    "",
		},
		{
			name:    "not shell",
			cmdline: []string{"cat", "foo.txt"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractToolFromWrapper(tt.cmdline)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
