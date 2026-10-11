// Package logtailer parses Claude Code session logs (JSONL).
package logtailer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/map588/clanktop/internal/model"
)

// maxInput is the number of bytes of tool input kept for the detail view.
const maxInput = 2000

// Log is the parsed state of one session log or subagent log.
type Log struct {
	Calls      []model.ToolCall
	index      map[string]int // tool_use id -> index in Calls
	Title      string
	LastPrompt string
	EndTurn    bool      // The last assistant record ended its turn
	FirstTime  time.Time // Timestamp of the first record that has one
	LastTime   time.Time // Timestamp of the newest record
	Lines      int       // Complete lines read
	Known      int       // Lines that parsed as user or assistant records
}

type record struct {
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	AITitle    string `json:"aiTitle"`
	LastPrompt string `json:"lastPrompt"`
	Message    struct {
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// Apply parses one log line and updates the state.
func (l *Log) Apply(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	l.Lines++
	var r record
	if json.Unmarshal(line, &r) != nil {
		return
	}
	ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	if l.FirstTime.IsZero() {
		l.FirstTime = ts
	}
	if ts.After(l.LastTime) {
		l.LastTime = ts
	}

	switch r.Type {
	case "ai-title":
		l.Title = r.AITitle
	case "last-prompt":
		l.LastPrompt = r.LastPrompt
	case "assistant":
		l.Known++
		l.EndTurn = r.Message.StopReason == "end_turn"
		for _, b := range blocks(r.Message.Content) {
			if b.Type == "tool_use" {
				l.addCall(b, ts)
			}
		}
	case "user":
		l.Known++
		for _, b := range blocks(r.Message.Content) {
			if b.Type != "tool_result" {
				continue
			}
			l.EndTurn = false
			if i, ok := l.index[b.ToolUseID]; ok {
				end := ts
				l.Calls[i].End = &end
				l.Calls[i].IsError = b.IsError
			}
		}
	}
}

// blocks decodes message content. User prompts have string content; it has no blocks.
func blocks(content json.RawMessage) []block {
	if len(content) == 0 || content[0] != '[' {
		return nil
	}
	var out []block
	_ = json.Unmarshal(content, &out)
	return out
}

func (l *Log) addCall(b block, ts time.Time) {
	if l.index == nil {
		l.index = map[string]int{}
	}
	if _, dup := l.index[b.ID]; dup {
		return
	}
	var input map[string]any
	_ = json.Unmarshal(b.Input, &input)
	summary, path, op := summarize(b.Name, input)
	raw := string(b.Input)
	if len(raw) > maxInput {
		raw = raw[:maxInput] + "…"
	}
	l.index[b.ID] = len(l.Calls)
	l.Calls = append(l.Calls, model.ToolCall{
		ID: b.ID, Name: b.Name, Summary: summary, Path: path, Op: op, Input: raw, Start: ts,
	})
}

// Pending is true when a tool call has no result yet.
func (l *Log) Pending() bool {
	return slices.ContainsFunc(l.Calls, func(c model.ToolCall) bool { return c.End == nil })
}

// summarize gives a one-line summary of a tool call, and the file path and
// operation for file tools. For Grep and Glob the path is the "path" input,
// never the pattern.
func summarize(name string, in map[string]any) (summary, path string, op model.FileOp) {
	s := func(k string) string {
		v, _ := in[k].(string)
		return v
	}
	switch name {
	case "Bash":
		first, _, _ := strings.Cut(s("command"), "\n")
		return first, "", 0
	case "Read":
		return s("file_path"), s("file_path"), model.FileOpRead
	case "Write":
		return s("file_path"), s("file_path"), model.FileOpWrite
	case "Edit", "MultiEdit":
		return s("file_path"), s("file_path"), model.FileOpEdit
	case "NotebookEdit":
		return s("notebook_path"), s("notebook_path"), model.FileOpEdit
	case "Grep", "Glob":
		summary = s("pattern")
		if p := s("path"); p != "" {
			summary += " in " + p
		}
		return summary, s("path"), model.FileOpSearch
	case "Agent", "Task":
		return s("subagent_type") + ": " + s("description"), "", 0
	case "WebFetch":
		return s("url"), "", 0
	case "WebSearch":
		return s("query"), "", 0
	case "Skill":
		return s("skill"), "", 0
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fmt.Sprint(in[k])
		if len(v) > 60 {
			v = v[:57] + "..."
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " "), "", 0
}

// Meta is the content of a subagent <agent>.meta.json file.
type Meta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

// ReadMeta reads the meta file beside a subagent log.
func ReadMeta(logPath string) Meta {
	var m Meta
	data, err := os.ReadFile(strings.TrimSuffix(logPath, ".jsonl") + ".meta.json")
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}
