// Claude-esp streams Claude Code's hidden output (thinking, tool calls, subagents)
// to a separate terminal in real-time.
//
// Usage:
//
//	claude-esp              # Watch all active sessions
//	claude-esp -n           # Skip history, live only
//	claude-esp -s <ID>      # Watch a specific session
//	claude-esp -a           # List active sessions
//	claude-esp -l           # List recent sessions
//
// See https://github.com/phiat/claude-esp for full documentation.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phiat/claude-esp/internal/parser"
	"github.com/phiat/claude-esp/internal/tui"
	"github.com/phiat/claude-esp/internal/watcher"
)

var (
	version = "0.7.1"
)

func main() {
	// Flags
	sessionID := flag.String("s", "", "Watch a specific session by ID")
	listSessions := flag.Bool("l", false, "List recent sessions")
	listActive := flag.Bool("a", false, "List active sessions (modified in last 5 min)")
	skipHistory := flag.Bool("n", false, "Start from newest (skip history, live only)")
	pollMs := flag.Int("p", 500, "Poll interval in milliseconds (min 100)")
	activeWindowStr := flag.String("w", "5m", "Active window duration (e.g. 30s, 2m, 5m)")
	maxSessions := flag.Int("m", 0, "Max sessions to show in tree (0=unlimited)")
	collapseAfterStr := flag.String("c", "0", "Auto-collapse sessions inactive ≥ this duration (0=disabled, e.g. 2m)")
	debugAll := flag.Bool("D", false, "Debug: surface raw type:subtype for every JSONL line type the parser would otherwise drop")
	showVersion := flag.Bool("v", false, "Show version")
	showHelp := flag.Bool("h", false, "Show help")

	flag.Parse()

	parser.DebugAll = *debugAll

	if *showHelp {
		printHelp()
		return
	}

	if *showVersion {
		fmt.Printf("claude-esp v%s\n", version)
		return
	}

	// Parse active window duration
	activeWindow, err := time.ParseDuration(*activeWindowStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid active window duration %q: %v\n", *activeWindowStr, err)
		os.Exit(1)
	}

	// Parse collapse-after duration (0 = disabled)
	var collapseAfter time.Duration
	if *collapseAfterStr != "0" && *collapseAfterStr != "" {
		collapseAfter, err = time.ParseDuration(*collapseAfterStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid collapse duration %q: %v\n", *collapseAfterStr, err)
			os.Exit(1)
		}
	}

	if *listActive {
		sessions, err := watcher.ListActiveSessions(activeWindow)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(sessions) == 0 {
			fmt.Printf("No active sessions (none modified in last %s)\n", activeWindow)
			return
		}
		fmt.Println("Active sessions:")
		for _, s := range sessions {
			status := "  "
			if s.IsActive {
				status = "● "
			}
			fmt.Printf("  %s%s  %s\n", status, s.ID[:min(12, len(s.ID))], truncatePath(s.ProjectPath, 40))
		}
		return
	}

	if *listSessions {
		sessions, err := watcher.ListSessions(10)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Recent sessions:")
		for _, s := range sessions {
			status := "  "
			if s.IsActive {
				status = "● "
			}
			fmt.Printf("  %s%s  %s  %s\n", status, s.Modified.Format("15:04:05"), s.ID[:min(12, len(s.ID))], truncatePath(s.ProjectPath, 30))
		}
		return
	}

	// Validate poll interval
	pollInterval := time.Duration(*pollMs) * time.Millisecond
	if pollInterval < 100*time.Millisecond {
		pollInterval = 100 * time.Millisecond
	}

	// Run TUI
	model := tui.NewModel(*sessionID, *skipHistory, pollInterval, activeWindow, *maxSessions, collapseAfter)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func truncatePath(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return "..." + s[len(s)-max+3:]
}

func printHelp() {
	fmt.Printf(`claude-esp v%s

Stream Claude Code's hidden output (thinking, tool calls, subagents)
to a separate terminal.

USAGE:
    claude-esp [OPTIONS]

OPTIONS:
    -s <ID>     Watch a specific session by ID
    -l          List recent sessions
    -a          List active sessions
    -n          Start from newest (skip history, live only)
    -p <ms>     Poll interval in ms, fallback mode only (default 500, min 100)
    -w <dur>    Active window duration (default 5m, e.g. 30s, 2m, 10m)
    -m <N>      Max sessions to show in tree (default 0=unlimited)
    -c <dur>    Auto-collapse sessions inactive ≥ dur (0=disabled, e.g. 2m, 30s)
    -D          Debug: show raw type:subtype for every JSONL line we'd drop
    -v          Show version
    -h          Show this help

ENVIRONMENT:
    CLAUDE_HOME     Override Claude config directory (default: ~/.claude)

KEYBINDINGS:
    t           Toggle thinking visibility
    i           Toggle tool input visibility
    o           Toggle tool output visibility
    a           Toggle auto-scroll
    h           Hide/show tree pane
    A           Toggle auto-discovery of new sessions
    x/d         Remove selected session (in tree)
    tab         Switch focus between tree and stream
    j/k         Navigate (tree) or scroll (stream)
    space       On agent: toggle visibility · On session: collapse/expand (pins on manual expand)
    g/G         Go to top/bottom of stream
    q           Quit

USAGE:
    # In one terminal, run Claude Code as normal
    claude

    # In another terminal, run the watcher
    claude-esp

The watcher will automatically find the most recent active session.
`, version)
}
