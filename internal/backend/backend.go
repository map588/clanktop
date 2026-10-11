package backend

import "github.com/map588/clanktop/internal/model"

// ClientBackend holds the knowledge that is specific to one AI client.
type ClientBackend interface {
	Name() string
	// Sessions returns the live sessions of the client.
	Sessions() []model.Session
	// LogPath returns the session log file, or "" if it does not exist yet.
	LogPath(sessionID string) string
	// SubAgentLogs returns the log files of the subagents of a session.
	SubAgentLogs(logPath, sessionID string) []string
	// ClassifyChild gives the role of a direct child of the agent process.
	ClassifyChild(proc *model.ProcessInfo) model.ProcessRole
	// Hidden is true for processes that the tree must not show.
	Hidden(proc *model.ProcessInfo) bool
	// Label gives a short display name for a process.
	Label(proc *model.ProcessInfo) string
	// FileAccessFilter is false for system paths that are noise in the open-files view.
	FileAccessFilter(path string) bool
}
