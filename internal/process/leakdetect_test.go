package process

import (
	"testing"
	"time"

	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/model"
)

func TestCheckZombies_NoAlert(t *testing.T) {
	eb := bus.New()
	defer eb.Shutdown()
	ld := NewLeakDetector(eb)

	tree := &model.ProcessInfo{
		PID:   1,
		State: "running",
		Children: []*model.ProcessInfo{
			{PID: 2, State: "zombie"},
			{PID: 3, State: "running"},
		},
	}

	ld.checkZombies(tree)

	select {
	case <-eb.Alerts:
		t.Fatal("should not alert for 1 zombie (threshold is 5)")
	default:
	}
}

func TestCheckZombies_Alert(t *testing.T) {
	eb := bus.New()
	defer eb.Shutdown()
	ld := NewLeakDetector(eb)

	children := make([]*model.ProcessInfo, 6)
	for i := range children {
		children[i] = &model.ProcessInfo{PID: int32(i + 2), State: "zombie"}
	}
	tree := &model.ProcessInfo{PID: 1, State: "running", Children: children}

	ld.checkZombies(tree)

	select {
	case alert := <-eb.Alerts:
		if alert.Severity != "warning" {
			t.Errorf("expected warning severity, got %s", alert.Severity)
		}
	default:
		t.Fatal("expected alert for 6 zombies")
	}
}

func TestCheckShellAccumulation(t *testing.T) {
	eb := bus.New()
	defer eb.Shutdown()
	ld := NewLeakDetector(eb)

	shells := make([]*model.ProcessInfo, 11)
	for i := range shells {
		shells[i] = &model.ProcessInfo{PID: int32(i + 10), Name: "sh", State: "running"}
	}
	tree := &model.ProcessInfo{
		PID:      1,
		Role:     model.RoleOrchestrator,
		Children: shells,
	}

	ld.checkShellAccumulation(tree)

	select {
	case <-eb.Alerts:
		// expected
	default:
		t.Fatal("expected shell accumulation alert for 11 shells")
	}
}

func TestCheckSpawnRate_NoAlert(t *testing.T) {
	eb := bus.New()
	defer eb.Shutdown()
	ld := NewLeakDetector(eb)

	ld.checkSpawnRate([]int32{1, 2, 3})

	select {
	case <-eb.Alerts:
		t.Fatal("should not alert for 3 spawns")
	default:
	}
}

func TestCheckSpawnRate_Alert(t *testing.T) {
	eb := bus.New()
	defer eb.Shutdown()
	ld := NewLeakDetector(eb)

	// Simulate 60 spawns in quick succession (>10/s over 5s)
	pids := make([]int32, 60)
	for i := range pids {
		pids[i] = int32(i)
	}
	ld.checkSpawnRate(pids)

	// Need to wait briefly or use the same timestamp
	// The timestamps are all "now" so rate = 60/5 = 12/s
	select {
	case alert := <-eb.Alerts:
		if alert.Severity != "critical" {
			t.Errorf("expected critical severity, got %s", alert.Severity)
		}
	default:
		t.Fatal("expected spawn rate alert for 60 spawns in 5s window")
	}

	_ = time.Now() // suppress import
}
