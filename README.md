# clanktop

A terminal-based real-time process observer for your clankers. Watch what your clanker is actually doing at the OS level, which processes it spawns, what files it touches, how deep the agent hierarchy goes, and whether anything is leaking.

Built for debugging [Claude Code](https://claude.ai/code) sessions, but designed to support other clients in the future.

## Why

When an clanker runs on your machine, it spawns shells, reads files, invokes tools, and sometimes creates process trees several levels deep. There is no easy way to see this activity in real time. 
`clanktop` enables this.

**What clanktop can help find:**

- What processes is my agent spawning right now?
- What files is it reading and writing?
- Which MCP servers are running and consuming memory?
- Is something leaking processes or accumulating zombies?
- What tool calls is the agent making, and with what arguments?
- Which config/prompt files did it load?

## Installation

Requires Go 1.22+ and macOS (Apple Silicon or Intel).

```bash
git clone https://github.com/map588/clanktop.git
cd clanktop
go build -o clanktop ./cmd/clanktop/
```

Optionally install to your PATH:

```bash
go install ./cmd/clanktop/
```

Or build with version info:

```bash
go build -ldflags="-X main.version=$(git describe --tags --always)" -o clanktop ./cmd/clanktop/
```

## Usage

### Basic

Start clanktop while a Claude Code session is running in another terminal:

```bash
./clanktop
```

It auto-detects the Claude Code process and attaches. If none is running, it waits.

### Options

```
--pid <PID>             Attach to a specific process instead of auto-detecting
--poll-interval <dur>   Process polling interval (default: 20ms)
--no-color              Disable colors
--dump                  Print a single JSON snapshot to stdout and exit
--version               Print version and exit
```

### Keyboard Controls

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle focus between panels |
| `Up` / `Down` / `j` / `k` | Move cursor within focused panel |
| `Enter` | Show detail view (or open file in `$EDITOR` for config panel) |
| `Esc` | Close detail/help overlay |
| `p` | Pause/resume display |
| `q` / `Ctrl+C` | Quit |
| `?` | Help overlay |

## Panels

### Agent Tree (top left)

Hierarchical view of all processes under the AI agent. Shows:

- Process name, PID, and memory usage
- Color-coded roles: orchestrator (white bold), MCP servers (yellow), tool processes (green)
- Exited processes with `[exited]` tag stay visible for session history
- Shell wrappers annotated with the actual command from Claude Code's logs (e.g. `sleep 3 && echo "done"` instead of `zsh`)

The tree is **append-only**: processes are added chronologically and never reordered, so the display is stable even during heavy tool use.

### Config & Prompts (top right)

Lists all prompt and configuration files discovered at startup:

- `~/.claude/CLAUDE.md`, `~/.claude/settings.json`
- Project-level `CLAUDE.md`, `.cursorrules`
- Project-specific config in `~/.claude/projects/<project>/`

Shows modification age for each file. Press `Enter` to open the selected file in `$EDITOR`.

### Live Tool Calls (middle)

Real-time feed of tool invocations from Claude Code's session log (JSONL). Shows:

- Timestamp, tool name, and arguments
- Bash commands, Agent spawns, MCP tool calls
- File operations (Read, Write, Edit, Grep, Glob) are routed to the File Activity panel instead

Latest calls appear at top. Scroll with arrow keys when focused.

### Process Stats (bottom left)

Aggregate counters:

- Total, running, sleeping, zombie, and exited process counts
- Total CPU% and RSS memory across the agent tree
- Process spawn rate (per second)
- Leak detection alerts (zombie accumulation, shell accumulation, runaway spawn rate)

### File Activity (bottom right)

Deduplicated list of files the agent has accessed, with operation tags:

- `[R]` Read, `[W]` Write, `[E]` Edit, `[G]` Grep, `[F]` Glob (Find)
- Tags accumulate, a file that was read then edited shows `[RE]`
- Write/Edit operations highlighted in red, read-only in green
- Sorted by most recent activity

## How It Works

### Process Detection

clanktop polls the OS process table every 20ms using a single `ps -eo pid,ppid,rss,comm` call (~2ms per invocation). This catches any process that lives longer than 20ms. Shorter processes are still visible through JSONL log tailing.

### Tool Call Tracking

Claude Code writes every tool invocation to a JSONL session log at `~/.claude/projects/<project>/<session-id>.jsonl`. clanktop tails this file and extracts structured tool calls with full arguments. This is the primary data source, it captures everything regardless of process lifetime.

### MCP Server Detection

MCP servers are identified by reading Claude Code's configuration from `~/.claude.json` and project-level `.mcp.json`. The configured command names are matched against running processes. Common launchers (`npx`, `uvx`, `node`) are handled by also checking the first argument.

### Stable Tree

The TUI maintains an append-only tree structure. New processes are added as children of their parent; exited processes are marked but never removed or reordered. This prevents the tree from flickering during heavy activity and preserves a chronological record of what the agent did.

### Shell Annotation

Claude Code runs commands through shell wrappers (`zsh -c "source snapshot.sh && eval 'actual command'"`). clanktop cross-references the JSONL Bash tool calls with spawned shell processes to annotate tree nodes with the actual command being run.

## Debug Build

Build with debug logging enabled:

```bash
go build -tags debug -o clanktop-debug ./cmd/clanktop/
```

Debug output writes to `/tmp/clanktop-debug.log` and includes scanner ticks, process detection events, and tree merge operations.

## Limitations

- **macOS only** — uses `ps` command and gopsutil's libproc wrappers. Linux support is feasible but untested.
- **No elevated privileges**: kqueue `EVFILT_PROC` is blocked by SIP on macOS, so we use fast polling instead of event-driven process monitoring.
- **Sub-20ms processes**: processes that spawn and exit within a single poll interval are only visible through JSONL log data, not in the process tree.
- **Claude Code only**: the backend interface supports other clients, but only Claude Code is implemented.
- **No network inspection**: does not monitor API calls, token usage, or network traffic.

## License

MIT
