package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/collector"
	"github.com/map588/clanktop/internal/tui"
)

var version = "dev"

func main() {
	pidFlag := flag.Int("pid", 0, "Show only the session of this agent PID")
	pollInterval := flag.Duration("poll-interval", 100*time.Millisecond, "Process tree poll interval")
	noColor := flag.Bool("no-color", false, "Disable colors")
	dump := flag.Bool("dump", false, "Print one JSON snapshot of all sessions to stdout and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("clanktop", version)
		return
	}
	if *noColor {
		tui.SetNoColor(true)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home directory: %v", err)
	}
	be := backend.NewClaudeCode(home)
	c := collector.New(be, int32(*pidFlag))

	if *dump {
		runDump(c)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go c.Run(ctx, *pollInterval)

	p := tea.NewProgram(tui.NewModel(c, be.Name(), home), tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

// runDump reads all sources, waits for a second CPU sample, and prints the state.
func runDump(c *collector.Collector) {
	c.PollOnce(time.Now())
	time.Sleep(500 * time.Millisecond)
	c.SampleCPU(time.Now())

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c.Snapshot(time.Now())); err != nil {
		log.Fatal(err)
	}
}
