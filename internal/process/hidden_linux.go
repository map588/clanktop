//go:build !darwin

package process

import (
	"fmt"
	"os"
	"time"
)

// hiddenProcessNames are processes filtered from the tree entirely (system noise).
var hiddenProcessNames = map[string]bool{}

// PrintWatcherHint prints a hint about running with sudo for netlink process events.
func PrintWatcherHint(err error, pollInterval time.Duration) {
	fmt.Fprintf(os.Stderr, "Process event watcher unavailable: %v\n", err)
	fmt.Fprintf(os.Stderr, "  Run with sudo for sub-millisecond process tracking.\n")
	fmt.Fprintf(os.Stderr, "  Requires root or CAP_NET_ADMIN to monitor fork/exec/exit events.\n")
	fmt.Fprintf(os.Stderr, "  Without it, polling at %s intervals works fine.\n", pollInterval)
}
