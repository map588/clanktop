package process

import (
	"fmt"
	"time"

	"github.com/map588/clanktop/internal/model"
)

const (
	DefaultZombieThreshold = 5
	DefaultShellThreshold  = 10
	DefaultSpawnRateLimit  = 10.0 // per second
	SpawnRateWindow        = 5 * time.Second
)

// LeakDetector finds leak patterns in the process tree of one session.
type LeakDetector struct {
	spawns []time.Time
}

// Check returns an alert for each leak pattern in the tree. newPIDs is the
// number of processes that appeared since the last check.
func (ld *LeakDetector) Check(tree *model.ProcessInfo, newPIDs int, now time.Time) []string {
	var alerts []string

	zombies := 0
	walk(tree, func(n *model.ProcessInfo) {
		if n.State == "Z" {
			zombies++
		}
	})
	if zombies > DefaultZombieThreshold {
		alerts = append(alerts, fmt.Sprintf("Zombie accumulation: %d zombie processes", zombies))
	}

	shells := 0
	for _, c := range tree.Children {
		if c.Role == model.RoleToolProcess && c.ExitTime == nil {
			shells++
		}
	}
	if shells > DefaultShellThreshold {
		alerts = append(alerts, fmt.Sprintf("Shell accumulation: %d live tool shells", shells))
	}

	for range newPIDs {
		ld.spawns = append(ld.spawns, now)
	}
	cutoff := now.Add(-SpawnRateWindow)
	start := 0
	for start < len(ld.spawns) && ld.spawns[start].Before(cutoff) {
		start++
	}
	ld.spawns = ld.spawns[start:]
	if rate := ld.SpawnRate(); rate > DefaultSpawnRateLimit {
		alerts = append(alerts, fmt.Sprintf("Runaway spawn rate: %.1f/s over %s", rate, SpawnRateWindow))
	}
	return alerts
}

// SpawnRate is the number of new processes per second in the window.
func (ld *LeakDetector) SpawnRate() float64 {
	return float64(len(ld.spawns)) / SpawnRateWindow.Seconds()
}

func walk(n *model.ProcessInfo, fn func(*model.ProcessInfo)) {
	fn(n)
	for _, c := range n.Children {
		walk(c, fn)
	}
}
