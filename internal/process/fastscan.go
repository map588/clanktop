package process

import (
	"path/filepath"

	"github.com/map588/clanktop/internal/model"
)

// FastScan gets the full process tree using platform-specific process enumeration.
// Returns root and flat list of descendants.
func FastScan(rootPID int32) (*model.ProcessInfo, []*model.ProcessInfo, error) {
	entries, err := ScanProcesses()
	if err != nil {
		return nil, nil, err
	}

	entryMap := make(map[int32]*ProcEntry, len(entries))
	children := make(map[int32][]int32)
	for i := range entries {
		e := &entries[i]
		entryMap[e.PID] = e
		children[e.PPID] = append(children[e.PPID], e.PID)
	}

	root, ok := entryMap[rootPID]
	if !ok {
		return nil, nil, nil
	}

	var buildNode func(e *ProcEntry) *model.ProcessInfo
	buildNode = func(e *ProcEntry) *model.ProcessInfo {
		name := e.Comm
		// ps comm for native binaries can be version string (e.g. "2.1.92")
		if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
			name = "claude"
		}
		// Prefer cmdline basename if available
		if len(e.Cmdline) > 0 {
			base := filepath.Base(e.Cmdline[0])
			if base != "" && base != "." {
				name = base
			}
		}

		node := &model.ProcessInfo{
			PID:     e.PID,
			PPID:    e.PPID,
			Name:    name,
			RSS:     e.RSS,
			Cmdline: e.Cmdline,
			State:   e.State,
			StartTime: e.StartTime,
		}
		for _, childPID := range children[e.PID] {
			if ce, ok := entryMap[childPID]; ok {
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

	return rootNode, allProcs, nil
}
