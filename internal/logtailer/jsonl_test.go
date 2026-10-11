package logtailer

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/map588/clanktop/internal/model"
	"pgregory.net/rapid"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// applyAll reads data in chunks through a LineReader, as the collector does
// when the writer appends the file in parts.
func applyAll(t testing.TB, chunks [][]byte) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lr, err := OpenLineReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lr.Close()
	var l Log
	for _, c := range chunks {
		if _, err := f.Write(c); err != nil {
			t.Fatal(err)
		}
		if err := lr.ReadLines(l.Apply); err != nil {
			t.Fatal(err)
		}
	}
	return &l
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSessionLog(t *testing.T) {
	l := applyAll(t, [][]byte{readFixture(t, "session.jsonl")})

	if l.Title != "rework-agent-discovery" || l.LastPrompt != "clanktop currently is kind of useless." {
		t.Fatalf("title %q prompt %q", l.Title, l.LastPrompt)
	}
	if l.Lines != 12 || l.Known != 7 {
		t.Fatalf("Lines %d Known %d", l.Lines, l.Known)
	}
	if len(l.Calls) != 4 {
		t.Fatalf("got %d calls: %+v", len(l.Calls), l.Calls)
	}
	bash, grep, read, mcp := l.Calls[0], l.Calls[1], l.Calls[2], l.Calls[3]

	// The duration comes from the record timestamps, not from the read time.
	if bash.Name != "Bash" || bash.Summary != "ps -eo pid,ppid,lstart,command | grep -iE 'claude|codex'" ||
		!bash.Start.Equal(ts("2026-10-11T15:53:30.074Z")) || bash.End == nil ||
		bash.End.Sub(bash.Start) != 2500*time.Millisecond || bash.IsError {
		t.Fatalf("bash call: %+v", bash)
	}
	// The path of a Grep call is the "path" input. The old TUI used the pattern.
	if grep.Path != "/Users/x/git/tui/clanktop/internal" || grep.Op != model.FileOpSearch ||
		grep.Summary != "FindRootProcess in /Users/x/git/tui/clanktop/internal" || !grep.IsError {
		t.Fatalf("grep call: %+v", grep)
	}
	if read.Path != "/Users/x/git/tui/clanktop/go.mod" || read.Op != model.FileOpRead || read.End != nil {
		t.Fatalf("read call: %+v", read)
	}
	if mcp.Summary != "include_content=true keypath=decisions" || mcp.Path != "" {
		t.Fatalf("mcp call: %+v", mcp)
	}
	if !l.Pending() || l.EndTurn {
		t.Fatalf("Pending %v EndTurn %v", l.Pending(), l.EndTurn)
	}
}

func TestSubAgentLog(t *testing.T) {
	l := applyAll(t, [][]byte{readFixture(t, "agent-a98.jsonl")})
	if !l.EndTurn || l.Pending() || len(l.Calls) != 1 || l.Calls[0].Path != "/Users/x/hal/readme.md" {
		t.Fatalf("subagent log: EndTurn %v Pending %v calls %+v", l.EndTurn, l.Pending(), l.Calls)
	}
	m := ReadMeta(filepath.Join("testdata", "agent-a98.jsonl"))
	if m.AgentType != "Explore" || m.Description != "Map modified sx1302_hal repo" ||
		m.ToolUseID != "toolu_01Bqqq1dX6L8RA8tLW8UscS9" {
		t.Fatalf("meta: %+v", m)
	}
}

// The writer can be in the middle of a line when the reader polls. The result
// must not depend on where the file was cut. The old watcher dropped the
// first part of a cut line.
func TestChunkedReadEqualsWholeRead(t *testing.T) {
	data := slices.Concat(readFixture(t, "session.jsonl"), readFixture(t, "agent-a98.jsonl"))
	want := applyAll(t, [][]byte{data})
	rapid.Check(t, func(rt *rapid.T) {
		cuts := rapid.SliceOfN(rapid.IntRange(0, len(data)), 1, 6).Draw(rt, "cuts")
		slices.Sort(cuts)
		var chunks [][]byte
		prev := 0
		for _, c := range cuts {
			chunks = append(chunks, data[prev:c])
			prev = c
		}
		chunks = append(chunks, data[prev:])
		got := applyAll(t, chunks)
		if !reflect.DeepEqual(got, want) {
			rt.Fatalf("chunked read differs at cuts %v:\ngot  %+v\nwant %+v", cuts, got, want)
		}
	})
}

func TestUnknownFormatIsCounted(t *testing.T) {
	l := applyAll(t, [][]byte{[]byte("{\"kind\":\"msg\"}\nnot json\n{\"type\":\"assistant\",\"message\":{}}\n")})
	if l.Lines != 3 || l.Known != 1 {
		t.Fatalf("Lines %d Known %d", l.Lines, l.Known)
	}
}
