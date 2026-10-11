package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/map588/clanktop/internal/model"
)

// sessionFile is the format of ~/.claude/sessions/<pid>.json.
// The directory also holds <pid>.<hash>.key files. They are secrets and are never read.
type sessionFile struct {
	PID       int32  `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	StartedAt int64  `json:"startedAt"` // Unix milliseconds
	UpdatedAt int64  `json:"updatedAt"` // Unix milliseconds
}

// ListSessions reads the session registry in dir. It drops entries whose process
// does not exist. The oldest session is first.
// ponytail: a stale entry whose PID was reused by another process shows as a session;
// compare the registry procStart with the process start time if that occurs.
func ListSessions(dir string) []model.Session {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []model.Session
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var f sessionFile
		if json.Unmarshal(data, &f) != nil || f.PID <= 0 || f.SessionID == "" || !alive(f.PID) {
			continue
		}
		out = append(out, model.Session{
			PID:       f.PID,
			SessionID: f.SessionID,
			Cwd:       f.Cwd,
			Kind:      f.Kind,
			Status:    f.Status,
			Name:      f.Name,
			Version:   f.Version,
			StartedAt: time.UnixMilli(f.StartedAt),
			UpdatedAt: time.UnixMilli(f.UpdatedAt),
		})
	}
	slices.SortFunc(out, func(a, b model.Session) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

func alive(pid int32) bool {
	err := syscall.Kill(int(pid), 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
