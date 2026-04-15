package process

import (
	"testing"
	"time"

	"github.com/map588/clanktop/internal/model"
)

func TestSnapshotDiffing(t *testing.T) {
	prev := map[int32]struct{}{
		100: {},
		101: {},
		102: {},
	}
	current := map[int32]struct{}{
		101: {},
		102: {},
		103: {},
	}

	// Check new PIDs
	var newPIDs []int32
	for pid := range current {
		if _, ok := prev[pid]; !ok {
			newPIDs = append(newPIDs, pid)
		}
	}
	if len(newPIDs) != 1 || newPIDs[0] != 103 {
		t.Fatalf("expected new PID 103, got %v", newPIDs)
	}

	// Check exited PIDs
	var exitedPIDs []int32
	for pid := range prev {
		if _, ok := current[pid]; !ok {
			exitedPIDs = append(exitedPIDs, pid)
		}
	}
	if len(exitedPIDs) != 1 || exitedPIDs[0] != 100 {
		t.Fatalf("expected exited PID 100, got %v", exitedPIDs)
	}
}

func TestExitedProcessRetention(t *testing.T) {
	now := time.Now()
	old := now.Add(-6 * time.Minute)
	recent := now.Add(-1 * time.Minute)

	exitedProcs := map[int32]*model.ProcessInfo{
		100: {PID: 100, ExitTime: &old},
		101: {PID: 101, ExitTime: &recent},
	}

	// Prune
	for pid, ep := range exitedProcs {
		if ep.ExitTime != nil && now.Sub(*ep.ExitTime) > ExitedRetention {
			delete(exitedProcs, pid)
		}
	}

	if _, ok := exitedProcs[100]; ok {
		t.Fatal("expected PID 100 to be pruned")
	}
	if _, ok := exitedProcs[101]; !ok {
		t.Fatal("expected PID 101 to be retained")
	}
}
