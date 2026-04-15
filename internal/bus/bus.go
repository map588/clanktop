package bus

import "github.com/map588/clanktop/internal/model"

const bufferSize = 64

type ProcessTreeEvent struct {
	Tree      *model.ProcessInfo   // Root of the process tree
	AllProcs  []*model.ProcessInfo // Flat list of all processes
	NewPIDs   []int32
	ExitedPIDs []int32
}

type AgentEvent struct {
	Type    string // "spawn", "tool_call", "error", etc.
	AgentID string
	Data    map[string]string
}

type AlertEvent struct {
	Message  string
	Severity string // "warning", "critical"
}

// ProcLifecycleEvent from kqueue — real-time fork/exec/exit.
type ProcLifecycleEvent struct {
	PID       int32
	ParentPID int32
	Type      string             // "fork", "exec", "exit"
	Info      *model.ProcessInfo // may be nil for exit
}

type EventBus struct {
	ProcessTree    chan ProcessTreeEvent
	ProcLifecycle  chan ProcLifecycleEvent
	FileEvents     chan model.FileEvent
	ToolCalls      chan model.ToolCall
	AgentEvents    chan AgentEvent
	Alerts         chan AlertEvent
	done           chan struct{}
}

func New() *EventBus {
	return &EventBus{
		ProcessTree:   make(chan ProcessTreeEvent, bufferSize),
		ProcLifecycle: make(chan ProcLifecycleEvent, bufferSize),
		FileEvents:    make(chan model.FileEvent, bufferSize),
		ToolCalls:     make(chan model.ToolCall, bufferSize),
		AgentEvents:   make(chan AgentEvent, bufferSize),
		Alerts:        make(chan AlertEvent, bufferSize),
		done:          make(chan struct{}),
	}
}

// Send attempts a non-blocking send on the channel. Drops on full buffer.
func Send[T any](ch chan T, event T) {
	select {
	case ch <- event:
	default:
	}
}

func (b *EventBus) Shutdown() {
	close(b.done)
	close(b.ProcessTree)
	close(b.ProcLifecycle)
	close(b.FileEvents)
	close(b.ToolCalls)
	close(b.AgentEvents)
	close(b.Alerts)
}

func (b *EventBus) Done() <-chan struct{} {
	return b.done
}
