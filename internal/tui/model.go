package tui

import (
	"fmt"
	"os"
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
	tree               *TreeView
	stream             *StreamView
	watcher            *watcher.Watcher
	focus              Focus
	showTree           bool
	width              int
	height             int
	treeWidth          int
	sessionID          string
	skipHistory        bool
	pollInterval       time.Duration
	activeWindow       time.Duration
	maxSessions        int
	collapseAfter      time.Duration // 0 = disabled
	err                error
	quitting           bool
	totalInputTokens   int64
	totalOutputTokens  int64
	totalCacheCreation int64
	totalCacheRead     int64
}

// NewModel creates a new TUI model. If collapseAfter > 0, sessions inactive
// for that duration will auto-collapse in the tree (and be hidden from the
// stream). See tree.Toggle / Solo for the interactive counterpart.
func NewModel(sessionID string, skipHistory bool, pollInterval time.Duration, activeWindow time.Duration, maxSessions int, collapseAfter time.Duration) *Model {
	return &Model{
		tree:          NewTreeView(),
		stream:        NewStreamView(),
		focus:         FocusStream,
		showTree:      true,
		treeWidth:     30,
		sessionID:     sessionID,
		skipHistory:   skipHistory,
		pollInterval:  pollInterval,
		activeWindow:  activeWindow,
		maxSessions:   maxSessions,
		collapseAfter: collapseAfter,
	}
}

// Messages
type (
	tickMsg              time.Time
	streamItemMsg        parser.StreamItem
	newAgentMsg          watcher.NewAgentMsg
	newSessionMsg        watcher.NewSessionMsg
	newBackgroundTaskMsg watcher.NewBackgroundTaskMsg
	errMsg               error
	watcherReadyMsg      struct{}
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
		w, err := watcher.New(m.sessionID, m.pollInterval, m.activeWindow, m.maxSessions)
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
				agentType := session.SubagentTypes[agentID]
				m.tree.AddAgent(session.ID, agentID, agentType)
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
		item := parser.StreamItem(msg)
		// Session-title items update the tree label, not the stream.
		if item.Type == parser.TypeSessionTitle {
			m.tree.SetSessionTitle(item.SessionID, item.Content)
			break
		}
		// Accumulate token usage (includes history — shows total session cost)
		if item.InputTokens > 0 {
			m.totalInputTokens += item.InputTokens
		}
		if item.OutputTokens > 0 {
			m.totalOutputTokens += item.OutputTokens
		}
		if item.CacheCreationTokens > 0 {
			m.totalCacheCreation += item.CacheCreationTokens
		}
		if item.CacheReadTokens > 0 {
			m.totalCacheRead += item.CacheReadTokens
		}
		// Per-agent context size: latest snapshot, not a sum. The prompt
		// size for a turn is input + cache_creation + cache_read; output
		// tokens don't fill the context window.
		if item.Model != "" {
			ctx := item.InputTokens + item.CacheCreationTokens + item.CacheReadTokens
			if ctx > 0 {
				m.tree.UpdateContext(item.SessionID, item.AgentID, ctx, parser.ContextWindowFor(item.Model))
			}
		}
		m.stream.AddItem(item)
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case newAgentMsg:
		m.tree.AddAgent(msg.SessionID, msg.AgentID, msg.AgentType)
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case newSessionMsg:
		m.tree.AddSession(msg.SessionID, msg.ProjectPath)
		m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())

	case newBackgroundTaskMsg:
		m.tree.AddBackgroundTask(msg.SessionID, msg.ParentAgentID, msg.ToolID, msg.ToolName, msg.OutputPath, msg.IsComplete)

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
		case task := <-m.watcher.NewBackgroundTask:
			return newBackgroundTaskMsg(task)
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
			// For background tasks, Enter loads the output
			if node := m.tree.GetSelectedNode(); node != nil && node.Type == NodeTypeBackgroundTask {
				m.loadBackgroundTaskOutput(node)
			} else {
				// For other nodes, toggle enabled state
				m.tree.Toggle()
				m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())
			}
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

	case "x":
		m.stream.ToggleText()

	case "s":
		if m.focus == FocusTree {
			m.tree.Solo()
			m.stream.SetEnabledFilters(m.tree.GetEnabledFilters())
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
	// Check activity within last 30 seconds. Gather infos once so the collapse
	// policy sees the same snapshot.
	infos := m.watcher.GetActivityInfo(30 * time.Second)
	for _, info := range infos {
		m.tree.UpdateActivity(info.SessionID, info.AgentID, info.IsActive)
	}
	if m.collapseAfter > 0 {
		m.applyCollapsePolicy(infos)
	}
}

// applyCollapsePolicy auto-collapses sessions whose newest-modified file is
// older than collapseAfter. A session wakes up (LastModified is recent) →
// any user-set Pin is cleared so the next sleep cycle re-auto-collapses.
// This is the "pin resets on wake" semantic discussed in issue #5 Option D.
func (m *Model) applyCollapsePolicy(infos []watcher.ActivityInfo) {
	// Find newest LastModified across main + all agents per session.
	latest := map[string]time.Time{}
	for _, info := range infos {
		if t, ok := latest[info.SessionID]; !ok || info.LastModified.After(t) {
			latest[info.SessionID] = info.LastModified
		}
	}

	now := time.Now()
	for _, node := range m.tree.Root.Children {
		if node.Type != NodeTypeSession {
			continue
		}
		lastMod, ok := latest[node.ID]
		if !ok {
			continue
		}

		sessionActive := now.Sub(lastMod) < 30*time.Second
		if sessionActive {
			// Woke up: clear any prior pin so the next sleep cycle auto-collapses.
			if node.Pinned {
				m.tree.SetPinned(node.ID, false)
			}
			continue
		}

		if now.Sub(lastMod) >= m.collapseAfter && !node.Collapsed && !node.Pinned {
			m.tree.SetCollapsed(node.ID, true)
		}
	}
}

func (m *Model) loadBackgroundTaskOutput(node *TreeNode) {
	if node.OutputPath == "" {
		return
	}

	// Read the file content
	content, err := os.ReadFile(node.OutputPath)
	if err != nil {
		// Show error in stream
		m.stream.AddItem(parser.StreamItem{
			Type:      parser.TypeToolOutput,
			SessionID: node.SessionID,
			AgentID:   node.ParentAgentID,
			Content:   fmt.Sprintf("Error reading task output: %v", err),
			Timestamp: time.Now(),
		})
		return
	}

	// Create a stream item for the background task output
	statusIcon := "⏳"
	if node.IsComplete {
		statusIcon = "✓"
	}

	m.stream.AddItem(parser.StreamItem{
		Type:      parser.TypeToolOutput,
		SessionID: node.SessionID,
		AgentID:   node.ParentAgentID,
		ToolName:  fmt.Sprintf("%s %s", statusIcon, node.Name),
		Content:   string(content),
		Timestamp: time.Now(),
	})

	// Scroll to bottom to see the new output
	m.stream.ScrollDown(9999)
}

// wrappedRows returns how many terminal rows a single-line string will
// occupy at the current pane width. lipgloss.Render() does NOT wrap text
// unless Width() is set on the style, so a long header string comes back
// as a single line from lipgloss.Height() even though the terminal will
// wrap it at display time. We compute the wrap ourselves by measuring the
// visible character width and dividing by the pane width.
func (m *Model) wrappedRows(s string) int {
	if m.width <= 0 {
		return 1
	}
	visible := lipgloss.Width(s)
	// lipgloss.Height also catches any intentional newlines inside s
	newlines := lipgloss.Height(s)
	rowsPerLogicalLine := (visible + m.width - 1) / m.width // ceil
	if rowsPerLogicalLine < 1 {
		rowsPerLogicalLine = 1
	}
	// If the string has embedded newlines, each logical line can itself wrap.
	// This is a conservative approximation: multiply by visible/width.
	// For single-line strings (the common case for header/help), newlines=1.
	rows := newlines * rowsPerLogicalLine
	if rows < 1 {
		return 1
	}
	return rows
}

// chromeHeight returns how many rows the header + help bar actually occupy
// at the current width. The header wraps on narrow terminals because of
// the toggle labels, so measuring it dynamically prevents the tree/stream
// panes from overflowing the top of the viewport.
//
// Total rows we reserve: header (measured, wrap-aware) + help (measured,
// wrap-aware) + 2 for the inner pane's top+bottom border.
func (m *Model) chromeHeight() int {
	headerRows := m.wrappedRows(m.renderHeader())
	helpRows := m.wrappedRows(m.renderHelp())
	return headerRows + helpRows + 2
}

// contentInnerHeight is the Height(...) value we pass to the tree/stream
// styled pane. Always at least 1 row so the TUI doesn't collapse on
// minuscule terminals.
func (m *Model) contentInnerHeight() int {
	h := m.height - m.chromeHeight()
	if h < 1 {
		return 1
	}
	return h
}

func (m *Model) updateLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	contentHeight := m.contentInnerHeight()

	if m.showTree {
		m.tree.SetSize(m.treeWidth, contentHeight)
		m.stream.SetSize(m.width-m.treeWidth-5, contentHeight) // -5 for borders/padding/gap
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

	// Recompute layout in case the header wrapped to more rows than we
	// planned for (e.g. after a terminal resize or after the watcher
	// reports more sessions and the session-count label changes width).
	m.updateLayout()

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
	textToggle := m.renderToggle("Text", m.stream.IsTextEnabled(), "x")
	autoScroll := m.renderToggle("Scroll", m.stream.IsAutoScrollEnabled(), "a")
	treeToggle := m.renderToggle("Tree", m.showTree, "h")

	toggles := fmt.Sprintf("%s  %s  %s  %s  %s  %s",
		thinking, toolInput, toolOutput, textToggle, autoScroll, treeToggle)

	// Session count and auto-discovery status
	sessionInfo := ""
	if m.watcher != nil {
		sessions := m.watcher.GetSessions()
		autoDisc := ""
		if !m.watcher.IsAutoDiscoveryEnabled() {
			autoDisc = " [paused]"
		}
		if len(sessions) == 0 {
			sessionInfo = "Waiting..."
		} else if len(sessions) == 1 {
			for _, s := range sessions {
				sessionInfo = fmt.Sprintf("Session: %s%s", truncate(s.ID, 12), autoDisc)
			}
		} else {
			sessionInfo = fmt.Sprintf("%d sessions%s", len(sessions), autoDisc)
		}
	}

	// Token usage display (in / out / cache write+read)
	tokenInfo := ""
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 ||
		m.totalCacheCreation > 0 || m.totalCacheRead > 0 {
		tokenInfo = fmt.Sprintf("│ %s in / %s out",
			formatTokenCount(m.totalInputTokens),
			formatTokenCount(m.totalOutputTokens))
		if m.totalCacheCreation > 0 || m.totalCacheRead > 0 {
			tokenInfo += fmt.Sprintf(" / %s+%s cache",
				formatTokenCount(m.totalCacheCreation),
				formatTokenCount(m.totalCacheRead))
		}
	}

	// Build header - use plain text and apply headerStyle uniformly (like Rust version)
	// Don't use Width() as it causes truncation on narrow terminals
	headerText := fmt.Sprintf("%s  │  %s", toggles, sessionInfo)
	if tokenInfo != "" {
		headerText += "  " + tokenInfo
	}
	header := headerStyle.Render(headerText)

	return header
}

// formatTokenCount formats token counts for display
func formatTokenCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000.0)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1000000.0)
}

func (m *Model) renderToggle(name string, enabled bool, key string) string {
	checkbox := "☐"
	if enabled {
		checkbox = "☑"
	}
	return fmt.Sprintf("%s %s[%s]", checkbox, name, key)
}

func (m *Model) renderWithTree() string {
	innerHeight := m.contentInnerHeight()

	// Tree pane
	treeBorder := treeBorderStyle
	if m.focus == FocusTree {
		treeBorder = treeBorder.BorderForeground(primaryColor)
	}
	treePane := treeBorder.
		Width(m.treeWidth).
		Height(innerHeight).
		Render(m.tree.View())

	// Stream pane
	streamBorder := streamBorderStyle
	if m.focus == FocusStream {
		streamBorder = streamBorder.BorderForeground(primaryColor)
	}
	streamPane := streamBorder.
		Width(m.width - m.treeWidth - 5).
		Height(innerHeight).
		Render(m.stream.View())

	return lipgloss.JoinHorizontal(lipgloss.Top, treePane, " ", streamPane)
}

func (m *Model) renderStreamOnly() string {
	streamBorder := streamBorderStyle.BorderForeground(primaryColor)
	return streamBorder.
		Width(m.width - 2).
		Height(m.contentInnerHeight()).
		Render(m.stream.View())
}

func (m *Model) renderHelp() string {
	var help string
	if m.focus == FocusTree {
		help = "j/k: navigate │ space: toggle │ s: solo │ A: auto-discover │ q: quit"
	} else {
		help = "j/k: scroll │ g/G: top/bottom │ A: auto-discover │ tab: tree │ q: quit"
	}
	return helpStyle.Render(help)
}
