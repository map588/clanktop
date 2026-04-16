package process

import (
	"context"
	"path/filepath"
	"time"

	"github.com/map588/clanktop/internal/debug"

	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/model"
)

const (
	DefaultSnapshotHistory = 60
	ExitedRetention        = 5 * time.Minute
)

type Scanner struct {
	rootPID      int32
	interval     time.Duration
	eventBus     *bus.EventBus
	snapshots    *model.RingBuffer[*model.ProcessInfo]
	prevPIDs     map[int32]struct{}
	prevProcs    map[int32]*model.ProcessInfo // full info from last tick
	exitedProcs  map[int32]*model.ProcessInfo // PID -> exited process with full info
}

func NewScanner(rootPID int32, interval time.Duration, eventBus *bus.EventBus) *Scanner {
	return &Scanner{
		rootPID:     rootPID,
		interval:    interval,
		eventBus:    eventBus,
		snapshots:   model.NewRingBuffer[*model.ProcessInfo](DefaultSnapshotHistory),
		prevPIDs:    make(map[int32]struct{}),
		prevProcs:   make(map[int32]*model.ProcessInfo),
		exitedProcs: make(map[int32]*model.ProcessInfo),
	}
}

func (s *Scanner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Scanner) tick() {
	tree, allProcs, err := FastScan(s.rootPID)
	if err != nil || tree == nil {
		debug.Log("SCANNER ERR: %v", err)
		return
	}

	currentPIDs := make(map[int32]struct{}, len(allProcs))
	for _, p := range allProcs {
		currentPIDs[p.PID] = struct{}{}
	}

	// Detect new and exited PIDs
	var newPIDs []int32
	var exitedPIDs []int32

	for pid := range currentPIDs {
		if _, existed := s.prevPIDs[pid]; !existed {
			newPIDs = append(newPIDs, pid)
		}
	}
	now := time.Now()
	for pid := range s.prevPIDs {
		if _, exists := currentPIDs[pid]; !exists {
			exitedPIDs = append(exitedPIDs, pid)
			// Keep full info from previous tick
			if prev, ok := s.prevProcs[pid]; ok {
				exitTime := now
				exited := *prev // copy
				exited.ExitTime = &exitTime
				exited.Children = nil // no children for exited
				s.exitedProcs[pid] = &exited
			}
		}
	}

	// Prune exited processes older than retention period
	for pid, ep := range s.exitedProcs {
		if ep.ExitTime != nil && now.Sub(*ep.ExitTime) > ExitedRetention {
			delete(s.exitedProcs, pid)
		}
	}

	// Attach exited processes to tree using real PPID hierarchy.
	// Build a mini-tree from exited procs, then graft onto live tree.
	exitedByPID := make(map[int32]*model.ProcessInfo)
	for pid, ep := range s.exitedProcs {
		name := ep.Name
		if len(ep.Cmdline) > 0 {
			name = filepath.Base(ep.Cmdline[0])
		}
		if name == "<defunct>" || name == "defunct" {
			continue
		}
		copy := *ep
		copy.Children = nil
		exitedByPID[pid] = &copy
	}

	// Wire exited children to exited parents
	attached := make(map[int32]bool)
	for pid, ep := range exitedByPID {
		if parent, ok := exitedByPID[ep.PPID]; ok {
			parent.Children = append(parent.Children, ep)
			attached[pid] = true
		}
	}

	// Attach unparented exited procs to live tree nodes
	for pid, ep := range exitedByPID {
		if attached[pid] {
			continue // already a child of another exited proc
		}
		if liveParent := findNode(tree, ep.PPID); liveParent != nil {
			liveParent.Children = append(liveParent.Children, ep)
		}
		// Orphans (parent not in tree or exited map) are dropped — expected
	}

	s.snapshots.Push(tree)
	s.prevPIDs = currentPIDs

	// Store full process info for next tick's exit handling
	newPrevProcs := make(map[int32]*model.ProcessInfo, len(allProcs))
	for _, p := range allProcs {
		newPrevProcs[p.PID] = p
	}
	s.prevProcs = newPrevProcs

	// Remove hidden processes from tree
	filterHidden(tree)

	bus.Send(s.eventBus.ProcessTree, bus.ProcessTreeEvent{
		Tree:       tree,
		AllProcs:   allProcs,
		NewPIDs:    newPIDs,
		ExitedPIDs: exitedPIDs,
	})
}

func filterHidden(node *model.ProcessInfo) {
	filtered := node.Children[:0]
	for _, c := range node.Children {
		name := filepath.Base(c.Name)
		if len(c.Cmdline) > 0 {
			name = filepath.Base(c.Cmdline[0])
		}
		if hiddenProcessNames[name] {
			continue
		}
		filterHidden(c)
		filtered = append(filtered, c)
	}
	node.Children = filtered
}

func findNode(root *model.ProcessInfo, pid int32) *model.ProcessInfo {
	if root.PID == pid {
		return root
	}
	for _, c := range root.Children {
		if found := findNode(c, pid); found != nil {
			return found
		}
	}
	return nil
}

// RecordExited adds a process to the exited process map from an external source (kqueue).
func (s *Scanner) RecordExited(info *model.ProcessInfo) {
	if info == nil {
		return
	}
	s.exitedProcs[info.PID] = info
}

// Snapshots returns the snapshot history.
func (s *Scanner) Snapshots() *model.RingBuffer[*model.ProcessInfo] {
	return s.snapshots
}

// ExitedProcs returns the map of recently exited processes.
func (s *Scanner) ExitedProcs() map[int32]*model.ProcessInfo {
	return s.exitedProcs
}
