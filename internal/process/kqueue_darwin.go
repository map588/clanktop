package process

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/map588/clanktop/internal/debug"
	"github.com/map588/clanktop/internal/model"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// ProcEvent represents a process lifecycle event.
type ProcEvent struct {
	PID       int32
	ParentPID int32
	Type      string // "fork", "exec", "exit"
	Time      time.Time
	Info      *model.ProcessInfo // populated on fork/exec
}

// KqueueWatcher uses kqueue EVFILT_PROC to get real-time process events.
type KqueueWatcher struct {
	rootPID   int32
	kq        int
	events    chan ProcEvent
	watching  map[int32]bool
	procCache map[int32]*model.ProcessInfo // cache info from fork/exec for exit lookup
}

func NewKqueueWatcher(rootPID int32) (*KqueueWatcher, error) {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("kqueue: %w", err)
	}

	w := &KqueueWatcher{
		rootPID:   rootPID,
		kq:        kq,
		events:    make(chan ProcEvent, 256),
		watching:  make(map[int32]bool),
		procCache: make(map[int32]*model.ProcessInfo),
	}

	// Watch root process
	if err := w.watchPID(rootPID); err != nil {
		syscall.Close(kq)
		return nil, fmt.Errorf("watch root %d: %w", rootPID, err)
	}

	// Watch existing children too
	w.watchExistingChildren(rootPID)

	return w, nil
}

func (w *KqueueWatcher) watchPID(pid int32) error {
	if w.watching[pid] {
		return nil
	}

	event := syscall.Kevent_t{
		Ident:  uint64(pid),
		Filter: syscall.EVFILT_PROC,
		Flags:  syscall.EV_ADD | syscall.EV_ENABLE,
		Fflags: syscall.NOTE_FORK | syscall.NOTE_EXEC | syscall.NOTE_EXIT | syscall.NOTE_TRACK,
	}

	_, err := syscall.Kevent(w.kq, []syscall.Kevent_t{event}, nil, nil)
	if err != nil {
		return err
	}

	w.watching[pid] = true
	debug.Log("KQUEUE watching PID=%d", pid)
	return nil
}

func (w *KqueueWatcher) watchExistingChildren(pid int32) {
	p, err := gopsutil.NewProcess(pid)
	if err != nil {
		return
	}
	// gopsutil Children() broken on macOS, enumerate manually
	procs, _ := gopsutil.Processes()
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			continue
		}
		if ppid == pid {
			w.watchPID(proc.Pid)
			w.watchExistingChildren(proc.Pid)
		}
	}
	_ = p
}

// Events returns the event channel.
func (w *KqueueWatcher) Events() <-chan ProcEvent {
	return w.events
}

// Run listens for kqueue events until context cancelled.
func (w *KqueueWatcher) Run(ctx context.Context) {
	defer syscall.Close(w.kq)
	defer close(w.events)

	eventBuf := make([]syscall.Kevent_t, 32)
	timeout := syscall.NsecToTimespec(int64(200 * time.Millisecond))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, err := syscall.Kevent(w.kq, nil, eventBuf, &timeout)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			debug.Log("KQUEUE err: %v", err)
			continue
		}

		now := time.Now()
		for i := 0; i < n; i++ {
			ev := eventBuf[i]
			pid := int32(ev.Ident)

			if ev.Fflags&syscall.NOTE_FORK != 0 {
				// Child PID comes from NOTE_CHILD on tracked events
				// For NOTE_FORK, the ident is the PARENT that forked
				debug.Log("KQUEUE FORK parent=%d", pid)

				// Find new child by scanning children of this pid
				go w.detectNewChild(pid, now)
			}

			if ev.Fflags&syscall.NOTE_EXEC != 0 {
				debug.Log("KQUEUE EXEC pid=%d", pid)
				info := getProcessInfo(pid)
				if info != nil {
					w.procCache[pid] = info
				}
				w.events <- ProcEvent{
					PID:  pid,
					Type: "exec",
					Time: now,
					Info: info,
				}
			}

			if ev.Fflags&syscall.NOTE_EXIT != 0 {
				debug.Log("KQUEUE EXIT pid=%d", pid)
				// Use cached info from fork/exec
				cached := w.procCache[pid]
				w.events <- ProcEvent{
					PID:  pid,
					Type: "exit",
					Time: now,
					Info: cached,
				}
				delete(w.watching, pid)
				delete(w.procCache, pid)
			}

			// NOTE_CHILD: auto-tracked child process
			if ev.Fflags&syscall.NOTE_CHILD != 0 {
				childPID := int32(ev.Ident)
				debug.Log("KQUEUE CHILD pid=%d", childPID)
				w.watchPID(childPID)
				info := getProcessInfo(childPID)
				if info != nil {
					w.procCache[childPID] = info
					w.events <- ProcEvent{
						PID:  childPID,
						Type: "fork",
						Time: now,
						Info: info,
					}
				}
			}

			// NOTE_TRACKERR: couldn't auto-track
			if ev.Fflags&noteTrackerr() != 0 {
				debug.Log("KQUEUE TRACKERR pid=%d", pid)
			}
		}
	}
}

func (w *KqueueWatcher) detectNewChild(parentPID int32, when time.Time) {
	// Brief delay to let child process start
	time.Sleep(10 * time.Millisecond)

	procs, _ := gopsutil.Processes()
	for _, p := range procs {
		ppid, err := p.Ppid()
		if err != nil || ppid != parentPID {
			continue
		}
		if !w.watching[p.Pid] {
			w.watchPID(p.Pid)
			info := getProcessInfo(p.Pid)
			if info != nil {
				info.PPID = parentPID
				w.procCache[p.Pid] = info
				w.events <- ProcEvent{
					PID:       p.Pid,
					ParentPID: parentPID,
					Type:      "fork",
					Time:      when,
					Info:      info,
				}
			}
		}
	}
}

func getProcessInfo(pid int32) *model.ProcessInfo {
	p, err := gopsutil.NewProcess(pid)
	if err != nil {
		return nil
	}
	ppid, _ := p.Ppid()
	name, _ := p.Name()
	cmdline, _ := p.CmdlineSlice()
	cpuPct, _ := p.CPUPercent()
	memInfo, _ := p.MemoryInfo()
	var rss uint64
	if memInfo != nil {
		rss = memInfo.RSS
	}
	status, _ := p.Status()
	stateStr := ""
	if len(status) > 0 {
		stateStr = status[0]
	}
	createTime, _ := p.CreateTime()

	// Better display name from cmdline
	displayName := name
	if len(cmdline) > 0 {
		base := filepath.Base(cmdline[0])
		if base != "" && base != "." {
			displayName = base
		}
	}
	_ = displayName
	_ = strings.ToLower // keep import

	return &model.ProcessInfo{
		PID:        pid,
		PPID:       ppid,
		Name:       name,
		Cmdline:    cmdline,
		CPUPercent: cpuPct,
		RSS:        rss,
		State:      stateStr,
		StartTime:  time.UnixMilli(createTime),
	}
}

// noteTrackerr returns NOTE_TRACKERR value.
// Not exported by syscall package on all versions.
func noteTrackerr() uint32 {
	// NOTE_TRACKERR = 0x00000002 on macOS
	return 0x00000002
}

// Ensure unsafe import used (for potential future use)
var _ = unsafe.Sizeof(0)
