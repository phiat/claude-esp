# claude-esp: Watch Claude Code's Hidden Output in Real-Time

When working with Claude Code in the terminal, you spend a lot of time pressing `Ctrl+O` to peek at tool outputs and thinking. The collapsed interface keeps things tidy, but when you're debugging or just curious about what's happening under the hood, you want to see everything.

**claude-esp** is a small TUI that streams Claude Code's internal activity to a second terminal. It parses the JSONL files that Claude Code writes to `~/.claude/projects/` and displays thinking blocks, tool calls, and their results as they happen.

## The Problem

Claude Code collapses tool outputs and thinking by default. This is sensible for a clean interface, but:

- You have to toggle visibility constantly when debugging
- You can't easily watch what a subagent is doing
- Scrolling back through collapsed history is tedious

The simple fix: run a watcher in a split pane that shows everything in real-time.

## What It Shows

The TUI displays four types of content:

1. **Thinking blocks** - Claude's extended thinking (the `<thinking>` content)
2. **Tool inputs** - What tool was called and with what parameters
3. **Tool outputs** - The results that came back
4. **Subagent activity** - Any spawned Task agents and their operations

Each item shows which agent generated it (Main or Agent-abc123), with visual indicators for active vs idle agents.

## How It Works

Claude Code stores conversation transcripts as JSONL files:

```
~/.claude/projects/<project-path>/<session-id>.jsonl
~/.claude/projects/<project-path>/<session-id>/subagents/agent-<id>.jsonl
```

The watcher:

1. Discovers sessions modified in the last 5 minutes
2. Polls JSONL files every 500ms for new lines
3. Parses each JSON line looking for `thinking`, `tool_use`, and `tool_result` blocks
4. Renders them in a scrollable viewport with filtering controls

For long-running sessions, it auto-skips history (keeping only the last ~10 lines) so you're not buried in old output.

## Usage

Run Claude Code in one terminal, claude-esp in another:

```bash
# Terminal 1
claude

# Terminal 2
claude-esp
```

It auto-discovers active sessions. If you have multiple sessions running, they all show up in the tree view on the left.

### Flags

```
-s <ID>   Watch a specific session
-n        Skip history, live output only
-a        List active sessions
-l        List recent sessions
```

### Keybindings

The TUI has filtering controls:

- `t` - Toggle thinking visibility
- `i` - Toggle tool input visibility
- `o` - Toggle tool output visibility
- `a` - Toggle auto-scroll
- `h` - Hide/show the session tree
- `space` - Toggle individual sessions/agents in tree

Navigation:
- `j/k` - Scroll or navigate
- `g/G` - Jump to top/bottom
- `tab` - Switch between tree and stream panes

## Example tmux Setup

```bash
tmux new-session -s claude \; \
  split-window -h \; \
  send-keys 'claude-esp' C-m \; \
  select-pane -L \; \
  send-keys 'claude' C-m
```

This creates a side-by-side layout with Claude Code on the left and the watcher on the right.

## Technical Notes

The parser handles Claude's message format, extracting content blocks from assistant messages and tool results from user messages. Tool inputs get formatted nicely - for Bash commands it shows the command, for Grep it shows the pattern, for Write it shows the file path and size.

Subagents are discovered dynamically. When Claude spawns a Task agent, the watcher notices the new JSONL file and starts tailing it.

The TUI is built with [Bubbletea](https://github.com/charmbracelet/bubbletea) and [Lipgloss](https://github.com/charmbracelet/lipgloss). The viewport component handles scrolling, and the tree component handles session/agent selection.

## Installation

```bash
git clone https://github.com/phiat/claude-esp.git
cd claude-esp
go build -o claude-esp .
./claude-esp
```

## Why "ESP"?

It's a tool for seeing what Claude is "thinking" - extrasensory perception for your AI coding assistant.

---

That's it. A small utility for a specific workflow need. If you spend time toggling Claude Code's output visibility, this might save you some keystrokes.
