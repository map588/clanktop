# clanktop

A terminal monitor for the AI coding agents that run on your machine. It shows every live Claude Code session, its subagents, the tool calls each agent makes, the files each agent touches, and the processes that each session starts.

Built for [Claude Code](https://claude.ai/code). The backend interface can support other clients later.

## Why

An agent on your machine runs shell commands, reads and edits files, starts MCP servers, and starts subagents. Usually more than one agent runs at the same time. clanktop shows all of them in one screen.

**What clanktop shows:**

- Which Claude Code sessions run now, which are busy, and what each one does at this moment
- Which subagents each session started, and whether they still run
- Each tool call with its arguments, its duration, and whether it failed
- Which files each agent read, wrote, edited, or searched
- Which files the processes of a session hold open now (MCP servers, language servers, test runners)
- Which processes each session started, with CPU and memory, and leak alerts
- Which prompt and config files apply to a session

## Installation

Requires Go 1.26+ and macOS (Apple Silicon or Intel).

```bash
git clone https://github.com/map588/clanktop.git
cd clanktop
go build -o clanktop ./cmd/clanktop/
```

To install it on your PATH:

```bash
go install ./cmd/clanktop/
```

To build with version information:

```bash
go build -ldflags="-X main.version=$(git describe --tags --always)" -o clanktop ./cmd/clanktop/
```

## Usage

Start clanktop in a terminal:

```bash
./clanktop
```

clanktop finds all live Claude Code sessions. It needs no PID and no root access. When no session runs, it waits and shows sessions as they start.

### Options

```
--pid <PID>             Show only the session of this agent process
--poll-interval <dur>   Process tree poll interval (default: 100ms)
--no-color              Disable colors
--dump                  Print one JSON snapshot of all sessions to stdout and exit
--version               Print version and exit
```

### Keys

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Change the focused panel |
| `Up` / `Down` / `j` / `k` | Select an agent (Agents panel) or a line (other panels) |
| `Enter` | Show the details of the selected line |
| `e` | Open the selected file in `$EDITOR` |
| `c` | Show the prompt and config files of the selected session |
| `p` | Pause or resume |
| `?` | Help |
| `q` / `Ctrl+C` | Quit |

## Panels

### Agents (left)

A tree of agents, like the process tree in htop. Under each session are:

- The subagents that it started. A subagent that another subagent started is under that subagent.
- The background session that it parked its work in.
- The sessions that its processes started, for example `claude -p` in a Bash call or in a script that starts workers.

A subagent stays in the tree while it runs and for 10 minutes after it stops. A stopped subagent is gray.

- `●` busy, `○` idle or other status, from the session registry
- The session name, `bg` for a background session, and the tool call that runs now with its elapsed time
- `⚠` when the session has an alert

The other panels show the selected agent. When you select a session, the Files panel also includes the files of its subagents.

### Activity (top right)

The tool calls of the selected agent, the newest first: start time, tool, duration, and a summary of the arguments. `▶` marks a call that runs now. `✗` marks a call that failed. Press `Enter` to see the full input of a call.

### Files touched · open (bottom middle)

The files that the agent touched, the most recent first:

- `R` read, `W` write, `E` edit, `S` search (the `path` of a Grep or Glob call)
- The number of calls on the file, and the time since the last call
- Write and edit lines are red

Under the separator are the files that the processes of the session hold open now, at most five for each process.

### Processes (bottom right)

The process tree of the session: MCP and language servers (yellow), Bash tool commands (green, with the command as the label), CPU, and memory. A process that exited stays in the tree for 60 seconds. Leak alerts show at the top: zombie accumulation, shell accumulation, and runaway spawn rate.

## How It Works

clanktop reads files that Claude Code writes for its own use. They are not a public interface. A Claude Code update can change them. In that case clanktop shows an alert, not an empty screen: "No session log found" or "Session log format not recognized". After a Claude Code update, run `clanktop --dump` once to check that your sessions still appear.

### Sessions

Claude Code writes one file for each live session to `~/.claude/sessions/<pid>.json`: the session ID, the working directory, the kind (interactive or bg), the status (busy or idle), and the name. clanktop reads this directory every second. It does not read the `*.key` files in this directory. It ignores spare processes (`"spare": true`), which wait for work. An interactive session whose `parkedJobId` is the `jobId` of a background session is the parent of that session.

### Tool calls and files

Each session writes a JSONL log to `~/.claude/projects/<project>/<session-id>.jsonl`. clanktop reads the full log when it starts and then reads new lines every 500 ms. It pairs each `tool_use` record with its `tool_result` record to get the duration and the error status. The file operations come from the arguments of the Read, Write, Edit, MultiEdit, NotebookEdit, Grep, and Glob calls.

Subagents run inside the agent process. They are not separate processes. Each subagent writes its own log to `<project>/<session-id>/subagents/agent-<id>.jsonl`, with a `.meta.json` file that gives its type, its description, and the ID of the Agent call that started it. clanktop finds that call in the session log or in the log of another subagent to put the subagent in the tree.

### Processes

clanktop reads the kernel process table with one `sysctl` call (about 0.1 ms) at each poll. It builds one tree for each session. A tree stops at a process that is another session. The parent of a session is the nearest ancestor process that is a session. The search stops at the Claude Code daemon (`claude daemon run`): the first session that needs the daemon starts it, but the daemon then hosts the background sessions of all sessions. A direct child of the agent that is a shell is a Bash tool call, a hook, or the status line. All other direct children are MCP or language servers. clanktop reads CPU and memory once per second, and the command line of each process one time.

### Open files

Claude Code opens and closes each file within milliseconds, so the agent process itself holds no project files open. clanktop runs `lsof` on the processes of each session every 2 seconds. This shows the files that servers and test runners keep open.

## Limitations

- **macOS only.** clanktop uses `sysctl kern.proc.all`, `proc_pidinfo` (through gopsutil), and `lsof`.
- **Claude Code only.** The backend interface can support other clients, but only Claude Code is implemented.
- **Private file formats.** See "How It Works".
- **No network inspection.** clanktop does not monitor API calls, token usage, or network traffic.

## License

MIT
