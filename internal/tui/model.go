package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/phiat/claude-esp/internal/parser"
	"github.com/phiat/claude-esp/internal/watcher"
)

// Focus indicates which pane has focus
type Focus int

const (
	FocusTree Focus = iota
	FocusStream
)

// Model is the main TUI model
type Model struct {
	tree        *TreeView
	stream      *StreamView
	watcher     *watcher.Watcher
	focus       Focus
	showTree    bool
	width       int
	height      int
	treeWidth   int
	sessionID   string
	skipHistory bool
	err         error
	quitting    bool
}

// NewModel creates a new TUI model
func NewModel(sessionID string, skipHistory bool) *Model {
	return &Model{
		tree:        NewTreeView(),
		stream:      NewStreamView(),
		focus:       FocusStream,
		showTree:    true,
		treeWidth:   25,
		sessionID:   sessionID,
		skipHistory: skipHistory,
	}
}

// Messages
type (
	tickMsg         time.Time
	streamItemMsg   parser.StreamItem
	newAgentMsg     watcher.NewAgentMsg
	newSessionMsg   watcher.NewSessionMsg
	errMsg          error
	watcherReadyMsg struct{}
)

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.initWatcher(),
		m.tick(),
	)
}

func (m *Model) initWatcher() tea.Cmd {
	return func() tea.Msg {
		w, err := watcher.New(m.sessionID)
		if err != nil {
			return errMsg(err)
		}
		m.watcher = w

		// Configure skip history before starting
		if m.skipHistory {
			w.SetSkipHistory(true)
		}

		// Add all sessions and their agents to the tree
		for _, session := range w.GetSessions() {
			m.tree.AddSession(session.ID, session.ProjectPath)
			for agentID := range session.Subagents {
				m.tree.AddAgent(session.ID, agentID)
			}
		}

		// Start watching
		w.Start()
		return watcherReadyMsg{}
	}
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()

	case tickMsg:
		cmds = append(cmds, m.tick())
		cmds = append(cmds, m.pollWatcher())
		m.updateActivityStatus()

	case streamItemMsg:
		m.stream.AddItem(parser.StreamItem(msg))
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case newAgentMsg:
		m.tree.AddAgent(msg.SessionID, msg.AgentID)
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case newSessionMsg:
		m.tree.AddSession(msg.SessionID, msg.ProjectPath)
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case errMsg:
		m.err = msg

	case watcherReadyMsg:
		// Initial sync of enabled filters
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) pollWatcher() tea.Cmd {
	if m.watcher == nil {
		return nil
	}

	return func() tea.Msg {
		select {
		case item := <-m.watcher.Items:
			return streamItemMsg(item)
		case agent := <-m.watcher.NewAgent:
			return newAgentMsg(agent)
		case session := <-m.watcher.NewSession:
			return newSessionMsg(session)
		case err := <-m.watcher.Errors:
			return errMsg(err)
		default:
			return nil
		}
	}
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		if m.watcher != nil {
			m.watcher.Stop()
		}
		return tea.Quit

	case "h":
		m.showTree = !m.showTree
		m.updateLayout()

	case "tab":
		if m.focus == FocusTree {
			m.focus = FocusStream
		} else {
			m.focus = FocusTree
		}

	case "t":
		m.stream.ToggleThinking()

	case "i":
		m.stream.ToggleToolInput()

	case "o":
		m.stream.ToggleToolOutput()

	case "a":
		m.stream.ToggleAutoScroll()

	case "j", "down":
		if m.focus == FocusTree {
			m.tree.MoveDown()
		} else {
			m.stream.ScrollDown(3)
		}

	case "k", "up":
		if m.focus == FocusTree {
			m.tree.MoveUp()
		} else {
			m.stream.ScrollUp(3)
		}

	case " ", "enter":
		if m.focus == FocusTree {
			m.tree.Toggle()
			m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())
		}

	case "g":
		// Go to top
		m.stream.ScrollUp(9999)

	case "G":
		// Go to bottom and enable auto-scroll
		m.stream.ScrollDown(9999)
		if !m.stream.IsAutoScrollEnabled() {
			m.stream.ToggleAutoScroll()
		}

	case "x", "d":
		// Remove selected session (only when tree is focused)
		if m.focus == FocusTree && m.watcher != nil {
			sessionID := m.tree.GetSelectedSession()
			if sessionID != "" {
				m.watcher.RemoveSession(sessionID)
				m.tree.RemoveSession(sessionID)
				m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())
			}
		}

	case "A":
		// Toggle auto-discovery of new sessions
		if m.watcher != nil {
			m.watcher.ToggleAutoDiscovery()
		}
	}

	return nil
}

func (m *Model) updateActivityStatus() {
	if m.watcher == nil {
		return
	}
	// Check activity within last 30 seconds
	for _, info := range m.watcher.GetActivityInfo(30 * time.Second) {
		m.tree.UpdateActivity(info.SessionID, info.AgentID, info.IsActive)
	}
}

func (m *Model) updateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	// Reserve space for header and help bar
	contentHeight := m.height - 4

	if m.showTree {
		m.tree.SetSize(m.treeWidth, contentHeight)
		m.stream.SetSize(m.width-m.treeWidth-3, contentHeight) // -3 for borders/gap
	} else {
		m.stream.SetSize(m.width-2, contentHeight)
	}
}

// View renders the UI
func (m *Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress q to quit.\n", m.err)
	}

	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header with toggles
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// Main content
	if m.showTree {
		b.WriteString(m.renderWithTree())
	} else {
		b.WriteString(m.renderStreamOnly())
	}

	// Help bar
	b.WriteString("\n")
	b.WriteString(m.renderHelp())

	return b.String()
}

func (m *Model) renderHeader() string {
	// Toggle indicators
	thinking := m.renderToggle("Thinking", m.stream.IsThinkingEnabled(), "t")
	toolInput := m.renderToggle("Tools", m.stream.IsToolInputEnabled(), "i")
	toolOutput := m.renderToggle("Output", m.stream.IsToolOutputEnabled(), "o")
	autoScroll := m.renderToggle("Auto", m.stream.IsAutoScrollEnabled(), "a")
	treeToggle := m.renderToggle("Tree", m.showTree, "h")

	toggles := fmt.Sprintf("%s  %s  %s  %s  %s",
		thinking, toolInput, toolOutput, autoScroll, treeToggle)

	// Session count and auto-discovery status
	sessionInfo := ""
	if m.watcher != nil {
		sessions := m.watcher.GetSessions()
		autoDisc := ""
		if !m.watcher.IsAutoDiscoveryEnabled() {
			autoDisc = " [paused]"
		}
		if len(sessions) == 1 {
			for _, s := range sessions {
				sessionInfo = mutedStyle.Render(fmt.Sprintf("Session: %s%s", truncate(s.ID, 12), autoDisc))
			}
		} else {
			sessionInfo = mutedStyle.Render(fmt.Sprintf("%d sessions%s", len(sessions), autoDisc))
		}
	}

	// Build header
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf("%s  │  %s", toggles, sessionInfo),
	)

	return header
}

func (m *Model) renderToggle(name string, enabled bool, key string) string {
	checkbox := "☐"
	style := toggleOffStyle
	if enabled {
		checkbox = "☑"
		style = toggleOnStyle
	}
	return style.Render(fmt.Sprintf("%s %s[%s]", checkbox, name, key))
}

func (m *Model) renderWithTree() string {
	// Tree pane
	treeBorder := treeBorderStyle
	if m.focus == FocusTree {
		treeBorder = treeBorder.BorderForeground(primaryColor)
	}
	treePane := treeBorder.
		Width(m.treeWidth).
		Height(m.height - 4).
		Render(m.tree.View())

	// Stream pane
	streamBorder := streamBorderStyle
	if m.focus == FocusStream {
		streamBorder = streamBorder.BorderForeground(primaryColor)
	}
	streamPane := streamBorder.
		Width(m.width - m.treeWidth - 3).
		Height(m.height - 4).
		Render(m.stream.View())

	return lipgloss.JoinHorizontal(lipgloss.Top, treePane, " ", streamPane)
}

func (m *Model) renderStreamOnly() string {
	streamBorder := streamBorderStyle.BorderForeground(primaryColor)
	return streamBorder.
		Width(m.width - 2).
		Height(m.height - 4).
		Render(m.stream.View())
}

func (m *Model) renderHelp() string {
	var help string
	if m.focus == FocusTree {
		help = "j/k: navigate │ space: toggle │ x: remove │ A: auto-discover │ q: quit"
	} else {
		help = "j/k: scroll │ g/G: top/bottom │ A: auto-discover │ tab: tree │ q: quit"
	}
	return helpStyle.Render(help)
}
