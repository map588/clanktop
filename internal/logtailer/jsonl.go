package logtailer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/map588/clanktop/internal/bus"
	"github.com/map588/clanktop/internal/model"
)

// JSONLWatcher tails Claude Code session JSONL files for tool calls.
type JSONLWatcher struct {
	projectDir string
	eventBus   *bus.EventBus
}

func NewJSONLWatcher(projectDir string, eventBus *bus.EventBus) *JSONLWatcher {
	return &JSONLWatcher{
		projectDir: projectDir,
		eventBus:   eventBus,
	}
}

func (w *JSONLWatcher) Run(ctx context.Context) {
	jsonlPath := w.findLatestJSONL()
	if jsonlPath == "" {
		return
	}

	f, err := os.Open(jsonlPath)
	if err != nil {
		return
	}
	defer f.Close()

	// Skip existing content — only show live activity
	f.Seek(0, io.SeekEnd)
	reader := bufio.NewReader(f)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				w.parseLine(line)
			}
		}
	}
}

func (w *JSONLWatcher) findLatestJSONL() string {
	home, _ := os.UserHomeDir()
	if home == "" || w.projectDir == "" {
		return ""
	}

	// Encode project dir path
	encoded := strings.ReplaceAll(w.projectDir, "/", "-")
	projDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}

	// Find most recently modified .jsonl
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var jsonlFiles []fileInfo
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			info, err := e.Info()
			if err != nil {
				continue
			}
			jsonlFiles = append(jsonlFiles, fileInfo{
				path:    filepath.Join(projDir, e.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	if len(jsonlFiles) == 0 {
		return ""
	}

	sort.Slice(jsonlFiles, func(i, j int) bool {
		return jsonlFiles[i].modTime.After(jsonlFiles[j].modTime)
	})

	return jsonlFiles[0].path
}

func (w *JSONLWatcher) parseExisting(f *os.File) {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		w.parseLine(scanner.Text())
	}
}

// JSONL message structure (subset)
type jsonlMessage struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			ID    string          `json:"id"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

type toolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (w *JSONLWatcher) parseLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var msg jsonlMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return
	}

	if msg.Type != "assistant" {
		return
	}

	for _, block := range msg.Message.Content {
		if block.Type != "tool_use" {
			continue
		}

		var inputMap map[string]interface{}
		json.Unmarshal(block.Input, &inputMap)

		// Build concise arg summary — show most relevant field per tool
		var args []string
		switch block.Name {
		case "Bash":
			if cmd, ok := inputMap["command"]; ok {
				args = append(args, toString(cmd))
			}
		case "Read":
			if fp, ok := inputMap["file_path"]; ok {
				args = append(args, toString(fp))
			}
		case "Write":
			if fp, ok := inputMap["file_path"]; ok {
				args = append(args, toString(fp))
			}
		case "Edit":
			if fp, ok := inputMap["file_path"]; ok {
				args = append(args, toString(fp))
			}
		case "Grep":
			if p, ok := inputMap["pattern"]; ok {
				args = append(args, toString(p))
			}
			if p, ok := inputMap["path"]; ok {
				args = append(args, toString(p))
			}
		case "Glob":
			if p, ok := inputMap["pattern"]; ok {
				args = append(args, toString(p))
			}
		case "Agent":
			if d, ok := inputMap["description"]; ok {
				args = append(args, toString(d))
			}
		default:
			// MCP tools, etc — show all params concisely
			for k, v := range inputMap {
				val := toString(v)
				if len(val) > 80 {
					val = val[:77] + "..."
				}
				args = append(args, k+"="+val)
			}
		}

		tc := model.ToolCall{
			Timestamp: time.Now(),
			AgentID:   "claude",
			ToolName:  block.Name,
			Args:      args,
		}
		bus.Send(w.eventBus.ToolCalls, tc)

		// Emit file event for Read calls on prompt-like files
		if block.Name == "Read" {
			if fp, ok := inputMap["file_path"]; ok {
				path := toString(fp)
				if isPromptLike(path) {
					bus.Send(w.eventBus.FileEvents, model.FileEvent{
						Timestamp: time.Now(),
						Path:      path,
						Operation: model.FileOpRead,
						Source:    "jsonl",
					})
				}
			}
		}
	}
}

func isPromptLike(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".md") ||
		strings.Contains(lower, ".claude/") ||
		strings.Contains(lower, ".cursorrules") ||
		strings.HasSuffix(lower, "settings.json") ||
		strings.HasSuffix(lower, "settings.local.json")
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
