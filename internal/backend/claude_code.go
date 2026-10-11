package backend

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/map588/clanktop/internal/model"
)

// Path prefixes to filter out from the open-files view.
var filteredPathPrefixes = []string{
	"/usr/lib/",
	"/usr/local/lib/",
	"/usr/share/",
	"/System/",
	"/Library/",
	"/opt/homebrew/",
	"/private/var/",
	"/tmp/",
	"/var/folders/",
	"/dev/",
}

// Interpreters and package runners. The label of such a process is its first
// argument that is not a flag or a runner verb.
var launchers = map[string]bool{
	"node": true, "python": true, "python3": true, "bun": true, "deno": true,
	"npx": true, "uvx": true, "bunx": true, "npm": true, "pnpm": true, "uv": true,
}

var launcherVerbs = map[string]bool{"exec": true, "run": true, "x": true, "dlx": true, "tool": true}

// evalPattern matches eval '<command>' in a Claude Code shell wrapper.
// Claude Code writes each single quote in the command as '"'"'.
var evalPattern = regexp.MustCompile(`eval '((?:[^']|'"'"')*)'`)

type ClaudeCode struct {
	home string
}

func NewClaudeCode(home string) *ClaudeCode {
	return &ClaudeCode{home: home}
}

func (c *ClaudeCode) Name() string {
	return "claude-code"
}

func (c *ClaudeCode) Sessions() []model.Session {
	return ListSessions(filepath.Join(c.home, ".claude", "sessions"))
}

// LogPath finds ~/.claude/projects/*/<sessionID>.jsonl. Claude Code names the
// project directory after the cwd with each non-alphanumeric character replaced
// by '-'. A glob avoids a dependency on that encoding.
func (c *ClaudeCode) LogPath(sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(c.home, ".claude", "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// SubAgentLogs finds <project dir>/<sessionID>/subagents/agent-*.jsonl.
func (c *ClaudeCode) SubAgentLogs(logPath, sessionID string) []string {
	if logPath == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(logPath), sessionID, "subagents", "agent-*.jsonl"))
	return matches
}

// ClassifyChild: the agent runs Bash tool calls, hooks and the status line
// through a shell. Every other child is a long-lived MCP or LSP server.
func (c *ClaudeCode) ClassifyChild(proc *model.ProcessInfo) model.ProcessRole {
	if isShell(cmdBase(proc)) {
		return model.RoleToolProcess
	}
	return model.RoleMCPServer
}

func (c *ClaudeCode) Hidden(proc *model.ProcessInfo) bool {
	return cmdBase(proc) == "caffeinate"
}

// HostsSessions is true for the Claude Code daemon ("claude daemon run"). The
// first session that needs it starts it. It then hosts the background
// sessions of all sessions.
func (c *ClaudeCode) HostsSessions(proc *model.ProcessInfo) bool {
	return cmdBase(proc) == "claude" && len(proc.Cmdline) > 1 && proc.Cmdline[1] == "daemon"
}

func (c *ClaudeCode) Label(proc *model.ProcessInfo) string {
	if cmd := ExtractToolFromWrapper(proc.Cmdline); cmd != "" {
		first, _, _ := strings.Cut(cmd, "\n")
		return first
	}
	base := cmdBase(proc)
	if launchers[base] {
		for _, arg := range proc.Cmdline[1:] {
			if strings.HasPrefix(arg, "-") || launcherVerbs[arg] {
				continue
			}
			return filepath.Base(arg)
		}
	}
	return base
}

func (c *ClaudeCode) FileAccessFilter(path string) bool {
	for _, prefix := range filteredPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return !strings.Contains(path, "node_modules")
}

// cmdBase is the base name of the executable. It uses the cmdline when it is
// known, else the ps name.
func cmdBase(proc *model.ProcessInfo) string {
	if len(proc.Cmdline) > 0 {
		return filepath.Base(proc.Cmdline[0])
	}
	return filepath.Base(proc.Name)
}

func isShell(base string) bool {
	return base == "zsh" || base == "bash" || base == "sh"
}

// ExtractToolFromWrapper parses a Claude Code zsh wrapper command to extract
// the actual tool command being run inside eval '...'.
// Returns the extracted command string, or empty if not a wrapper.
func ExtractToolFromWrapper(cmdline []string) string {
	if len(cmdline) < 3 || !isShell(filepath.Base(cmdline[0])) {
		return ""
	}
	cmdStr := strings.Join(cmdline, " ")
	if !strings.Contains(cmdStr, "shell-snapshots") {
		return ""
	}
	matches := evalPattern.FindStringSubmatch(cmdStr)
	if len(matches) >= 2 {
		return strings.ReplaceAll(matches[1], `'"'"'`, "'")
	}
	return ""
}
