// Package filetracker finds the prompt and config files that Claude Code loads.
package filetracker

import (
	"os"
	"path/filepath"
	"slices"
	"time"
)

// PromptFile is a prompt or config file and its modification time.
type PromptFile struct {
	Path    string
	ModTime time.Time
}

// Global files, relative to the home directory.
var globalPromptFiles = []string{
	".claude/CLAUDE.md",
	".claude/settings.json",
	".claude/settings.local.json",
	".claude/rules/*.md",
}

// Project files, relative to the session cwd.
var projectPromptFiles = []string{
	"CLAUDE.md",
	"CLAUDE.local.md",
	".claude/CLAUDE.md",
	".claude/settings.json",
	".claude/settings.local.json",
	".mcp.json",
}

// ScanPromptFiles finds the prompt and config files of a session. projectDir
// is the directory of the session log; its memory/ directory holds the
// project memory files. The most recently modified file is first.
func ScanPromptFiles(home, cwd, projectDir string) []PromptFile {
	var patterns []string
	if home != "" {
		for _, p := range globalPromptFiles {
			patterns = append(patterns, filepath.Join(home, p))
		}
	}
	if cwd != "" {
		for _, p := range projectPromptFiles {
			patterns = append(patterns, filepath.Join(cwd, p))
		}
	}
	if projectDir != "" {
		patterns = append(patterns, filepath.Join(projectDir, "memory", "*.md"))
	}

	var files []PromptFile
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				files = append(files, PromptFile{Path: path, ModTime: info.ModTime()})
			}
		}
	}
	slices.SortFunc(files, func(a, b PromptFile) int { return b.ModTime.Compare(a.ModTime) })
	return files
}
