//go:build !darwin

package process

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/map588/clanktop/internal/debug"
	"github.com/map588/clanktop/internal/model"
)

// Netlink / proc connector constants from linux/connector.h and linux/cn_proc.h
const (
	_NETLINK_CONNECTOR = 11
	_CN_IDX_PROC       = 1
	_CN_VAL_PROC       = 1

	// proc_cn_mcast_op
	_PROC_CN_MCAST_LISTEN = 1
	_PROC_CN_MCAST_IGNORE = 2

	// proc_event.what values
	_PROC_EVENT_FORK = 0x00000001
	_PROC_EVENT_EXEC = 0x00000002
	_PROC_EVENT_EXIT = 0x80000000
)

// cnMsg is the connector message header (struct cn_msg).
type cnMsg struct {
	ID    cbID
	Seq   uint32
	Ack   uint32
	Len   uint16
	Flags uint16
}

type cbID struct {
	Idx uint32
	Val uint32
}

// procEvent header (first 12 bytes after cn_msg payload start).
type procEventHeader struct {
	What      uint32
	CPU       uint32
	Timestamp uint64
}

type forkProcEvent struct {
	ParentPID  uint32
	ParentTGID uint32
	ChildPID   uint32
	ChildTGID  uint32
}

type execProcEvent struct {
	ProcessPID  uint32
	ProcessTGID uint32
}

type exitProcEvent struct {
	ProcessPID  uint32
	ProcessTGID uint32
	ExitCode    uint32
	ExitSignal  uint32
}

// NetlinkWatcher uses the netlink proc connector to receive fork/exec/exit events.
type NetlinkWatcher struct {
	rootPID    int32
	sock       int
	events     chan ProcEvent
	knownPIDs  map[int32]bool // subtree tracking
	pidLock    sync.Mutex
}

// NewProcWatcher creates a netlink-based process watcher.
// Returns ErrNotAvailable if not running as root or lacking CAP_NET_ADMIN.
func NewProcWatcher(rootPID int32) (ProcWatcher, error) {
	sock, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, _NETLINK_CONNECTOR)
	if err != nil {
		return nil, fmt.Errorf("%w: netlink socket: %v (requires root or CAP_NET_ADMIN)", ErrNotAvailable, err)
	}

	addr := &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Groups: _CN_IDX_PROC,
		Pid:    uint32(os.Getpid()),
	}
	if err := syscall.Bind(sock, addr); err != nil {
		syscall.Close(sock)
		return nil, fmt.Errorf("%w: netlink bind: %v", ErrNotAvailable, err)
	}

	// Send PROC_CN_MCAST_LISTEN to enable events
	if err := sendMcastListen(sock); err != nil {
		syscall.Close(sock)
		return nil, fmt.Errorf("%w: mcast listen: %v", ErrNotAvailable, err)
	}

	// Seed known PIDs from current process tree
	known := make(map[int32]bool)
	seedSubtree(rootPID, known)

	w := &NetlinkWatcher{
		rootPID:   rootPID,
		sock:      sock,
		events:    make(chan ProcEvent, 256),
		knownPIDs: known,
	}

	return w, nil
}

func sendMcastListen(sock int) error {
	// Build: nlmsghdr + cn_msg + uint32(PROC_CN_MCAST_LISTEN)
	nlHdrSize := 16 // sizeof(nlmsghdr)
	cnMsgSize := int(unsafe.Sizeof(cnMsg{}))
	payloadSize := 4 // uint32
	totalSize := nlHdrSize + cnMsgSize + payloadSize

	buf := make([]byte, totalSize)

	// nlmsghdr
	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalSize)) // nlmsg_len
	binary.LittleEndian.PutUint16(buf[4:6], syscall.NLMSG_DONE) // nlmsg_type
	binary.LittleEndian.PutUint16(buf[6:8], 0)                  // nlmsg_flags
	binary.LittleEndian.PutUint32(buf[8:12], 0)                 // nlmsg_seq
	binary.LittleEndian.PutUint32(buf[12:16], uint32(os.Getpid())) // nlmsg_pid

	// cn_msg
	off := nlHdrSize
	binary.LittleEndian.PutUint32(buf[off:off+4], _CN_IDX_PROC)   // id.idx
	binary.LittleEndian.PutUint32(buf[off+4:off+8], _CN_VAL_PROC) // id.val
	binary.LittleEndian.PutUint32(buf[off+8:off+12], 0)           // seq
	binary.LittleEndian.PutUint32(buf[off+12:off+16], 0)          // ack
	binary.LittleEndian.PutUint16(buf[off+16:off+18], uint16(payloadSize)) // len
	binary.LittleEndian.PutUint16(buf[off+18:off+20], 0)          // flags

	// payload: PROC_CN_MCAST_LISTEN
	off += cnMsgSize
	binary.LittleEndian.PutUint32(buf[off:off+4], _PROC_CN_MCAST_LISTEN)

	return syscall.Sendto(sock, buf, 0, &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK,
		Pid:    0, // kernel
	})
}

// seedSubtree populates the known PID set with the root and all its descendants.
func seedSubtree(rootPID int32, known map[int32]bool) {
	entries, err := ScanProcesses()
	if err != nil {
		known[rootPID] = true
		return
	}

	children := make(map[int32][]int32)
	for _, e := range entries {
		children[e.PPID] = append(children[e.PPID], e.PID)
	}

	var walk func(pid int32)
	walk = func(pid int32) {
		known[pid] = true
		for _, child := range children[pid] {
			walk(child)
		}
	}
	walk(rootPID)
}

func (w *NetlinkWatcher) Events() <-chan ProcEvent {
	return w.events
}

func (w *NetlinkWatcher) Run(ctx context.Context) {
	defer syscall.Close(w.sock)
	defer close(w.events)

	buf := make([]byte, 4096)

	// Set read timeout so we can check ctx.Done()
	tv := syscall.Timeval{Sec: 0, Usec: 200000} // 200ms
	syscall.SetsockoptTimeval(w.sock, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, _, err := syscall.Recvfrom(w.sock, buf, 0)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			if err == syscall.EINTR {
				continue
			}
			debug.Log("NETLINK recv err: %v", err)
			continue
		}

		if n < 16 {
			continue
		}

		w.handleMessage(buf[:n])
	}
}

func (w *NetlinkWatcher) handleMessage(buf []byte) {
	nlHdrSize := 16
	cnMsgSize := int(unsafe.Sizeof(cnMsg{}))
	hdrSize := int(unsafe.Sizeof(procEventHeader{}))
	minSize := nlHdrSize + cnMsgSize + hdrSize

	if len(buf) < minSize {
		return
	}

	// Skip nlmsghdr and cn_msg to get to proc_event
	off := nlHdrSize + cnMsgSize

	var hdr procEventHeader
	hdr.What = binary.LittleEndian.Uint32(buf[off : off+4])
	off += hdrSize

	now := time.Now()

	switch hdr.What {
	case _PROC_EVENT_FORK:
		if len(buf) < off+int(unsafe.Sizeof(forkProcEvent{})) {
			return
		}
		var ev forkProcEvent
		ev.ParentPID = binary.LittleEndian.Uint32(buf[off : off+4])
		ev.ParentTGID = binary.LittleEndian.Uint32(buf[off+4 : off+8])
		ev.ChildPID = binary.LittleEndian.Uint32(buf[off+8 : off+12])
		ev.ChildTGID = binary.LittleEndian.Uint32(buf[off+12 : off+16])

		parentPID := int32(ev.ParentTGID)
		childPID := int32(ev.ChildTGID)

		w.pidLock.Lock()
		inSubtree := w.knownPIDs[parentPID]
		if inSubtree {
			w.knownPIDs[childPID] = true
		}
		w.pidLock.Unlock()

		if inSubtree {
			debug.Log("NETLINK FORK parent=%d child=%d", parentPID, childPID)
			info := procEntryToInfo(childPID)
			w.events <- ProcEvent{
				PID:       childPID,
				ParentPID: parentPID,
				Type:      "fork",
				Time:      now,
				Info:      info,
			}
		}

	case _PROC_EVENT_EXEC:
		if len(buf) < off+int(unsafe.Sizeof(execProcEvent{})) {
			return
		}
		var ev execProcEvent
		ev.ProcessPID = binary.LittleEndian.Uint32(buf[off : off+4])
		ev.ProcessTGID = binary.LittleEndian.Uint32(buf[off+4 : off+8])
		pid := int32(ev.ProcessTGID)

		w.pidLock.Lock()
		inSubtree := w.knownPIDs[pid]
		w.pidLock.Unlock()

		if inSubtree {
			debug.Log("NETLINK EXEC pid=%d", pid)
			info := procEntryToInfo(pid)
			w.events <- ProcEvent{
				PID:  pid,
				Type: "exec",
				Time: now,
				Info: info,
			}
		}

	case _PROC_EVENT_EXIT:
		if len(buf) < off+int(unsafe.Sizeof(exitProcEvent{})) {
			return
		}
		var ev exitProcEvent
		ev.ProcessPID = binary.LittleEndian.Uint32(buf[off : off+4])
		ev.ProcessTGID = binary.LittleEndian.Uint32(buf[off+4 : off+8])
		pid := int32(ev.ProcessTGID)

		w.pidLock.Lock()
		inSubtree := w.knownPIDs[pid]
		if inSubtree {
			delete(w.knownPIDs, pid)
		}
		w.pidLock.Unlock()

		if inSubtree {
			debug.Log("NETLINK EXIT pid=%d", pid)
			w.events <- ProcEvent{
				PID:  pid,
				Type: "exit",
				Time: now,
			}
		}
	}
}

// procEntryToInfo reads /proc/<pid> and converts to ProcessInfo.
func procEntryToInfo(pid int32) *model.ProcessInfo {
	e, err := readProcEntry(pid)
	if err != nil {
		return nil
	}
	name := e.Comm
	if len(e.Cmdline) > 0 {
		if base := filepath.Base(e.Cmdline[0]); base != "" && base != "." {
			name = base
		}
	}
	return &model.ProcessInfo{
		PID:       pid,
		PPID:      e.PPID,
		Name:      name,
		Cmdline:   e.Cmdline,
		RSS:       e.RSS,
		State:     e.State,
		StartTime: e.StartTime,
	}
}

func isTimeout(err error) bool {
	if errno, ok := err.(syscall.Errno); ok {
		return errno == syscall.EAGAIN || errno == syscall.EWOULDBLOCK
	}
	return false
}
