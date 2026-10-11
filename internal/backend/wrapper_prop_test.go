package backend

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// wrap builds the command line that Claude Code uses for a Bash tool call.
// Claude Code quotes the command with single quotes and writes each
// embedded single quote as '"'"'.
func wrap(cmd string) []string {
	quoted := "'" + strings.ReplaceAll(cmd, "'", `'"'"'`) + "'"
	return []string{"/bin/zsh", "-c",
		"source /Users/x/.claude/shell-snapshots/snapshot-zsh-1-abc.sh 2>/dev/null || true && " +
			"setopt NO_EXTENDED_GLOB 2>/dev/null || true && eval " + quoted +
			" < /dev/null && pwd -P >| /tmp/claude-94bc-cwd"}
}

func TestExtractToolFromWrapperRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cmd := rapid.StringMatching(`[ -~\n]{1,40}`).Draw(t, "cmd")
		if got := ExtractToolFromWrapper(wrap(cmd)); got != cmd {
			t.Fatalf("ExtractToolFromWrapper(wrap(%q)) = %q", cmd, got)
		}
	})
}
