package process

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/map588/clanktop/internal/model"

	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// FastScan gets the full process tree using a single `ps` invocation.
// Returns root and flat list of descendants. Much faster than per-process gopsutil calls.
func FastScan(rootPID int32) (*model.ProcessInfo, []*model.ProcessInfo, error) {
	// Single ps call — ~2ms vs ~100ms for gopsutil enumeration
	out, err := exec.Command("ps", "-eo", "pid,ppid,rss,comm").Output()
	if err != nil {
		return nil, nil, err
	}

	type entry struct {
		pid, ppid int32
		rss       uint64
		comm      string
	}

	entries := make(map[int32]*entry)
	children := make(map[int32][]int32)

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

		// Strip parens from zombie/exited process names: (bash) → bash
		comm = strings.TrimPrefix(comm, "(")
		comm = strings.TrimSuffix(comm, ")")

		// Skip defunct/zombie
		if comm == "<defunct>" || comm == "defunct" {
			continue
		}

		// Skip caffeinate
		base := filepath.Base(comm)
		if base == "caffeinate" {
			continue
		}

		e := &entry{
			pid:  int32(pid),
			ppid: int32(ppid),
			rss:  rss * 1024, // ps reports in KB
			comm: comm,
		}
		entries[e.pid] = e
		children[e.ppid] = append(children[e.ppid], e.pid)
	}

	root, ok := entries[rootPID]
	if !ok {
		return nil, nil, nil
	}

	// Build tree recursively from root
	var buildNode func(e *entry) *model.ProcessInfo
	buildNode = func(e *entry) *model.ProcessInfo {
		name := filepath.Base(e.comm)
		// ps comm for native binaries can be version string (e.g. "2.1.92")
		// Use full comm path basename instead, or "claude" if it looks like a version
		if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
			name = "claude"
		}
		node := &model.ProcessInfo{
			PID:  e.pid,
			PPID: e.ppid,
			Name: name,
			RSS:  e.rss,
		}
		for _, childPID := range children[e.pid] {
			if ce, ok := entries[childPID]; ok {
				node.Children = append(node.Children, buildNode(ce))
			}
		}
		return node
	}

	rootNode := buildNode(root)

	// Flatten
	var allProcs []*model.ProcessInfo
	var flatten func(*model.ProcessInfo)
	flatten = func(n *model.ProcessInfo) {
		allProcs = append(allProcs, n)
		for _, c := range n.Children {
			flatten(c)
		}
	}
	flatten(rootNode)

	// Enrich only descendants with cmdline (need gopsutil for this)
	for _, p := range allProcs {
		enrichCmdline(p)
	}

	return rootNode, allProcs, nil
}

// enrichCmdline gets full cmdline for a process. Lightweight — skips CPU/state.
func enrichCmdline(info *model.ProcessInfo) {
	p, err := gopsutil.NewProcess(info.PID)
	if err != nil {
		return
	}
	if cmdline, err := p.CmdlineSlice(); err == nil && len(cmdline) > 0 {
		info.Cmdline = cmdline
		// Use cmdline basename as Name (more reliable than ps comm)
		base := filepath.Base(cmdline[0])
		if base != "" && base != "." {
			info.Name = base
		}
	}
}
