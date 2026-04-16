//go:build !linux

package process

import "time"

// hiddenProcessNames are processes filtered from the tree entirely (system noise).
var hiddenProcessNames = map[string]bool{
	"caffeinate": true,
}

// PrintWatcherHint is a no-op on macOS. SIP blocks kqueue EVFILT_PROC on
// nearly all systems, so printing a "run with sudo" hint would be misleading.
func PrintWatcherHint(_ error, _ time.Duration) {}
