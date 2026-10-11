package model

import (
	"slices"
	"time"
)

type ProcessRole int

const (
	RoleUnknown      ProcessRole = iota
	RoleOrchestrator             // The agent process of a session
	RoleToolProcess              // A Bash tool shell and its descendants
	RoleMCPServer                // Any other child of the agent (MCP or LSP server) and its descendants
)

func (r ProcessRole) String() string {
	switch r {
	case RoleOrchestrator:
		return "agent"
	case RoleToolProcess:
		return "tool"
	case RoleMCPServer:
		return "service"
	default:
		return "unknown"
	}
}

type ProcessInfo struct {
	PID        int32
	PPID       int32
	Name       string // Display label
	Cmdline    []string
	Role       ProcessRole
	CPUPercent float64
	RSS        uint64 // Resident memory in bytes
	State      string // Z (zombie), T (stopped), or ""
	Start      time.Time
	ExitTime   *time.Time // nil while the process runs
	Children   []*ProcessInfo
}

// Session is one live Claude Code session from ~/.claude/sessions/<pid>.json.
type Session struct {
	PID       int32     `json:"pid"`
	SessionID string    `json:"sessionId"`
	Cwd       string    `json:"cwd"`
	Kind      string    `json:"kind"`   // interactive or bg
	Status    string    `json:"status"` // busy or idle
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type FileOp int

const (
	FileOpRead FileOp = iota
	FileOpWrite
	FileOpEdit
	FileOpSearch
)

func (op FileOp) String() string {
	switch op {
	case FileOpRead:
		return "read"
	case FileOpWrite:
		return "write"
	case FileOpEdit:
		return "edit"
	case FileOpSearch:
		return "search"
	default:
		return "unknown"
	}
}

// Tag is the one-letter label of the operation in the TUI.
func (op FileOp) Tag() string {
	return [...]string{"R", "W", "E", "S"}[op]
}

// ToolCall is one tool_use record from a session log, with its result if it arrived.
type ToolCall struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Summary string     `json:"summary"`
	Path    string     `json:"path,omitempty"` // File path for file tools
	Op      FileOp     `json:"op"`             // Valid when Path is set
	Input   string     `json:"-"`              // Raw input JSON, truncated, for the detail view
	Start   time.Time  `json:"start"`
	End     *time.Time `json:"end,omitempty"` // nil until the tool_result arrives
	IsError bool       `json:"isError,omitempty"`
}

// SubAgent is an in-process subagent of a session, read from its own log.
type SubAgent struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	ToolUseID   string     `json:"toolUseId"`
	Start       time.Time  `json:"start"`
	Running     bool       `json:"running"`
	Calls       []ToolCall `json:"calls"`
}

// OpenFile is a regular file that a process holds open now.
type OpenFile struct {
	PID  int32  `json:"pid"`
	Proc string `json:"proc"`
	Path string `json:"path"`
}

// AgentView is the state of one session at one time.
type AgentView struct {
	Session
	LogPath    string       `json:"logPath"`
	Title      string       `json:"title"`
	LastPrompt string       `json:"lastPrompt"`
	Calls      []ToolCall   `json:"calls"`
	SubAgents  []SubAgent   `json:"subAgents"`
	Tree       *ProcessInfo `json:"tree"`
	OpenFiles  []OpenFile   `json:"openFiles"`
	Alerts     []string     `json:"alerts"`
}

// FileTouch is the summary of all tool calls on one path.
type FileTouch struct {
	Path  string
	Ops   map[FileOp]bool
	Count int
	Last  time.Time
}

// FilesTouched groups calls by path. The most recent path is first.
func FilesTouched(calls []ToolCall) []FileTouch {
	index := map[string]int{}
	var out []FileTouch
	for _, c := range calls {
		if c.Path == "" {
			continue
		}
		i, ok := index[c.Path]
		if !ok {
			i = len(out)
			index[c.Path] = i
			out = append(out, FileTouch{Path: c.Path, Ops: map[FileOp]bool{}})
		}
		out[i].Ops[c.Op] = true
		out[i].Count++
		if c.Start.After(out[i].Last) {
			out[i].Last = c.Start
		}
	}
	slices.SortStableFunc(out, func(a, b FileTouch) int { return b.Last.Compare(a.Last) })
	return out
}
