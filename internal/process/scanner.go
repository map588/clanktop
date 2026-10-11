// Package process builds the process tree of each agent session.
package process

import (
	"slices"
	"time"

	gopsutil "github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/unix"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/model"
)

// ExitedRetention is how long the tree keeps a process after it exits.
const ExitedRetention = 60 * time.Second

// Proc is one entry of the kernel process table.
type Proc struct {
	PID, PPID int32
	State     string // Z (zombie), T (stopped), or "" (macOS p_stat does not tell running from sleeping)
	Comm      string // Executable name, truncated to 16 bytes by the kernel
	Start     time.Time
}

// ListProcs reads the process table with one sysctl call. It does not fork,
// so it is cheap enough to call every 100 ms.
func ListProcs() ([]Proc, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(kps))
	for _, k := range kps {
		state := ""
		switch k.Proc.P_stat {
		case 4: // SSTOP
			state = "T"
		case 5: // SZOMB
			state = "Z"
		}
		out = append(out, Proc{
			PID:   k.Proc.P_pid,
			PPID:  k.Eproc.Ppid,
			State: state,
			Comm:  unix.ByteSliceToString(k.Proc.P_comm[:]),
			Start: time.Unix(k.Proc.P_starttime.Sec, int64(k.Proc.P_starttime.Usec)*1000),
		})
	}
	return out, nil
}

// procInfo is what the scanner remembers about a live process in a tree.
type procInfo struct {
	start   time.Time
	cmdline []string
	cpuTime float64 // User + system seconds at the last sample
	sampled time.Time
	cpuPct  float64
	rss     uint64
}

type exitedProc struct {
	node *model.ProcessInfo
	root int32
}

// Scanner builds one process tree per session from successive process tables.
type Scanner struct {
	be      backend.ClientBackend
	info    map[int32]*procInfo
	prev    map[int32]int32 // PID -> root PID, for the processes of the last scan
	nodes   map[int32]*model.ProcessInfo
	exited  map[int32]*exitedProc
	cmdline func(pid int32) []string // Replaced in tests
}

func NewScanner(be backend.ClientBackend) *Scanner {
	return &Scanner{
		be:     be,
		info:   map[int32]*procInfo{},
		prev:   map[int32]int32{},
		nodes:  map[int32]*model.ProcessInfo{},
		exited: map[int32]*exitedProc{},
		cmdline: func(pid int32) []string {
			c, _ := (&gopsutil.Process{Pid: pid}).CmdlineSlice()
			return c
		},
	}
}

// Scan builds the tree of each root PID. It also returns the number of
// processes in each tree that the previous scan did not see, and for each
// root that a process of another root started, that other root.
func (s *Scanner) Scan(procs []Proc, roots []int32, now time.Time) (trees map[int32]*model.ProcessInfo, newCount map[int32]int, parents map[int32]int32) {
	byPID := make(map[int32]Proc, len(procs))
	kids := make(map[int32][]Proc)
	for _, p := range procs {
		byPID[p.PID] = p
		if p.PID != p.PPID {
			kids[p.PPID] = append(kids[p.PPID], p)
		}
	}

	trees = map[int32]*model.ProcessInfo{}
	newCount = map[int32]int{}
	cur := map[int32]int32{}
	nodes := map[int32]*model.ProcessInfo{}
	isRoot := map[int32]bool{}
	for _, r := range roots {
		isRoot[r] = true
	}

	var build func(p Proc, parent *model.ProcessInfo, root int32) *model.ProcessInfo
	build = func(p Proc, parent *model.ProcessInfo, root int32) *model.ProcessInfo {
		if _, dup := cur[p.PID]; dup {
			return nil
		}
		info := s.infoFor(p, parent != nil)
		node := &model.ProcessInfo{
			PID: p.PID, PPID: p.PPID, Name: p.Comm, Cmdline: info.cmdline, State: p.State,
			Start: p.Start, CPUPercent: info.cpuPct, RSS: info.rss,
		}
		switch {
		case parent == nil:
			node.Role = model.RoleOrchestrator
		case s.be.Hidden(node):
			return nil
		case parent.Role == model.RoleOrchestrator:
			node.Role = s.be.ClassifyChild(node)
			node.Name = s.be.Label(node)
		default:
			node.Role = parent.Role
			node.Name = s.be.Label(node)
		}
		cur[p.PID] = root
		nodes[p.PID] = node
		// The first scan of a session finds old processes. They are not spawns.
		_, seen := s.prev[p.PID]
		if _, rootSeen := s.prev[root]; !seen && rootSeen {
			newCount[root]++
		}
		for _, k := range kids[p.PID] {
			// A session can start another session, directly (claude -p) or
			// through the Claude Code daemon. Each session has its own tree.
			if isRoot[k.PID] {
				continue
			}
			if c := build(k, node, root); c != nil {
				node.Children = append(node.Children, c)
			}
		}
		sortByStart(node.Children)
		return node
	}
	for _, r := range roots {
		if p, ok := byPID[r]; ok {
			trees[r] = build(p, nil, r)
		}
	}

	// Processes of the last scan that are gone now have exited.
	for pid, root := range s.prev {
		if _, live := cur[pid]; live {
			continue
		}
		if n, ok := s.nodes[pid]; ok && s.exited[pid] == nil {
			e := *n
			e.Children, e.CPUPercent, e.ExitTime = nil, 0, &now
			s.exited[pid] = &exitedProc{node: &e, root: root}
		}
	}
	s.attachExited(trees, nodes, now)

	// Keep the info of hidden processes too, so that their cmdline is read once.
	for pid := range s.info {
		if _, alive := byPID[pid]; !alive {
			delete(s.info, pid)
		}
	}
	s.prev, s.nodes = cur, nodes
	return trees, newCount, s.parents(byPID, nodes, isRoot)
}

// parents finds, for each root, the nearest ancestor process that is another
// root. The walk stops at a process that hosts sessions for all sessions
// (the Claude Code daemon): the session that started the daemon did not start
// the sessions in it.
func (s *Scanner) parents(byPID map[int32]Proc, nodes map[int32]*model.ProcessInfo, isRoot map[int32]bool) map[int32]int32 {
	out := map[int32]int32{}
	for r := range isRoot {
		p, ok := byPID[r]
		for steps := 0; ok && steps < len(byPID); steps++ {
			if p, ok = byPID[p.PPID]; !ok || p.PID <= 1 {
				break
			}
			if isRoot[p.PID] {
				out[r] = p.PID
				break
			}
			if n := nodes[p.PID]; n != nil && s.be.HostsSessions(n) {
				break
			}
		}
	}
	return out
}

// attachExited adds each exited process under its parent: a live process, an
// exited process, or else the root of its tree.
func (s *Scanner) attachExited(trees map[int32]*model.ProcessInfo, live map[int32]*model.ProcessInfo, now time.Time) {
	copies := map[int32]*model.ProcessInfo{}
	for pid, e := range s.exited {
		if now.Sub(*e.node.ExitTime) > ExitedRetention || trees[e.root] == nil {
			delete(s.exited, pid)
			continue
		}
		c := *e.node
		c.Children = nil
		copies[pid] = &c
	}
	changed := map[*model.ProcessInfo]bool{}
	for pid, c := range copies {
		parent := live[c.PPID]
		if parent == nil {
			parent = copies[c.PPID]
		}
		if parent == nil {
			parent = trees[s.exited[pid].root]
		}
		parent.Children = append(parent.Children, c)
		changed[parent] = true
	}
	for p := range changed {
		sortByStart(p.Children)
	}
}

func sortByStart(nodes []*model.ProcessInfo) {
	slices.SortFunc(nodes, func(a, b *model.ProcessInfo) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return int(a.PID - b.PID)
	})
}

// infoFor returns the cached info of a process. It reads the cmdline once per
// process. The root needs no cmdline: its role and label are fixed.
func (s *Scanner) infoFor(p Proc, wantCmdline bool) *procInfo {
	info := s.info[p.PID]
	if info == nil || !info.start.Equal(p.Start) {
		info = &procInfo{start: p.Start}
		if wantCmdline {
			info.cmdline = s.cmdline(p.PID)
		}
		s.info[p.PID] = info
	}
	return info
}

// Sample reads RSS and CPU time of every process in the trees with
// proc_pidinfo. It costs more than Scan, so the collector calls it less often.
func (s *Scanner) Sample(now time.Time) {
	for pid, info := range s.info {
		p := &gopsutil.Process{Pid: pid}
		if m, err := p.MemoryInfo(); err == nil {
			info.rss = m.RSS
		}
		t, err := p.Times()
		if err != nil {
			continue
		}
		total := t.User + t.System
		if !info.sampled.IsZero() {
			if dt := now.Sub(info.sampled).Seconds(); dt > 0 {
				info.cpuPct = (total - info.cpuTime) / dt * 100
			}
		}
		info.cpuTime, info.sampled = total, now
	}
}
