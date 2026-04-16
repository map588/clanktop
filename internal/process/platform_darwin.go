//go:build !linux

package process

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// ScanProcesses enumerates all processes using a single `ps -eo` call
// and enriches each with full cmdline via gopsutil.
func ScanProcesses() ([]ProcEntry, error) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,rss,comm").Output()
	if err != nil {
		return nil, err
	}

	var entries []ProcEntry
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "PID") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil {
			continue
		}
		ppid, err := strconv.ParseInt(fields[1], 10, 32)
		if err != nil {
			continue
		}
		rss, _ := strconv.ParseUint(fields[2], 10, 64)
		comm := strings.Join(fields[3:], " ")

		// Strip parens from zombie/exited process names: (bash) -> bash
		comm = strings.TrimPrefix(comm, "(")
		comm = strings.TrimSuffix(comm, ")")

		if comm == "<defunct>" || comm == "defunct" {
			continue
		}

		e := ProcEntry{
			PID:  int32(pid),
			PPID: int32(ppid),
			RSS:  rss * 1024, // ps reports KB
			Comm: filepath.Base(comm),
		}

		// Enrich with full cmdline via gopsutil
		if p, err := gopsutil.NewProcess(e.PID); err == nil {
			if cmdline, err := p.CmdlineSlice(); err == nil && len(cmdline) > 0 {
				e.Cmdline = cmdline
			}
		}

		entries = append(entries, e)
	}

	return entries, nil
}

// GetProcessCwd returns the working directory for a given PID.
func GetProcessCwd(pid int32) (string, error) {
	p, err := gopsutil.NewProcess(pid)
	if err != nil {
		return "", err
	}
	return p.Cwd()
}
