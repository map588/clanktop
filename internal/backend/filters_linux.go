//go:build !darwin

package backend

// filteredPathPrefixes are path prefixes to exclude from file access tracking.
var filteredPathPrefixes = []string{
	"node_modules/",
	"/usr/lib/",
	"/usr/local/lib/",
	"/proc/",
	"/sys/",
	"/run/",
	"/snap/",
	"/tmp/",
}

