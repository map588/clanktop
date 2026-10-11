// Package collector polls all data sources and keeps the state of each agent session.
package collector

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/map588/clanktop/internal/backend"
	"github.com/map588/clanktop/internal/logtailer"
	"github.com/map588/clanktop/internal/model"
	"github.com/map588/clanktop/internal/process"
)

// IdleAfter is how long a subagent without a pending tool call and without an
// end_turn record stays "running". An interrupted subagent never writes end_turn.
const IdleAfter = 2 * time.Minute

type Collector struct {
	be        backend.ClientBackend
	pidFilter int32
	scanner   *process.Scanner

	mu       sync.Mutex
	sessions map[int32]*session // By agent PID
}

type session struct {
	info   model.Session
	main   *agentLog
	subs   map[string]*agentLog // By log path
	tree   *model.ProcessInfo
	open   []model.OpenFile
	leak   process.LeakDetector
	alerts []string
}

type agentLog struct {
	path string
	lr   *logtailer.LineReader
	log  logtailer.Log
	meta logtailer.Meta
}

// New makes a collector. A non-zero pidFilter keeps only the session of that PID.
func New(be backend.ClientBackend, pidFilter int32) *Collector {
	return &Collector{
		be:        be,
		pidFilter: pidFilter,
		scanner:   process.NewScanner(be),
		sessions:  map[int32]*session{},
	}
}

// Run polls until ctx is done. The process tree is the cheapest source and
// changes fastest, so it has the shortest interval.
func (c *Collector) Run(ctx context.Context, procInterval time.Duration) {
	c.PollOnce(time.Now())
	tick := func(d time.Duration) *time.Ticker { return time.NewTicker(d) }
	sessions, logs, procs, sample, open := tick(time.Second), tick(500*time.Millisecond),
		tick(procInterval), tick(time.Second), tick(2*time.Second)
	defer func() {
		for _, t := range []*time.Ticker{sessions, logs, procs, sample, open} {
			t.Stop()
		}
		c.close()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sessions.C:
			c.PollSessions()
		case <-logs.C:
			c.PollLogs()
		case now := <-procs.C:
			c.PollProcs(now)
		case now := <-sample.C:
			c.scanner.Sample(now)
		case <-open.C:
			c.PollOpenFiles()
		}
	}
}

// PollOnce reads every source one time. The CPU percentages need a second
// sample, so they are zero after only this call.
func (c *Collector) PollOnce(now time.Time) {
	c.PollSessions()
	c.PollLogs()
	c.PollProcs(now)
	c.scanner.Sample(now)
	c.PollOpenFiles()
}

// SampleCPU takes a CPU sample and updates the trees with it.
func (c *Collector) SampleCPU(now time.Time) {
	c.scanner.Sample(now)
	c.PollProcs(now)
}

func (c *Collector) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessions {
		s.closeLogs()
	}
}

// PollSessions reads the session registry. A session whose sessionId changed
// (after /clear or a resume) starts again with the new log.
func (c *Collector) PollSessions() {
	list := c.be.Sessions()
	c.mu.Lock()
	defer c.mu.Unlock()
	live := map[int32]bool{}
	for _, info := range list {
		if c.pidFilter != 0 && info.PID != c.pidFilter {
			continue
		}
		live[info.PID] = true
		s := c.sessions[info.PID]
		if s == nil {
			s = &session{subs: map[string]*agentLog{}}
			c.sessions[info.PID] = s
		} else if s.info.SessionID != info.SessionID {
			s.closeLogs()
		}
		s.info = info
	}
	for pid, s := range c.sessions {
		if !live[pid] {
			s.closeLogs()
			delete(c.sessions, pid)
		}
	}
}

func (s *session) closeLogs() {
	if s.main != nil {
		s.main.lr.Close()
		s.main = nil
	}
	for _, a := range s.subs {
		a.lr.Close()
	}
	s.subs = map[string]*agentLog{}
}

// PollLogs reads the new lines of each session log and subagent log.
func (c *Collector) PollLogs() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessions {
		if s.main == nil {
			s.main = openLog(c.be.LogPath(s.info.SessionID))
			if s.main == nil {
				continue
			}
		}
		s.main.read()
		// ponytail: a finished subagent log stays open; close idle ones if file descriptors run short.
		for _, path := range c.be.SubAgentLogs(s.main.path, s.info.SessionID) {
			if s.subs[path] == nil {
				if a := openLog(path); a != nil {
					a.meta = logtailer.ReadMeta(path)
					s.subs[path] = a
				}
			}
		}
		for _, a := range s.subs {
			a.read()
		}
	}
}

func openLog(path string) *agentLog {
	if path == "" {
		return nil
	}
	lr, err := logtailer.OpenLineReader(path)
	if err != nil {
		return nil
	}
	return &agentLog{path: path, lr: lr}
}

func (a *agentLog) read() {
	_ = a.lr.ReadLines(a.log.Apply)
}

// PollProcs builds the process tree of each session.
func (c *Collector) PollProcs(now time.Time) {
	procs, err := process.ListProcs()
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	roots := make([]int32, 0, len(c.sessions))
	for pid := range c.sessions {
		roots = append(roots, pid)
	}
	trees, news := c.scanner.Scan(procs, roots, now)
	for pid, s := range c.sessions {
		s.tree = trees[pid]
		s.alerts = nil
		if s.tree != nil {
			s.alerts = s.leak.Check(s.tree, news[pid], now)
		}
	}
}

// PollOpenFiles lists the files that the processes of each session hold open.
func (c *Collector) PollOpenFiles() {
	c.mu.Lock()
	owner := map[int32]*session{}
	var pids []int32
	for _, s := range c.sessions {
		walkLive(s.tree, func(n *model.ProcessInfo) {
			owner[n.PID] = s
			pids = append(pids, n.PID)
		})
	}
	c.mu.Unlock()

	files, err := process.OpenFiles(pids)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessions {
		s.open = nil
	}
	for _, f := range files {
		if s := owner[f.PID]; s != nil && c.be.FileAccessFilter(f.Path) && c.sessions[s.info.PID] == s {
			s.open = append(s.open, f)
		}
	}
}

func walkLive(n *model.ProcessInfo, fn func(*model.ProcessInfo)) {
	if n == nil || n.ExitTime != nil {
		return
	}
	fn(n)
	for _, c := range n.Children {
		walkLive(c, fn)
	}
}

// Snapshot returns a copy of the state of all sessions, the oldest session first.
func (c *Collector) Snapshot(now time.Time) []model.AgentView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.AgentView, 0, len(c.sessions))
	for _, s := range c.sessions {
		v := model.AgentView{
			Session:   s.info,
			Tree:      s.tree,
			OpenFiles: slices.Clone(s.open),
			Alerts:    slices.Clone(s.alerts),
		}
		if s.main == nil {
			if s.info.Status == "busy" {
				v.Alerts = append(v.Alerts, "No session log found for a busy session. The Claude Code log layout may have changed.")
			}
		} else {
			l := &s.main.log
			v.LogPath, v.Title, v.LastPrompt, v.Calls = s.main.path, l.Title, l.LastPrompt, slices.Clone(l.Calls)
			if l.Lines > 20 && l.Known == 0 {
				v.Alerts = append(v.Alerts, fmt.Sprintf("Session log format not recognized (Claude Code %s).", s.info.Version))
			}
		}
		for path, a := range s.subs {
			v.SubAgents = append(v.SubAgents, model.SubAgent{
				ID:          strings.TrimPrefix(strings.TrimSuffix(filepath.Base(path), ".jsonl"), "agent-"),
				Type:        a.meta.AgentType,
				Description: a.meta.Description,
				ToolUseID:   a.meta.ToolUseID,
				Start:       a.log.FirstTime,
				Running:     !a.log.EndTurn && (a.log.Pending() || now.Sub(a.log.LastTime) < IdleAfter),
				Calls:       slices.Clone(a.log.Calls),
			})
		}
		slices.SortFunc(v.SubAgents, func(a, b model.SubAgent) int { return a.Start.Compare(b.Start) })
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b model.AgentView) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return int(a.PID - b.PID)
	})
	return out
}
