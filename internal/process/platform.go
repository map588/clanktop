package process

import (
	"context"
	"errors"
	"time"

	"github.com/map588/clanktop/internal/model"
)

// ErrNotAvailable is returned when the platform's event watcher
// cannot be used (missing root, SIP, no CAP_NET_ADMIN, etc.).
var ErrNotAvailable = errors.New("process event watcher not available")

// ProcEntry is a platform-independent snapshot of a single process.
// Populated by ScanProcesses() on each platform.
type ProcEntry struct {
	PID     int32
	PPID    int32
	RSS     uint64
	Comm    string   // short process name
	Cmdline []string // full argv
	State   string
	StartTime time.Time
}

// ProcWatcher delivers real-time process fork/exec/exit events.
// Implementations: kqueue (macOS), netlink CN_PROC (Linux).
// Both require elevated privileges; callers must handle ErrNotAvailable.
type ProcWatcher interface {
	Events() <-chan ProcEvent
	Run(ctx context.Context)
}

// ProcEvent represents a process lifecycle event from the watcher.
type ProcEvent struct {
	PID       int32
	ParentPID int32
	Type      string // "fork", "exec", "exit"
	Time      time.Time
	Info      *model.ProcessInfo
}
