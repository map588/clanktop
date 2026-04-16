//go:build !linux

package backend

// filteredPathPrefixes are path prefixes to exclude from file access tracking.
var filteredPathPrefixes = []string{
	"node_modules/",
	"/usr/lib/",
	"/usr/local/lib/",
	"/System/",
	"/Library/",
	"/opt/homebrew/",
	"/private/var/",
	"/var/folders/",
	"/tmp/",
}

