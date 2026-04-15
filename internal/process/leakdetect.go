package process

import (
	"fmt"
	"strings"
	"time"

	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/model"
)

const (
	DefaultZombieThreshold = 5
	DefaultShellThreshold  = 10
	DefaultSpawnRateLimit  = 10.0 // per second
	SpawnRateWindow        = 5 * time.Second
)

type LeakDetector struct {
	eventBus       *bus.EventBus
	spawnTimestamps []time.Time
}

func NewLeakDetector(eventBus *bus.EventBus) *LeakDetector {
	return &LeakDetector{
		eventBus: eventBus,
	}
}

// Check analyzes the current process tree for leak patterns.
func (ld *LeakDetector) Check(tree *model.ProcessInfo, newPIDs []int32) {
	ld.checkZombies(tree)
	ld.checkShellAccumulation(tree)
	ld.checkSpawnRate(newPIDs)
}

func (ld *LeakDetector) checkZombies(tree *model.ProcessInfo) {
	count := 0
	var walk func(*model.ProcessInfo)
	walk = func(node *model.ProcessInfo) {
		if strings.Contains(strings.ToLower(node.State), "zombie") {
			count++
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(tree)

	if count > DefaultZombieThreshold {
		bus.Send(ld.eventBus.Alerts, bus.AlertEvent{
			Message:  fmt.Sprintf("Zombie accumulation: %d zombie processes detected", count),
			Severity: "warning",
		})
	}
}

func (ld *LeakDetector) checkShellAccumulation(tree *model.ProcessInfo) {
	// Count sh/bash children per agent node
	agentShells := make(map[int32]int) // agent PID -> shell count

	var walk func(*model.ProcessInfo, int32)
	walk = func(node *model.ProcessInfo, currentAgentPID int32) {
		if node.Role == model.RoleOrchestrator || node.Role == model.RoleSubAgent {
			currentAgentPID = node.PID
		}

		name := strings.ToLower(node.Name)
		if (name == "sh" || name == "bash" || name == "zsh") && currentAgentPID != 0 {
			agentShells[currentAgentPID]++
		}

		for _, child := range node.Children {
			walk(child, currentAgentPID)
		}
	}
	walk(tree, 0)

	for agentPID, count := range agentShells {
		if count > DefaultShellThreshold {
			bus.Send(ld.eventBus.Alerts, bus.AlertEvent{
				Message:  fmt.Sprintf("Shell accumulation: agent PID %d has %d shell processes", agentPID, count),
				Severity: "warning",
			})
		}
	}
}

func (ld *LeakDetector) checkSpawnRate(newPIDs []int32) {
	now := time.Now()

	// Record spawn timestamps
	for range newPIDs {
		ld.spawnTimestamps = append(ld.spawnTimestamps, now)
	}

	// Prune old timestamps outside the window
	cutoff := now.Add(-SpawnRateWindow)
	start := 0
	for start < len(ld.spawnTimestamps) && ld.spawnTimestamps[start].Before(cutoff) {
		start++
	}
	ld.spawnTimestamps = ld.spawnTimestamps[start:]

	// Calculate rate
	if len(ld.spawnTimestamps) > 0 {
		rate := float64(len(ld.spawnTimestamps)) / SpawnRateWindow.Seconds()
		if rate > DefaultSpawnRateLimit {
			bus.Send(ld.eventBus.Alerts, bus.AlertEvent{
				Message:  fmt.Sprintf("Runaway spawn rate: %.1f/s over %s window", rate, SpawnRateWindow),
				Severity: "critical",
			})
		}
	}
}
