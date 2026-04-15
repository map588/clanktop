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
	gopsutil "github.com/shirou/gopsutil/v3/process"

	"github.com/map588/clanktop/internal/agent"
	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/filetracker"
	"github.com/map588/clanktop/internal/logtailer"
	"github.com/map588/clanktop/internal/process"
	"github.com/map588/clanktop/internal/tui"
)

var version = "dev"

func main() {
	clientName := flag.String("client", "claude-code", "AI client backend to use")
	pidFlag := flag.Int("pid", 0, "Attach to a specific PID instead of auto-detecting")
	pollInterval := flag.Duration("poll-interval", 20*time.Millisecond, "Process tree poll interval")
	logFile := flag.String("log-file", "", "Path to AI client log file (overrides auto-detection)")
	noColor := flag.Bool("no-color", false, "Disable colors")
	dump := flag.Bool("dump", false, "Dump a single snapshot to stdout as JSON and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")

	flag.Parse()

	if *showVersion {
		fmt.Println("clanktop", version)
		os.Exit(0)
	}

	if *noColor {
		tui.SetNoColor(true)
	}

	// Find root PID first (need it for projectDir)
	var rootPID int32
	if *pidFlag != 0 {
		rootPID = int32(*pidFlag)
	} else {
		// Use a temp backend for finding the process
		tmpBe := backend.NewClaudeCode("")
		rootPID = waitForProcess(tmpBe)
	}

	// Detect project directory, then create backend with config knowledge
	projectDir := detectProjectDir(rootPID)
	fmt.Fprintf(os.Stderr, "Project dir: %q\n", projectDir)

	var be backend.ClientBackend
	switch *clientName {
	case "claude-code":
		be = backend.NewClaudeCode(projectDir)
	default:
		log.Fatalf("unknown client backend: %s", *clientName)
	}

	// Create event bus
	eventBus := bus.New()

	// Create components
	scanner := process.NewScanner(rootPID, *pollInterval, eventBus)
	detector := agent.NewDetector(be)
	tracker := filetracker.NewTracker(be, eventBus)
	leakDetect := process.NewLeakDetector(eventBus)

	// Log sources
	logSources := be.LogSources(rootPID)
	if *logFile != "" {
		logSources = []backend.LogSource{{Path: *logFile, Format: "text"}}
	}
	tailer := logtailer.NewTailer(logSources, be, eventBus)

	// Handle dump mode
	if *dump {
		runDump(scanner, detector, tracker)
		return
	}

	// Set up context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Start background components
	go scanner.Run(ctx)
	go tailer.Run(ctx)

	// Note: kqueue EVFILT_PROC doesn't work on macOS with SIP.
	// Using fast polling (50ms) instead to catch short-lived processes.

	// JSONL watcher — tail Claude Code session logs for tool calls
	jsonlWatcher := logtailer.NewJSONLWatcher(projectDir, eventBus)
	go jsonlWatcher.Run(ctx)

	// TUI owns all processing — no competing consumer goroutine
	model := tui.NewModel(eventBus, rootPID, be.Name(), be, detector, tracker, leakDetect, projectDir)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}

	cancel()
	eventBus.Shutdown()
}

func waitForProcess(be backend.ClientBackend) int32 {
	fmt.Fprintf(os.Stderr, "Waiting for %s...\n", be.Name())
	for {
		pid, err := be.FindRootProcess()
		if err == nil {
			fmt.Fprintf(os.Stderr, "Found %s (PID %d)\n", be.Name(), pid)
			return pid
		}
		time.Sleep(2 * time.Second)
	}
}

func detectProjectDir(pid int32) string {
	p, err := gopsutil.NewProcess(pid)
	if err != nil {
		return ""
	}
	cwd, err := p.Cwd()
	if err != nil {
		return ""
	}
	return cwd
}

func runDump(scanner *process.Scanner, detector *agent.Detector, tracker *filetracker.Tracker) {
	// Do a single scan cycle manually
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run one tick
	go scanner.Run(ctx)
	time.Sleep(2 * time.Second)
	cancel()

	snapshot := map[string]any{
		"agents":      detector.Agents(),
		"promptFiles": tracker.PromptFiles(),
		"fileEvents":  tracker.Events(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snapshot); err != nil {
		log.Fatal(err)
	}
}
