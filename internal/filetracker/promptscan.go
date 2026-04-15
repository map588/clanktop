package filetracker

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/map588/clanktop/internal/model"
)

// Known Claude Code config/prompt file locations, in typical load order.
// Paths are relative to home dir unless absolute.
var knownPromptPaths = []string{
	// Global
	".claude/CLAUDE.md",
	".claude/settings.json",
	".claude/settings.local.json",
}

// Project-relative prompt files
var projectPromptFiles = []string{
	"CLAUDE.md",
	".claude/CLAUDE.md",
	".claude/settings.json",
	".claude/settings.local.json",
	".cursorrules",
	".github/copilot-instructions.md",
}

// ScanPromptFiles discovers prompt/config files by checking known paths.
// Returns files sorted by modification time (proxy for load order).
func ScanPromptFiles(projectDir string) []model.FileEvent {
	home, _ := os.UserHomeDir()
	sessionStart := time.Now()

	var files []model.FileEvent

	// Global files
	for _, rel := range knownPromptPaths {
		path := filepath.Join(home, rel)
		if info, err := os.Stat(path); err == nil {
			files = append(files, model.FileEvent{
				Timestamp:  info.ModTime(),
				Path:       path,
				Operation:  model.FileOpRead,
				Source:     "scan",
			})
		}
	}

	// Project-specific claude config dir
	if home != "" {
		// Project memory dir: ~/.claude/projects/<encoded-path>/
		projConfigDir := findProjectConfigDir(home, projectDir)
		if projConfigDir != "" {
			scanDir(projConfigDir, &files)
		}
	}

	// Project-relative files
	if projectDir != "" {
		for _, rel := range projectPromptFiles {
			path := filepath.Join(projectDir, rel)
			if info, err := os.Stat(path); err == nil {
				files = append(files, model.FileEvent{
					Timestamp:  info.ModTime(),
					Path:       path,
					Operation:  model.FileOpRead,
					Source:     "scan",
				})
			}
		}
	}

	// Sort by mod time
	sort.Slice(files, func(i, j int) bool {
		return files[i].Timestamp.Before(files[j].Timestamp)
	})

	// Normalize timestamps relative to session
	_ = sessionStart
	return files
}

// findProjectConfigDir locates ~/.claude/projects/<encoded-path>/ for given project dir.
func findProjectConfigDir(home, projectDir string) string {
	if projectDir == "" {
		return ""
	}
	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	// Claude encodes path by replacing / with -
	// e.g., /Users/matthewprock/git/clanktop -> -Users-matthewprock-git-clanktop
	encoded := encodePath(projectDir)
	for _, e := range entries {
		if e.IsDir() && e.Name() == encoded {
			return filepath.Join(projectsDir, e.Name())
		}
	}
	return ""
}

func encodePath(path string) string {
	// Replace path separators with dashes, matching Claude Code's encoding
	result := make([]byte, 0, len(path))
	for _, c := range []byte(path) {
		if c == '/' {
			result = append(result, '-')
		} else {
			result = append(result, c)
		}
	}
	return string(result)
}

func scanDir(dir string, files *[]model.FileEvent) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		*files = append(*files, model.FileEvent{
			Timestamp: info.ModTime(),
			Path:      path,
			Operation: model.FileOpRead,
			Source:    "scan",
		})
	}
}
