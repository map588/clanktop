//go:build !linux

package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/map588/clanktop/internal/debug"
	"github.com/map588/clanktop/internal/model"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// KqueueWatcher uses kqueue EVFILT_PROC to get real-time process events.
// Requires root privileges; SIP blocks EVFILT_PROC for non-root on macOS.
type KqueueWatcher struct {
	rootPID   int32
	kq        int
	events    chan ProcEvent
	watching  map[int32]bool
	procCache map[int32]*model.ProcessInfo
}

// NewProcWatcher creates a kqueue-based process watcher.
// Returns ErrNotAvailable if not running as root or if SIP blocks kqueue EVFILT_PROC.
func NewProcWatcher(rootPID int32) (ProcWatcher, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("%w: not running as root (euid=%d)", ErrNotAvailable, os.Geteuid())
	}

	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("%w: kqueue: %v", ErrNotAvailable, err)
	}

	// Probe: try attaching EVFILT_PROC to the target PID.
	// SIP blocks this for processes you don't own, even as root.
	probe := syscall.Kevent_t{
		Ident:  uint64(rootPID),
		Filter: syscall.EVFILT_PROC,
		Flags:  syscall.EV_ADD | syscall.EV_ENABLE,
		Fflags: syscall.NOTE_EXIT,
	}
	_, err = syscall.Kevent(kq, []syscall.Kevent_t{probe}, nil, nil)
	if err != nil {
		syscall.Close(kq)
		return nil, fmt.Errorf("%w: SIP blocks EVFILT_PROC on PID %d: %v", ErrNotAvailable, rootPID, err)
	}
	// Probe succeeded — upgrade to full tracking (remove probe, watchPID adds NOTE_FORK etc.)
	probe.Flags = syscall.EV_DELETE
	syscall.Kevent(kq, []syscall.Kevent_t{probe}, nil, nil)

	w := &KqueueWatcher{
		rootPID:   rootPID,
		kq:        kq,
		events:    make(chan ProcEvent, 256),
		watching:  make(map[int32]bool),
		procCache: make(map[int32]*model.ProcessInfo),
	}

	if err := w.watchPID(rootPID); err != nil {
		syscall.Close(kq)
		return nil, fmt.Errorf("%w: watch root %d: %v", ErrNotAvailable, rootPID, err)
	}

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
}

func (w *KqueueWatcher) Events() <-chan ProcEvent {
	return w.events
}

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
				debug.Log("KQUEUE FORK parent=%d", pid)
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

			if ev.Fflags&noteTrackerr() != 0 {
				debug.Log("KQUEUE TRACKERR pid=%d", pid)
			}
		}
	}
}

func (w *KqueueWatcher) detectNewChild(parentPID int32, when time.Time) {
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

	if len(cmdline) > 0 {
		if base := filepath.Base(cmdline[0]); base != "" && base != "." {
			name = base
		}
	}

	return &model.ProcessInfo{
		PID:       pid,
		PPID:      ppid,
		Name:      name,
		Cmdline:   cmdline,
		RSS:       rss,
		State:     stateStr,
		StartTime: time.UnixMilli(createTime),
	}
}

func noteTrackerr() uint32 {
	return 0x00000002
}
