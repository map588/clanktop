package logtailer

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/bus"
)

type Tailer struct {
	sources  []backend.LogSource
	backend  backend.ClientBackend
	eventBus *bus.EventBus
}

func NewTailer(sources []backend.LogSource, be backend.ClientBackend, eventBus *bus.EventBus) *Tailer {
	return &Tailer{
		sources:  sources,
		backend:  be,
		eventBus: eventBus,
	}
}

func (t *Tailer) Run(ctx context.Context) {
	if len(t.sources) == 0 {
		return // no-op, graceful degradation
	}

	for _, src := range t.sources {
		go t.tailFile(ctx, src.Path)
	}
}

func (t *Tailer) tailFile(ctx context.Context, path string) {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("logtailer: skipping %s: %v", path, err)
		return
	}
	defer f.Close()

	// Seek to end — we only want new lines
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		log.Printf("logtailer: seek failed for %s: %v", path, err)
		return
	}

	var lastSize int64
	if info, err := f.Stat(); err == nil {
		lastSize = info.Size()
	}

	reader := bufio.NewReader(f)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check for truncation/rotation
			if info, err := f.Stat(); err == nil {
				if info.Size() < lastSize {
					// File was truncated — seek to beginning
					f.Seek(0, io.SeekStart)
					reader.Reset(f)
				}
				lastSize = info.Size()
			}

			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				event := t.backend.ParseLogLine(line)
				if event != nil {
					bus.Send(t.eventBus.AgentEvents, bus.AgentEvent{
						Type:    event.Type,
						AgentID: event.AgentID,
						Data:    event.Data,
					})
				}
			}
		}
	}
}
