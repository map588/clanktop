package process

import (
	"testing"
	"time"

	"github.com/map588/clanktop/internal/model"
)

func treeWith(n int, role model.ProcessRole, state string) *model.ProcessInfo {
	root := &model.ProcessInfo{PID: 1, Role: model.RoleOrchestrator}
	for i := range n {
		root.Children = append(root.Children, &model.ProcessInfo{PID: int32(i + 2), Role: role, State: state})
	}
	return root
}

func TestLeakDetector(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		tree   *model.ProcessInfo
		newPID int
		want   int
	}{
		{"one zombie is fine", treeWith(1, model.RoleToolProcess, "Z"), 0, 0},
		{"six zombies alert", treeWith(6, model.RoleMCPServer, "Z"), 0, 1},
		{"ten shells is fine", treeWith(10, model.RoleToolProcess, ""), 0, 0},
		{"eleven shells alert", treeWith(11, model.RoleToolProcess, ""), 0, 1},
		{"fifty spawns in one check alert", treeWith(0, 0, ""), 51, 1},
	}
	for _, tt := range tests {
		var ld LeakDetector
		if got := ld.Check(tt.tree, tt.newPID, now); len(got) != tt.want {
			t.Errorf("%s: got alerts %v, want %d", tt.name, got, tt.want)
		}
	}
}

func TestSpawnWindowExpires(t *testing.T) {
	var ld LeakDetector
	now := time.Now()
	ld.Check(treeWith(0, 0, ""), 60, now)
	if got := ld.Check(treeWith(0, 0, ""), 0, now.Add(SpawnRateWindow+time.Second)); len(got) != 0 {
		t.Fatalf("old spawns must expire: %v", got)
	}
}
