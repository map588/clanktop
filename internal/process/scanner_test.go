package process

import (
	"os"
	"testing"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/model"
)

var t0 = time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)

func newTestScanner(cmdlines map[int32][]string) (*Scanner, *int) {
	s := NewScanner(backend.NewClaudeCode(""))
	reads := 0
	s.cmdline = func(pid int32) []string {
		reads++
		return cmdlines[pid]
	}
	return s, &reads
}

var cmdlines = map[int32][]string{
	101: {"/bin/zsh", "-c", "source /Users/x/.claude/shell-snapshots/s.sh && eval 'go test ./...' < /dev/null"},
	102: {"node", "/Users/x/.nvm/bin/memstate-mcp"},
	103: {"caffeinate", "-i", "-t", "300"},
	104: {"go", "test", "./..."},
	301: {"/Users/x/.cargo/bin/tokensave", "serve"},
}

func procs(pids ...int32) []Proc {
	all := map[int32]Proc{
		1:   {PID: 1, PPID: 0, Comm: "launchd", Start: t0},
		100: {PID: 100, PPID: 1, Comm: "2.1.296", Start: t0.Add(1 * time.Second)},
		101: {PID: 101, PPID: 100, Comm: "zsh", Start: t0.Add(5 * time.Second)},
		102: {PID: 102, PPID: 100, Comm: "node", Start: t0.Add(2 * time.Second)},
		103: {PID: 103, PPID: 100, Comm: "caffeinate", Start: t0.Add(3 * time.Second)},
		104: {PID: 104, PPID: 101, Comm: "go", Start: t0.Add(6 * time.Second)},
		200: {PID: 200, PPID: 1, Comm: "Finder", Start: t0},
		300: {PID: 300, PPID: 1, Comm: "2.1.296", Start: t0},
		301: {PID: 301, PPID: 300, Comm: "tokensave", Start: t0.Add(time.Second)},
		105: {PID: 105, PPID: 100, Comm: "sh", Start: t0.Add(7 * time.Second)},
		// A session hosted by a Claude Code daemon that session 100 started.
		400: {PID: 400, PPID: 102, Comm: "claude", Start: t0.Add(8 * time.Second)},
		401: {PID: 401, PPID: 400, Comm: "tokensave", Start: t0.Add(9 * time.Second)},
	}
	var out []Proc
	for _, p := range pids {
		out = append(out, all[p])
	}
	return out
}

func names(n *model.ProcessInfo) []string {
	var out []string
	for _, c := range n.Children {
		out = append(out, c.Name)
	}
	return out
}

func TestScanBuildsOneTreePerSession(t *testing.T) {
	s, reads := newTestScanner(cmdlines)
	trees, news := s.Scan(procs(1, 100, 101, 102, 103, 104, 200, 300, 301), []int32{100, 300, 999}, t0)

	if len(trees) != 2 || trees[999] != nil {
		t.Fatalf("got %d trees", len(trees))
	}
	a := trees[100]
	if a.Role != model.RoleOrchestrator || len(a.Children) != 2 {
		t.Fatalf("root %+v children %v", a, names(a))
	}
	// Children are in start order. caffeinate is hidden.
	mcp, bash := a.Children[0], a.Children[1]
	if mcp.Name != "memstate-mcp" || mcp.Role != model.RoleMCPServer {
		t.Fatalf("mcp: %+v", mcp)
	}
	if bash.Name != "go test ./..." || bash.Role != model.RoleToolProcess || len(bash.Children) != 1 {
		t.Fatalf("bash: %+v", bash)
	}
	if g := bash.Children[0]; g.Name != "go" || g.Role != model.RoleToolProcess {
		t.Fatalf("grandchild inherits the tool role: %+v", g)
	}
	if b := trees[300]; len(b.Children) != 1 || b.Children[0].Name != "tokensave" {
		t.Fatalf("second session: %v", names(b))
	}
	// The first scan of a session finds old processes. They are not spawns.
	if news[100] != 0 || news[300] != 0 {
		t.Fatalf("new counts %v", news)
	}
	// One cmdline read per process below a root. Roots need none.
	if *reads != 5 {
		t.Fatalf("cmdline reads %d, want 5", *reads)
	}

	// A second scan reads no cmdline again, also not for the hidden
	// caffeinate. It counts the one new process.
	_, news = s.Scan(procs(1, 100, 101, 102, 103, 104, 105, 200, 300, 301), []int32{100, 300}, t0.Add(100*time.Millisecond))
	if *reads != 6 || news[100] != 1 || news[300] != 0 {
		t.Fatalf("second scan: reads %d news %v", *reads, news)
	}
}

func TestNestedSessionHasItsOwnTree(t *testing.T) {
	s, _ := newTestScanner(cmdlines)
	trees, _ := s.Scan(procs(1, 100, 102, 400, 401), []int32{100, 400}, t0)
	if mcp := trees[100].Children[0]; len(mcp.Children) != 0 {
		t.Fatalf("session 400 is inside the tree of session 100: %+v", mcp.Children[0])
	}
	if b := trees[400]; b == nil || len(b.Children) != 1 || b.Children[0].PID != 401 {
		t.Fatalf("tree of session 400: %+v", b)
	}
}

func TestExitedProcessStaysForRetention(t *testing.T) {
	s, _ := newTestScanner(cmdlines)
	s.Scan(procs(1, 100, 101, 102, 104), []int32{100}, t0)

	// The Bash tool and its child exit.
	trees, _ := s.Scan(procs(1, 100, 102), []int32{100}, t0.Add(time.Second))
	a := trees[100]
	if len(a.Children) != 2 {
		t.Fatalf("children %v", names(a))
	}
	bash := a.Children[1]
	if bash.ExitTime == nil || bash.Name != "go test ./..." || len(bash.Children) != 1 || bash.Children[0].ExitTime == nil {
		t.Fatalf("exited bash: %+v", bash)
	}

	trees, _ = s.Scan(procs(1, 100, 102), []int32{100}, t0.Add(ExitedRetention+2*time.Second))
	if got := names(trees[100]); len(got) != 1 {
		t.Fatalf("exited processes must go after retention: %v", got)
	}
}

func TestListProcsFindsSelf(t *testing.T) {
	ps, err := ListProcs()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.PID == int32(os.Getpid()) {
			if p.PPID != int32(os.Getppid()) || p.Start.IsZero() {
				t.Fatalf("self: %+v", p)
			}
			return
		}
	}
	t.Fatal("own PID not in process table")
}
