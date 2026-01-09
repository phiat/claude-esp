package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phiat/claude-watch/internal/tui"
	"github.com/phiat/claude-watch/internal/watcher"
)

var (
	version = "0.1.0"
)

func main() {
	// Flags
	sessionID := flag.String("s", "", "Watch a specific session by ID")
	listSessions := flag.Bool("l", false, "List recent sessions")
	listActive := flag.Bool("a", false, "List active sessions (modified in last 5 min)")
	showVersion := flag.Bool("v", false, "Show version")
	showHelp := flag.Bool("h", false, "Show help")

	flag.Parse()

	if *showHelp {
		printHelp()
		return
	}

	if *showVersion {
		fmt.Printf("claude-watch v%s\n", version)
		return
	}

	if *listActive {
		sessions, err := watcher.ListActiveSessions(5 * time.Minute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(sessions) == 0 {
			fmt.Println("No active sessions (none modified in last 5 minutes)")
			return
		}
		fmt.Println("Active sessions:")
		for _, s := range sessions {
			status := "  "
			if s.IsActive {
				status = "● "
			}
			fmt.Printf("  %s%s  %s\n", status, s.ID[:12], truncatePath(s.ProjectPath, 40))
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
			fmt.Printf("  %s%s  %s  %s\n", status, s.Modified.Format("15:04:05"), s.ID[:12], truncatePath(s.ProjectPath, 30))
		}
		return
	}

	// Run TUI
	model := tui.NewModel(*sessionID)
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
	fmt.Printf(`claude-watch v%s

Stream Claude Code's hidden output (thinking, tool calls, subagents)
to a separate terminal.

USAGE:
    claude-watch [OPTIONS]

OPTIONS:
    -s <ID>     Watch a specific session by ID
    -l          List recent sessions
    -a          List active sessions (modified in last 5 min)
    -v          Show version
    -h          Show this help

KEYBINDINGS:
    t           Toggle thinking visibility
    i           Toggle tool input visibility
    o           Toggle tool output visibility
    a           Toggle auto-scroll
    h           Hide/show tree pane
    tab         Switch focus between tree and stream
    j/k         Navigate (tree) or scroll (stream)
    space       Toggle agent visibility (in tree)
    g/G         Go to top/bottom of stream
    q           Quit

USAGE:
    # In one terminal, run Claude Code as normal
    claude

    # In another terminal, run the watcher
    claude-watch

The watcher will automatically find the most recent active session.
`, version)
}
