package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

func main() { // poke2 //x
	target := int32(78680)

	// Poll twice, 1s apart, show diff
	snap1 := snapshot(target)
	fmt.Printf("=== Snapshot 1: %d descendants ===\n", len(snap1))
	for pid, info := range snap1 {
		fmt.Printf("  PID=%d PPID=%d Base=%s\n", pid, info.ppid, info.base)
	}

	fmt.Println("\nWaiting 2s for next snapshot...")
	time.Sleep(2 * time.Second)

	snap2 := snapshot(target)
	fmt.Printf("\n=== Snapshot 2: %d descendants ===\n", len(snap2))

	// Show new
	for pid, info := range snap2 {
		if _, existed := snap1[pid]; !existed {
			fmt.Printf("  NEW:  PID=%d PPID=%d Base=%s\n", pid, info.ppid, info.base)
		}
	}
	// Show exited
	for pid, info := range snap1 {
		if _, exists := snap2[pid]; !exists {
			fmt.Printf("  EXIT: PID=%d PPID=%d Base=%s\n", pid, info.ppid, info.base)
		}
	}
}

type pinfo struct {
	ppid int32
	base string
}

func snapshot(rootPID int32) map[int32]pinfo {
	procs, _ := process.Processes()

	// Build parent map
	children := make(map[int32][]int32)
	infoMap := make(map[int32]pinfo)
	for _, p := range procs {
		ppid, err := p.Ppid()
		if err != nil {
			continue
		}
		cmdline, _ := p.CmdlineSlice()
		base := ""
		if len(cmdline) > 0 {
			base = filepath.Base(cmdline[0])
		}
		cmd := strings.Join(cmdline, " ")
		if len(cmd) > 80 {
			cmd = cmd[:77] + "..."
		}
		infoMap[p.Pid] = pinfo{ppid: ppid, base: base}
		children[ppid] = append(children[ppid], p.Pid)
	}

	// Collect descendants
	result := make(map[int32]pinfo)
	var walk func(pid int32)
	walk = func(pid int32) {
		if info, ok := infoMap[pid]; ok {
			result[pid] = info
		}
		for _, c := range children[pid] {
			walk(c)
		}
	}
	walk(rootPID)
	return result
}
