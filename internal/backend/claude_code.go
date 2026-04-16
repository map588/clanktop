package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/map588/clanktop/internal/model"
	"github.com/map588/clanktop/internal/process"
)

// Common tool process names that Claude Code spawns.
var toolProcessNames = map[string]bool{
	"cat": true, "bat": true,
	"grep": true, "rg": true, "ag": true,
	"sed": true, "awk": true,
	"find": true, "fd": true,
	"ls": true, "tree": true,
	"head": true, "tail": true,
	"wc": true, "sort": true, "uniq": true,
	"python": true, "python3": true,
	"git": true, "gh": true,
	"curl": true, "wget": true,
	"mkdir": true, "cp": true, "mv": true, "rm": true,
	"chmod": true, "chown": true,
	"touch": true, "tee": true,
	"go": true,
}

// Infra process names (long-lived system utilities).
var infraProcessNames = map[string]bool{
	"fswatch": true, "watchman": true,
}


// MCP server indicators in process name or cmdline.
var mcpServerPatterns = []string{
	"aidex", "mcp-server", "mcp_server", "-mcp",
	"modelcontextprotocol",
}


// Matches eval '<command>' in shell wrapper commands
var evalPattern = regexp.MustCompile(`eval '([^']*)'`)

type ClaudeCode struct {
	mcpNames map[string]bool // command names from config
}

func NewClaudeCode(projectDir string) *ClaudeCode {
	return &ClaudeCode{
		mcpNames: LoadMCPServerNames(projectDir),
	}
}

func (c *ClaudeCode) Name() string {
	return "claude-code"
}

func (c *ClaudeCode) FindRootProcess() (int32, error) {
	entries, err := process.ScanProcesses()
	if err != nil {
		return 0, fmt.Errorf("listing processes: %w", err)
	}

	var candidates []int32
	for _, e := range entries {
		base := e.Comm
		if len(e.Cmdline) > 0 {
			base = filepath.Base(e.Cmdline[0])
		}
		if base == "claude" {
			candidates = append(candidates, e.PID)
		}
	}

	switch len(candidates) {
	case 0:
		return 0, fmt.Errorf("no Claude Code process found")
	case 1:
		return candidates[0], nil
	default:
		return candidates[0], nil
	}
}

func (c *ClaudeCode) ClassifyProcess(proc *model.ProcessInfo) model.ProcessRole {
	cmdStr := strings.Join(proc.Cmdline, " ")

	var cmdBase string
	if len(proc.Cmdline) > 0 {
		cmdBase = strings.ToLower(filepath.Base(proc.Cmdline[0]))
	}

	// Native claude binary
	if cmdBase == "claude" {
		if strings.Contains(cmdStr, "--subagent") || strings.Contains(cmdStr, "subagent") {
			return model.RoleSubAgent
		}
		return model.RoleOrchestrator
	}

	// MCP servers — check config-loaded names first
	if c.mcpNames[cmdBase] {
		return model.RoleMCPServer
	}
	// Check all cmdline args against MCP names (catches npx/uvx <server>)
	for _, arg := range proc.Cmdline {
		if c.mcpNames[filepath.Base(arg)] {
			return model.RoleMCPServer
		}
	}
	// Fallback pattern matching
	cmdLower := strings.ToLower(cmdStr)
	for _, pattern := range mcpServerPatterns {
		if strings.Contains(cmdLower, pattern) {
			return model.RoleMCPServer
		}
	}

	// Shell processes — tool process (wrapper or standalone)
	if cmdBase == "zsh" || cmdBase == "bash" || cmdBase == "sh" {
		return model.RoleToolProcess
	}

	// Known infra processes
	if infraProcessNames[cmdBase] {
		return model.RoleInfra
	}

	// Known tool processes
	if toolProcessNames[cmdBase] {
		return model.RoleToolProcess
	}

	return model.RoleUnknown
}

// isClaudeShellWrapper detects zsh -c "source .../shell-snapshots/..." wrapper commands
func isClaudeShellWrapper(cmdStr string) bool {
	return strings.Contains(cmdStr, "shell-snapshots") || strings.Contains(cmdStr, "claude")
}

// ExtractToolFromWrapper parses a Claude Code zsh wrapper command to extract
// the actual tool command being run inside eval '...'.
// Returns the extracted command string, or empty if not a wrapper.
func ExtractToolFromWrapper(cmdline []string) string {
	if len(cmdline) < 3 {
		return ""
	}
	base := filepath.Base(cmdline[0])
	if base != "zsh" && base != "bash" && base != "sh" {
		return ""
	}

	cmdStr := strings.Join(cmdline, " ")
	if !strings.Contains(cmdStr, "shell-snapshots") {
		return ""
	}

	// Extract the eval '...' portion
	matches := evalPattern.FindStringSubmatch(cmdStr)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

func (c *ClaudeCode) LogSources(rootPID int32) []LogSource {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var sources []LogSource
	logDir := filepath.Join(home, ".claude", "logs")
	if entries, err := os.ReadDir(logDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				sources = append(sources, LogSource{
					Path:   filepath.Join(logDir, e.Name()),
					Format: "text",
				})
			}
		}
	}

	return sources
}

func (c *ClaudeCode) ParseLogLine(line string) *AgentEvent {
	return nil
}

func (c *ClaudeCode) FileAccessFilter(path string) bool {
	for _, prefix := range filteredPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	if strings.Contains(path, "node_modules") {
		return false
	}
	return true
}
