package filetracker

import (
	"testing"

	"github.com/map588/clanktop/internal/model"
)

func TestExtractFileEvents_Cat(t *testing.T) {
	events := extractFileEvents("cat", []string{"cat", "/home/user/src/main.go"}, "agent-0", 100)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "/home/user/src/main.go" {
		t.Errorf("path: got %s, want /home/user/src/main.go", events[0].Path)
	}
	if events[0].Operation != model.FileOpRead {
		t.Errorf("op: got %v, want Read", events[0].Operation)
	}
}

func TestExtractFileEvents_SedInPlace(t *testing.T) {
	events := extractFileEvents("sed", []string{"sed", "-i", "s/foo/bar/", "config.yaml"}, "agent-0", 101)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Operation != model.FileOpWrite {
		t.Errorf("op: got %v, want Write", events[0].Operation)
	}
}

func TestExtractFileEvents_Rm(t *testing.T) {
	events := extractFileEvents("rm", []string{"rm", "-f", "temp.txt"}, "agent-0", 102)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Operation != model.FileOpDelete {
		t.Errorf("op: got %v, want Delete", events[0].Operation)
	}
}

func TestExtractFileEvents_NoArgs(t *testing.T) {
	events := extractFileEvents("cat", []string{"cat"}, "agent-0", 100)
	if len(events) != 0 {
		t.Fatalf("expected 0 events for bare cat, got %d", len(events))
	}
}

func TestExtractFileEvents_GrepWithFile(t *testing.T) {
	events := extractFileEvents("grep", []string{"grep", "pattern", "file.txt"}, "agent-0", 103)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Path != "file.txt" {
		t.Errorf("path: got %s, want file.txt", events[0].Path)
	}
}

func TestIsPromptFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"CLAUDE.md", true},
		{"/home/user/project/CLAUDE.md", true},
		{".claude/settings.json", true},
		{".cursorrules", true},
		{"src/main.go", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		got := isPromptFile(tt.path)
		if got != tt.want {
			t.Errorf("isPromptFile(%s): got %v, want %v", tt.path, got, tt.want)
		}
	}
}
