package backend

import "github.com/map588/clanktop/internal/model"

// LogSource describes a log file to tail.
type LogSource struct {
	Path   string
	Format string // "json", "text", etc.
}

// ClientBackend encapsulates all client-specific knowledge.
type ClientBackend interface {
	Name() string
	FindRootProcess() (int32, error)
	ClassifyProcess(proc *model.ProcessInfo) model.ProcessRole
	LogSources(rootPID int32) []LogSource
	ParseLogLine(line string) *AgentEvent
	FileAccessFilter(path string) bool
}

// AgentEvent is a structured event parsed from a log line.
type AgentEvent struct {
	Type    string // "tool_call", "agent_spawn", "error", etc.
	AgentID string
	Data    map[string]string
}
