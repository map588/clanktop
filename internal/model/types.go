package model

import "time"

type ProcessRole int

const (
	RoleUnknown      ProcessRole = iota
	RoleOrchestrator             // Top-level agent
	RoleSubAgent                 // Nested agent instance
	RoleToolProcess              // Shell, cat, grep, etc.
	RoleRuntime                  // Node.js, Python interpreter, etc.
	RoleInfra                    // System utility (caffeinate, etc.)
	RoleMCPServer                // MCP servers (aidex, gopls-mcp, etc.)
)

func (r ProcessRole) String() string {
	switch r {
	case RoleOrchestrator:
		return "orchestrator"
	case RoleSubAgent:
		return "sub-agent"
	case RoleToolProcess:
		return "tool"
	case RoleRuntime:
		return "runtime"
	case RoleInfra:
		return "infra"
	case RoleMCPServer:
		return "mcp"
	default:
		return "unknown"
	}
}

type ProcessInfo struct {
	PID        int32
	PPID       int32
	Name       string
	Cmdline    []string
	Role       ProcessRole
	AgentID    string
	CPUPercent float64
	RSS        uint64 // Resident memory in bytes
	State      string // Running, Sleeping, Zombie, etc.
	StartTime  time.Time
	ExitTime   *time.Time // nil if still running
	ExitCount  int       // >1 if multiple identical exited processes collapsed
	Children   []*ProcessInfo
}

type AgentNode struct {
	ID         string
	Role       ProcessRole
	PID        int32
	ParentID   string // Parent agent ID (empty for root)
	SpawnTime  time.Time
	Processes  []*ProcessInfo
	FileAccess []FileEvent
	ToolCalls  []ToolCall
}

type FileOp int

const (
	FileOpRead FileOp = iota
	FileOpWrite
	FileOpCreate
	FileOpDelete
)

func (op FileOp) String() string {
	switch op {
	case FileOpRead:
		return "read"
	case FileOpWrite:
		return "write"
	case FileOpCreate:
		return "create"
	case FileOpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

type FileEvent struct {
	Timestamp  time.Time
	AgentID    string
	Path       string
	Operation  FileOp
	Source     string // "argv" or "log"
	ProcessPID int32
}

type ToolCall struct {
	Timestamp time.Time
	AgentID   string
	ToolName  string
	Args      []string
	PID       int32
	Duration  *time.Duration // nil if still running
	ExitCode  *int
}
