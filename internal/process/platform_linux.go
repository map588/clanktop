//go:build !darwin

package process

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var pageSize = uint64(os.Getpagesize())

// ScanProcesses enumerates all processes by reading /proc directly.
// No subprocess fork, no gopsutil dependency.
func ScanProcesses() ([]ProcEntry, error) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("readdir /proc: %w", err)
	}

	entries := make([]ProcEntry, 0, len(dirs)/2)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		pid, err := strconv.ParseInt(d.Name(), 10, 32)
		if err != nil {
			continue // not a PID directory
		}

		e, err := readProcEntry(int32(pid))
		if err != nil {
			continue // process vanished or permission denied
		}
		entries = append(entries, e)
	}

	return entries, nil
}

// readProcEntry reads /proc/<pid>/stat, statm, and cmdline for a single process.
func readProcEntry(pid int32) (ProcEntry, error) {
	pidStr := strconv.FormatInt(int64(pid), 10)
	base := "/proc/" + pidStr

	// /proc/<pid>/stat: "pid (comm) state ppid ..."
	statBytes, err := os.ReadFile(base + "/stat")
	if err != nil {
		return ProcEntry{}, err
	}

	ppid, comm, state, startTicks, err := parseProcStat(statBytes)
	if err != nil {
		return ProcEntry{}, err
	}

	if comm == "<defunct>" || comm == "defunct" {
		return ProcEntry{}, fmt.Errorf("defunct process")
	}

	// /proc/<pid>/statm: "size rss shared ..."
	var rss uint64
	if statmBytes, err := os.ReadFile(base + "/statm"); err == nil {
		rss = parseStatmRSS(statmBytes)
	}

	// /proc/<pid>/cmdline: null-separated argv
	var cmdline []string
	if cmdBytes, err := os.ReadFile(base + "/cmdline"); err == nil && len(cmdBytes) > 0 {
		cmdline = parseCmdline(cmdBytes)
	}

	e := ProcEntry{
		PID:     pid,
		PPID:    ppid,
		RSS:     rss * pageSize,
		Comm:    comm,
		Cmdline: cmdline,
		State:   state,
	}

	// Convert start time from clock ticks to wall time
	if startTicks > 0 {
		e.StartTime = ticksToTime(startTicks)
	}

	return e, nil
}

// parseProcStat parses /proc/<pid>/stat content.
// Format: "pid (comm) state ppid pgrp session tty_nr tpgid flags ... starttime ..."
// comm can contain spaces and parens, so find the last ')' to delimit it.
func parseProcStat(data []byte) (ppid int32, comm string, state string, startTicks uint64, err error) {
	// Find comm field boundaries: first '(' and last ')'
	openParen := bytes.IndexByte(data, '(')
	closeParen := bytes.LastIndexByte(data, ')')
	if openParen < 0 || closeParen < 0 || closeParen <= openParen {
		return 0, "", "", 0, fmt.Errorf("malformed stat")
	}

	comm = string(data[openParen+1 : closeParen])

	// Fields after ")" are space-separated: state ppid pgrp session ...
	rest := strings.Fields(string(data[closeParen+2:]))
	if len(rest) < 2 {
		return 0, "", "", 0, fmt.Errorf("too few fields in stat")
	}

	state = rest[0]
	ppidVal, err := strconv.ParseInt(rest[1], 10, 32)
	if err != nil {
		return 0, "", "", 0, fmt.Errorf("parse ppid: %w", err)
	}

	// starttime is field 22 (0-indexed), which is rest[19] (rest starts at field 3)
	if len(rest) > 19 {
		startTicks, _ = strconv.ParseUint(rest[19], 10, 64)
	}

	return int32(ppidVal), comm, state, startTicks, nil
}

// parseStatmRSS extracts the RSS field (second field) from /proc/<pid>/statm.
func parseStatmRSS(data []byte) uint64 {
	// Format: "size rss shared text lib data dt"
	fields := bytes.Fields(data)
	if len(fields) < 2 {
		return 0
	}
	rss, _ := strconv.ParseUint(string(fields[1]), 10, 64)
	return rss
}

// parseCmdline splits null-separated /proc/<pid>/cmdline into argv.
func parseCmdline(data []byte) []string {
	// Trim trailing null bytes
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = string(p)
	}
	return result
}

// GetProcessCwd returns the working directory for a given PID via /proc/<pid>/cwd symlink.
func GetProcessCwd(pid int32) (string, error) {
	link := fmt.Sprintf("/proc/%d/cwd", pid)
	return os.Readlink(link)
}

// clkTck caches the system clock ticks per second.
var clkTck uint64

func init() {
	// Standard value on Linux. Could read from sysconf(_SC_CLK_TCK) but 100 is
	// the default on virtually all Linux systems.
	clkTck = 100
}

// ticksToTime converts a start time in clock ticks (from /proc/pid/stat field 22)
// to a time.Time by reading system boot time from /proc/stat.
func ticksToTime(ticks uint64) time.Time {
	bootTime := getBootTime()
	if bootTime.IsZero() {
		return time.Time{}
	}
	offsetSec := float64(ticks) / float64(clkTck)
	return bootTime.Add(time.Duration(offsetSec * float64(time.Second)))
}

var cachedBootTime time.Time

func getBootTime() time.Time {
	if !cachedBootTime.IsZero() {
		return cachedBootTime
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				sec, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					cachedBootTime = time.Unix(sec, 0)
					return cachedBootTime
				}
			}
		}
	}
	return time.Time{}
}

// FindProcessByName searches /proc for a process with the given binary basename.
// Used by FindRootProcess to locate the claude binary without gopsutil.
func FindProcessByName(name string) ([]int32, error) {
	entries, err := ScanProcesses()
	if err != nil {
		return nil, err
	}
	var pids []int32
	for _, e := range entries {
		base := e.Comm
		if len(e.Cmdline) > 0 {
			base = filepath.Base(e.Cmdline[0])
		}
		if base == name {
			pids = append(pids, e.PID)
		}
	}
	return pids, nil
}
