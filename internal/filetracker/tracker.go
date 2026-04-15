package filetracker

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/model"
)

const MaxFileEvents = 500

// Prompt/config file patterns
var promptFilePatterns = []string{
	"CLAUDE.md",
	".claude/",
	".cursorrules",
	".github/copilot-instructions.md",
}

type Tracker struct {
	backend     backend.ClientBackend
	eventBus    *bus.EventBus
	events      *model.RingBuffer[model.FileEvent]
	promptFiles []model.FileEvent
	sessionStart time.Time
}

func NewTracker(be backend.ClientBackend, eventBus *bus.EventBus) *Tracker {
	return &Tracker{
		backend:      be,
		eventBus:     eventBus,
		events:       model.NewRingBuffer[model.FileEvent](MaxFileEvents),
		sessionStart: time.Now(),
	}
}

// ProcessArgv inspects a tool process's command line to detect file access.
func (t *Tracker) ProcessArgv(agentID string, pid int32, cmdline []string) {
	if len(cmdline) == 0 {
		return
	}

	cmd := filepath.Base(cmdline[0])
	events := extractFileEvents(cmd, cmdline, agentID, pid)

	for _, ev := range events {
		if !t.backend.FileAccessFilter(ev.Path) {
			continue
		}
		ev.Timestamp = time.Now()
		ev.Source = "argv"
		t.events.Push(ev)

		if isPromptFile(ev.Path) {
			t.promptFiles = append(t.promptFiles, ev)
		}

		bus.Send(t.eventBus.FileEvents, ev)
	}
}

// AddLogEvent records a file event from the log tailer.
func (t *Tracker) AddLogEvent(ev model.FileEvent) {
	ev.Source = "log"
	if !t.backend.FileAccessFilter(ev.Path) {
		return
	}
	t.events.Push(ev)

	if isPromptFile(ev.Path) {
		t.promptFiles = append(t.promptFiles, ev)
	}

	bus.Send(t.eventBus.FileEvents, ev)
}

// Events returns all tracked file events.
func (t *Tracker) Events() []model.FileEvent {
	return t.events.All()
}

// PromptFiles returns prompt/config files in load order.
func (t *Tracker) PromptFiles() []model.FileEvent {
	return t.promptFiles
}

// SessionStart returns when tracking began.
func (t *Tracker) SessionStart() time.Time {
	return t.sessionStart
}

func extractFileEvents(cmd string, args []string, agentID string, pid int32) []model.FileEvent {
	var events []model.FileEvent

	switch cmd {
	case "cat", "bat", "head", "tail", "less", "more":
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				events = append(events, model.FileEvent{
					AgentID:    agentID,
					Path:       arg,
					Operation:  model.FileOpRead,
					ProcessPID: pid,
				})
			}
		}

	case "grep", "rg", "ag":
		// File args come after the pattern
		pastPattern := false
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if !pastPattern {
				pastPattern = true
				continue
			}
			events = append(events, model.FileEvent{
				AgentID:    agentID,
				Path:       arg,
				Operation:  model.FileOpRead,
				ProcessPID: pid,
			})
		}

	case "sed":
		// sed -i indicates a write
		isInPlace := false
		for _, arg := range args[1:] {
			if arg == "-i" || strings.HasPrefix(arg, "-i") {
				isInPlace = true
			}
		}
		if isInPlace {
			// Last non-flag arg is the file
			for i := len(args) - 1; i >= 1; i-- {
				if !strings.HasPrefix(args[i], "-") && !strings.Contains(args[i], "/") || filepath.IsAbs(args[i]) || strings.Contains(args[i], ".") {
					events = append(events, model.FileEvent{
						AgentID:    agentID,
						Path:       args[i],
						Operation:  model.FileOpWrite,
						ProcessPID: pid,
					})
					break
				}
			}
		}

	case "python", "python3", "node":
		// First non-flag arg is typically the script
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				events = append(events, model.FileEvent{
					AgentID:    agentID,
					Path:       arg,
					Operation:  model.FileOpRead,
					ProcessPID: pid,
				})
				break
			}
		}

	case "touch":
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				events = append(events, model.FileEvent{
					AgentID:    agentID,
					Path:       arg,
					Operation:  model.FileOpCreate,
					ProcessPID: pid,
				})
			}
		}

	case "rm":
		for _, arg := range args[1:] {
			if !strings.HasPrefix(arg, "-") {
				events = append(events, model.FileEvent{
					AgentID:    agentID,
					Path:       arg,
					Operation:  model.FileOpDelete,
					ProcessPID: pid,
				})
			}
		}
	}

	return events
}

func isPromptFile(path string) bool {
	for _, pattern := range promptFilePatterns {
		if strings.HasSuffix(path, pattern) || strings.Contains(path, pattern) {
			return true
		}
	}
	return false
}
