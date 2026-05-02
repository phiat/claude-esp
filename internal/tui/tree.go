package tui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// NodeType indicates the type of tree node
type NodeType int

const (
	NodeTypeRoot           NodeType = iota
	NodeTypeSession                 // A Claude session
	NodeTypeMain                    // Main conversation within a session
	NodeTypeAgent                   // A subagent within a session
	NodeTypeBackgroundTask          // A background task (tool running in background)

	// AgentIDDisplayLength is how many chars of agent ID to show in display name
	AgentIDDisplayLength = 7
	// ToolIDDisplayLength is how many chars of tool ID to show
	ToolIDDisplayLength = 12
)

// TreeNode represents a node in the session/agent tree
type TreeNode struct {
	Type      NodeType
	ID        string // session ID for sessions, agent ID for agents, tool ID for bg tasks
	SessionID string // which session this belongs to (for main/agent/task nodes)
	Name      string
	Enabled   bool
	IsActive  bool // whether this node has recent activity (for main/agent nodes)
	Children  []*TreeNode
	Parent    *TreeNode

	// Background task specific fields
	ParentAgentID string // which agent spawned this task (empty = main)
	OutputPath    string // path to tool-results file
	IsComplete    bool   // whether the task has finished

	// Per-agent context size (Main/Agent nodes only). ContextTokens is the
	// most recent input+cache_creation+cache_read snapshot for this agent;
	// ContextWindow is the model's max context window in tokens. Both are
	// zero until at least one assistant message with usage info has arrived.
	ContextTokens int64
	ContextWindow int64

	// Session-only collapse state (used by -c / auto-collapse feature).
	// Collapsed: children are hidden from tree navigation and stream filtering.
	// Pinned: user manually expanded this session; suppress auto-collapse until
	// the session wakes up again.
	Collapsed bool
	Pinned    bool
}

// TreeView manages the tree of sessions and agents
type TreeView struct {
	Root   *TreeNode
	nodes  []*TreeNode // flattened list for navigation
	cursor int
	width  int
	height int
}

// NewTreeView creates a new tree view with a hidden root
func NewTreeView() *TreeView {
	root := &TreeNode{
		Type:    NodeTypeRoot,
		Name:    "Sessions",
		Enabled: true,
	}
	return &TreeView{
		Root:   root,
		nodes:  []*TreeNode{},
		cursor: 0,
	}
}

// AddSession adds a new session to the tree
func (t *TreeView) AddSession(sessionID, projectPath string) *TreeNode {
	// Check if session already exists
	for _, child := range t.Root.Children {
		if child.ID == sessionID {
			return child
		}
	}

	// Create a short display name from the project path
	displayName := projectPath
	parts := strings.Split(projectPath, "/")
	if len(parts) > 2 {
		displayName = parts[len(parts)-1]
	}
	if len(displayName) > 15 {
		displayName = displayName[:15]
	}

	session := &TreeNode{
		Type:     NodeTypeSession,
		ID:       sessionID,
		Name:     displayName,
		Enabled:  true,
		IsActive: true,
		Parent:   t.Root,
	}

	// Add Main node under the session
	main := &TreeNode{
		Type:      NodeTypeMain,
		SessionID: sessionID,
		Name:      "Main",
		Enabled:   true,
		IsActive:  true,
		Parent:    session,
	}
	session.Children = append(session.Children, main)

	t.Root.Children = append(t.Root.Children, session)
	t.rebuildNodeList()
	return session
}

// AddAgent adds a subagent under a session.
// If agentType is non-empty, it is used as the display name.
// For compound types like "feature-dev:code-reviewer", only the part after ":" is used.
func (t *TreeView) AddAgent(sessionID, agentID, agentType string) {
	// Find the session node
	var session *TreeNode
	for _, child := range t.Root.Children {
		if child.Type == NodeTypeSession && child.ID == sessionID {
			session = child
			break
		}
	}

	if session == nil {
		return // Session not found
	}

	// Check if agent already exists
	for _, child := range session.Children {
		if child.Type == NodeTypeAgent && child.ID == agentID {
			return
		}
	}

	displayName := fmt.Sprintf("Agent-%s", agentID[:min(AgentIDDisplayLength, len(agentID))])
	if agentType != "" {
		// For compound types like "feature-dev:code-reviewer", use part after ":"
		if idx := strings.LastIndex(agentType, ":"); idx >= 0 && idx < len(agentType)-1 {
			displayName = agentType[idx+1:]
		} else {
			displayName = agentType
		}
	}

	node := &TreeNode{
		Type:      NodeTypeAgent,
		ID:        agentID,
		SessionID: sessionID,
		Name:      displayName,
		Enabled:   true,
		IsActive:  true,
		Parent:    session,
	}
	session.Children = append(session.Children, node)
	t.rebuildNodeList()
}

// AddBackgroundTask adds a background task under the appropriate agent/main node
func (t *TreeView) AddBackgroundTask(sessionID, parentAgentID, toolID, toolName, outputPath string, isComplete bool) {
	// Find the session node
	var session *TreeNode
	for _, child := range t.Root.Children {
		if child.Type == NodeTypeSession && child.ID == sessionID {
			session = child
			break
		}
	}

	if session == nil {
		return // Session not found
	}

	// Find the parent node (Main or Agent)
	var parent *TreeNode
	for _, child := range session.Children {
		if parentAgentID == "" && child.Type == NodeTypeMain {
			parent = child
			break
		} else if child.Type == NodeTypeAgent && child.ID == parentAgentID {
			parent = child
			break
		}
	}

	if parent == nil {
		return // Parent not found
	}

	// Check if task already exists
	for _, child := range parent.Children {
		if child.Type == NodeTypeBackgroundTask && child.ID == toolID {
			// Update completion status if changed
			child.IsComplete = isComplete
			return
		}
	}

	// Truncate tool name for display
	displayName := toolName
	if len(displayName) > 25 {
		displayName = displayName[:25] + "..."
	}

	node := &TreeNode{
		Type:          NodeTypeBackgroundTask,
		ID:            toolID,
		SessionID:     sessionID,
		Name:          displayName,
		Enabled:       true,
		IsActive:      !isComplete,
		Parent:        parent,
		ParentAgentID: parentAgentID,
		OutputPath:    outputPath,
		IsComplete:    isComplete,
	}
	parent.Children = append(parent.Children, node)
	t.rebuildNodeList()
}

// UpdateBackgroundTaskStatus updates a background task's completion status
func (t *TreeView) UpdateBackgroundTaskStatus(sessionID, toolID string, isComplete bool) {
	for _, session := range t.Root.Children {
		if session.Type != NodeTypeSession || session.ID != sessionID {
			continue
		}
		for _, agent := range session.Children {
			for _, child := range agent.Children {
				if child.Type == NodeTypeBackgroundTask && child.ID == toolID {
					child.IsComplete = isComplete
					child.IsActive = !isComplete
					return
				}
			}
		}
	}
}

// GetSelectedNode returns the currently selected node
func (t *TreeView) GetSelectedNode() *TreeNode {
	if t.cursor >= 0 && t.cursor < len(t.nodes) {
		return t.nodes[t.cursor]
	}
	return nil
}

// rebuildNodeList flattens the tree for navigation (excluding hidden root)
func (t *TreeView) rebuildNodeList() {
	t.nodes = nil
	for _, child := range t.Root.Children {
		t.flattenNode(child, 0)
	}
	// Ensure cursor is valid
	if t.cursor >= len(t.nodes) {
		t.cursor = max(0, len(t.nodes)-1)
	}
}

func (t *TreeView) flattenNode(node *TreeNode, depth int) {
	t.nodes = append(t.nodes, node)
	// Collapsed sessions hide their children from navigation AND from the
	// stream's enabled-filter set (GetEnabledFilters walks t.nodes).
	if node.Type == NodeTypeSession && node.Collapsed {
		return
	}
	for _, child := range node.Children {
		t.flattenNode(child, depth+1)
	}
}

// MoveUp moves cursor up
func (t *TreeView) MoveUp() {
	if t.cursor > 0 {
		t.cursor--
	}
}

// MoveDown moves cursor down
func (t *TreeView) MoveDown() {
	if t.cursor < len(t.nodes)-1 {
		t.cursor++
	}
}

// Toggle toggles the current node's visibility.
//
// On a session node, space collapses/expands (hides children in the tree and
// filters them from the stream). Manually expanding pins the session so
// auto-collapse won't re-collapse it until the session wakes again.
//
// On Main/Agent/BackgroundTask nodes, space toggles the node's enabled state
// (shows/hides that specific agent's output).
func (t *TreeView) Toggle() {
	if t.cursor < 0 || t.cursor >= len(t.nodes) {
		return
	}
	node := t.nodes[t.cursor]
	if node.Type == NodeTypeSession {
		node.Collapsed = !node.Collapsed
		if !node.Collapsed {
			node.Pinned = true
		}
		t.rebuildNodeList()
		return
	}
	node.Enabled = !node.Enabled
}

// Solo isolates the selected node: disables all others, enables only this one.
// If already soloed, re-enables all.
//
// If the target is a collapsed session, Solo force-expands it first (and
// pins) — the whole point of soloing is to see that session's output, which
// means its children must be visible in the tree and routed to the stream.
func (t *TreeView) Solo() {
	if t.cursor < 0 || t.cursor >= len(t.nodes) {
		return
	}
	selected := t.nodes[t.cursor]

	if t.isSoloed(selected) {
		// Un-solo: re-enable everything
		setAllEnabled(t.Root, true)
	} else {
		// Disable all sessions and their children
		for _, session := range t.Root.Children {
			setAllEnabled(session, false)
		}

		// If soloing onto a collapsed session, expand it first so its
		// children can be enabled and shown in the stream.
		if selected.Type == NodeTypeSession && selected.Collapsed {
			selected.Collapsed = false
			selected.Pinned = true
			defer t.rebuildNodeList()
		}

		// Enable the selected node and the path to it
		switch selected.Type {
		case NodeTypeSession:
			setAllEnabled(selected, true)
		case NodeTypeMain, NodeTypeAgent:
			if selected.Parent != nil {
				selected.Parent.Enabled = true
			}
			selected.Enabled = true
		}
	}
}

func (t *TreeView) isSoloed(selected *TreeNode) bool {
	if !selected.Enabled {
		return false
	}

	for _, session := range t.Root.Children {
		if selected.Type == NodeTypeSession {
			if session != selected && session.Enabled {
				return false
			}
		} else {
			for _, child := range session.Children {
				if child.Type == NodeTypeBackgroundTask {
					continue
				}
				if child != selected && child.Enabled {
					return false
				}
			}
		}
	}
	return true
}

func setAllEnabled(node *TreeNode, enabled bool) {
	node.Enabled = enabled
	for _, child := range node.Children {
		setAllEnabled(child, enabled)
	}
}

// GetSelectedSession returns the session ID of the currently selected node (or its parent session)
func (t *TreeView) GetSelectedSession() string {
	if t.cursor < 0 || t.cursor >= len(t.nodes) {
		return ""
	}
	node := t.nodes[t.cursor]
	switch node.Type {
	case NodeTypeSession:
		return node.ID
	case NodeTypeMain, NodeTypeAgent, NodeTypeBackgroundTask:
		return node.SessionID
	}
	return ""
}

// SetCollapsed updates a session's collapse state. When collapsing, if the
// cursor was pointing at a now-hidden child, it jumps up to the session row
// so the user doesn't lose their position entirely. Setting collapsed=false
// does NOT set Pinned — the caller decides (auto-wake vs user Toggle).
func (t *TreeView) SetCollapsed(sessionID string, collapsed bool) {
	for _, session := range t.Root.Children {
		if session.Type != NodeTypeSession || session.ID != sessionID {
			continue
		}
		if session.Collapsed == collapsed {
			return
		}
		cursorNode := t.GetSelectedNode()
		session.Collapsed = collapsed
		t.rebuildNodeList()
		// If the cursor was inside the subtree that just got hidden, move it
		// up to the session row. Otherwise leave it alone — rebuildNodeList
		// already clamps it to a valid range.
		if collapsed && cursorNode != nil && cursorNode != session {
			if t.nodeInSubtree(cursorNode, session) {
				for i, n := range t.nodes {
					if n == session {
						t.cursor = i
						break
					}
				}
			}
		}
		return
	}
}

// SetPinned sets the user-pinned flag on a session. Pinned sessions are
// exempted from auto-collapse until they next wake up.
func (t *TreeView) SetPinned(sessionID string, pinned bool) {
	for _, session := range t.Root.Children {
		if session.Type == NodeTypeSession && session.ID == sessionID {
			session.Pinned = pinned
			return
		}
	}
}

// nodeInSubtree returns true if needle appears anywhere in root's subtree.
func (t *TreeView) nodeInSubtree(needle, root *TreeNode) bool {
	if root == needle {
		return true
	}
	for _, c := range root.Children {
		if t.nodeInSubtree(needle, c) {
			return true
		}
	}
	return false
}

// SetSessionTitle updates the display name of a session node. Used when the
// JSONL stream reports an agent-name or custom-title for the session, giving
// users a human-readable label instead of the project path. Length is capped
// so narrow tree panes don't overflow; the raw project name (25 char cap) was
// the prior default, so we keep the same ceiling here.
func (t *TreeView) SetSessionTitle(sessionID, title string) {
	if title == "" {
		return
	}
	for _, child := range t.Root.Children {
		if child.Type == NodeTypeSession && child.ID == sessionID {
			if len(title) > 25 {
				title = title[:25]
			}
			child.Name = title
			return
		}
	}
}

// RemoveSession removes a session and all its children from the tree
func (t *TreeView) RemoveSession(sessionID string) {
	// Find and remove the session from root's children
	for i, child := range t.Root.Children {
		if child.Type == NodeTypeSession && child.ID == sessionID {
			t.Root.Children = append(t.Root.Children[:i], t.Root.Children[i+1:]...)
			break
		}
	}
	t.rebuildNodeList()
}

// UpdateContext sets the latest context-size snapshot for a Main/Agent node.
// agentID == "" targets the session's Main node; otherwise the matching Agent.
// Tokens overwrite (not accumulate) — context size is a rolling snapshot,
// not a sum. window is the model's max context window from
// parser.ContextWindowFor.
func (t *TreeView) UpdateContext(sessionID, agentID string, tokens, window int64) {
	for _, session := range t.Root.Children {
		if session.Type != NodeTypeSession || session.ID != sessionID {
			continue
		}
		for _, child := range session.Children {
			if agentID == "" && child.Type == NodeTypeMain {
				child.ContextTokens = tokens
				child.ContextWindow = window
				return
			}
			if agentID != "" && child.Type == NodeTypeAgent && child.ID == agentID {
				child.ContextTokens = tokens
				child.ContextWindow = window
				return
			}
		}
		return
	}
}

// UpdateActivity updates the active status of nodes and re-sorts them
func (t *TreeView) UpdateActivity(sessionID, agentID string, isActive bool) {
	// Find the session
	for _, session := range t.Root.Children {
		if session.Type != NodeTypeSession || session.ID != sessionID {
			continue
		}

		// Update session's active status based on any active children
		sessionHasActive := false

		for _, child := range session.Children {
			if child.Type == NodeTypeMain && agentID == "" {
				child.IsActive = isActive
			} else if child.Type == NodeTypeAgent && child.ID == agentID {
				child.IsActive = isActive
			}
			if child.IsActive {
				sessionHasActive = true
			}
		}
		session.IsActive = sessionHasActive

		// Sort children: active first, then by name
		t.sortChildren(session)
		break
	}

	// Sort sessions: active first
	t.sortChildren(t.Root)
	t.rebuildNodeList()
}

// sortChildren sorts a node's children with active nodes first
func (t *TreeView) sortChildren(parent *TreeNode) {
	if len(parent.Children) <= 1 {
		return
	}

	// Stable sort: active first, preserve relative order otherwise
	// Keep Main always first within a session
	for i := 1; i < len(parent.Children); i++ {
		for j := i; j > 0; j-- {
			curr := parent.Children[j]
			prev := parent.Children[j-1]

			// Main always stays first
			if prev.Type == NodeTypeMain {
				break
			}

			// Active nodes bubble up (but not past Main)
			if curr.IsActive && !prev.IsActive {
				parent.Children[j], parent.Children[j-1] = parent.Children[j-1], parent.Children[j]
			} else {
				break
			}
		}
	}
}

// EnabledFilter represents which sessions/agents are enabled
type EnabledFilter struct {
	SessionID string
	AgentID   string // empty string means main
}

// GetEnabledFilters returns list of enabled session+agent combinations
func (t *TreeView) GetEnabledFilters() []EnabledFilter {
	var filters []EnabledFilter
	for _, node := range t.nodes {
		if !node.Enabled {
			continue
		}
		switch node.Type {
		case NodeTypeMain:
			filters = append(filters, EnabledFilter{
				SessionID: node.SessionID,
				AgentID:   "", // main
			})
		case NodeTypeAgent:
			filters = append(filters, EnabledFilter{
				SessionID: node.SessionID,
				AgentID:   node.ID,
			})
		}
	}
	return filters
}

// IsEnabled checks if a session+agent combo is enabled
func (t *TreeView) IsEnabled(sessionID, agentID string) bool {
	for _, node := range t.nodes {
		if !node.Enabled {
			continue
		}
		if node.Type == NodeTypeMain && node.SessionID == sessionID && agentID == "" {
			return true
		}
		if node.Type == NodeTypeAgent && node.SessionID == sessionID && node.ID == agentID {
			return true
		}
	}
	return false
}

// SetSize sets the dimensions
func (t *TreeView) SetSize(width, height int) {
	t.width = width
	t.height = height
}

// View renders the tree
func (t *TreeView) View() string {
	if len(t.nodes) == 0 {
		return mutedStyle.Render("Waiting for Claude Code sessions...")
	}

	var b strings.Builder

	for i, node := range t.nodes {
		// Determine indent (sessions are depth 0, main/agents are depth 1)
		depth := t.getDepth(node) - 1 // -1 because we skip the hidden root
		if depth < 0 {
			depth = 0
		}
		indent := strings.Repeat("  ", depth)

		// Tree branch character
		branch := ""
		if depth > 0 {
			if t.isLastChild(node) {
				branch = "└─"
			} else {
				branch = "├─"
			}
		}

		// Icon based on node type and activity
		icon := ""
		switch node.Type {
		case NodeTypeSession:
			arrow := "▾"
			if node.Collapsed {
				arrow = "▸"
			}
			if node.IsActive {
				icon = "📁" + arrow + " "
			} else {
				icon = "📂" + arrow + " "
			}
		case NodeTypeMain:
			if node.IsActive {
				icon = "💬 "
			} else {
				icon = "💤 "
			}
		case NodeTypeAgent:
			if node.IsActive {
				icon = "🤖 "
			} else {
				icon = "💤 "
			}
		case NodeTypeBackgroundTask:
			if node.IsComplete {
				icon = "✓ "
			} else {
				icon = "⏳ "
			}
		}

		// Build line with name (muted if inactive)
		name := node.Name
		// Collapsed sessions show a hidden-agent count so users don't lose
		// the signal that subagents exist underneath the collapsed node.
		if node.Type == NodeTypeSession && node.Collapsed {
			agents := 0
			for _, c := range node.Children {
				if c.Type == NodeTypeAgent {
					agents++
				}
			}
			if agents > 0 {
				name = fmt.Sprintf("%s (+%d)", name, agents)
			}
		}
		if !node.IsActive && node.Type != NodeTypeSession {
			name = mutedStyle.Render(node.Name)
		}

		line := fmt.Sprintf("%s%s%s%s",
			indent,
			branch,
			icon,
			name,
		)

		// Context-size suffix for Main/Agent nodes (e.g. "  142k/1M").
		// Right-aligned when the line fits; appended otherwise. Truncation
		// of over-wide lines (below) handles the worst case.
		ctxSuffix := contextSuffix(node)
		if ctxSuffix != "" && t.width > 0 {
			innerWidth := t.width - 4
			if innerWidth < 1 {
				innerWidth = 1
			}
			used := lipglossWidth(line)
			suffixW := lipglossWidth(ctxSuffix)
			gap := innerWidth - used - suffixW
			if gap >= 1 {
				line += strings.Repeat(" ", gap) + mutedStyle.Render(ctxSuffix)
			} else {
				line += " " + mutedStyle.Render(ctxSuffix)
			}
		}

		// Apply selection style. With the checkbox gone, disabled rows are
		// muted so the user still has a visual signal for what they've
		// toggled off (in addition to the inactive-children muting).
		switch {
		case i == t.cursor:
			line = treeSelectedStyle.Render(line)
		case !node.Enabled,
			!node.IsActive && node.Type != NodeTypeSession:
			line = mutedStyle.Render(line)
		default:
			line = treeNormalStyle.Render(line)
		}

		// Ensure consistent width.
		//
		// `t.width` is set by the caller to the OUTER width of the
		// bordered pane the tree is rendered into. treeBorderStyle uses
		// `Border()` (2 cols) + `Padding(0, 1)` (2 cols) = 4 cols of
		// chrome, so the actual content area is `t.width - 4`.
		//
		// Padding to `t.width - 2` (the old value) put every line 2
		// cols wider than the content area, which the terminal then
		// wrapped to a second row. With emoji icons adding another
		// visible column per line, every tree entry was 2-3 cols too
		// wide and rendered as 2 terminal rows. That effectively
		// doubled the tree height and overflowed the viewport,
		// scrolling the header + top borders off the screen.
		//
		// runewidth.StringWidth (via lipglossWidth below) now correctly
		// counts emoji as 2 cols, and the padding target is -4 so the
		// line fits inside the bordered+padded pane without wrapping.
		if t.width > 0 {
			innerWidth := t.width - 4
			if innerWidth < 1 {
				innerWidth = 1
			}
			lineLen := lipglossWidth(line)
			if lineLen < innerWidth {
				line += strings.Repeat(" ", innerWidth-lineLen)
			} else if lineLen > innerWidth {
				// Truncate over-wide lines rune-by-rune so we stop at
				// exactly innerWidth visible columns. Preserves ANSI
				// escape sequences that precede the visible runes.
				line = runewidth.Truncate(stripAnsi(line), innerWidth, "…")
			}
		}

		b.WriteString(line)
		if i < len(t.nodes)-1 {
			b.WriteString("\n")
		}
	}

	// Defensive: never emit more lines than the assigned inner height.
	// The lipglossWidth fix above keeps each line from wrapping in the
	// terminal, but if we simply have more nodes than height allows,
	// we still need to cap the output so the pane doesn't overflow.
	innerHeight := t.height - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	raw := b.String()
	allLines := strings.Split(raw, "\n")
	if len(allLines) > innerHeight {
		// Keep the BOTTOM innerHeight lines — that's the most recent /
		// most relevant content if nodes were appended over time. The
		// future: add scroll support that respects t.cursor.
		allLines = allLines[len(allLines)-innerHeight:]
	}

	// Pad to fill height
	for len(allLines) < innerHeight {
		allLines = append(allLines, "")
	}

	return strings.Join(allLines, "\n")
}

func (t *TreeView) getDepth(node *TreeNode) int {
	depth := 0
	current := node
	for current.Parent != nil {
		depth++
		current = current.Parent
	}
	return depth
}

func (t *TreeView) isLastChild(node *TreeNode) bool {
	if node.Parent == nil {
		return true
	}
	children := node.Parent.Children
	return len(children) > 0 && children[len(children)-1] == node
}

// contextSuffix returns "14%" for Main/Agent nodes once we've seen at least
// one assistant message with usage info. Percentage of the model's max
// context window used (input + cache_creation + cache_read / window).
// Returns "" for sessions, background tasks, and agents with no usage yet.
func contextSuffix(node *TreeNode) string {
	if node.Type != NodeTypeMain && node.Type != NodeTypeAgent {
		return ""
	}
	if node.ContextTokens <= 0 || node.ContextWindow <= 0 {
		return ""
	}
	pct := node.ContextTokens * 100 / node.ContextWindow
	return fmt.Sprintf("%d%%", pct)
}

// lipglossWidth calculates visible width accounting for ANSI codes
func lipglossWidth(s string) int {
	// runewidth.StringWidth correctly handles East Asian wide characters
	// and emoji (which occupy 2 terminal columns despite being 1 rune).
	// len([]rune(s)) would undercount lines with 💤/📁/💬/🤖/✓/⏳ icons,
	// causing them to be padded too short and then wrap in the terminal —
	// which made the tree taller than its assigned height and overflowed
	// the viewport, clipping the top of the TUI.
	return runewidth.StringWidth(stripAnsi(s))
}

func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
