package tui

import (
	"fmt"
	"strings"
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

// AddAgent adds a subagent under a session
func (t *TreeView) AddAgent(sessionID, agentID string) {
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

	node := &TreeNode{
		Type:      NodeTypeAgent,
		ID:        agentID,
		SessionID: sessionID,
		Name:      fmt.Sprintf("Agent-%s", agentID[:min(AgentIDDisplayLength, len(agentID))]),
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

// Toggle toggles the enabled state of current node
func (t *TreeView) Toggle() {
	if t.cursor >= 0 && t.cursor < len(t.nodes) {
		node := t.nodes[t.cursor]
		node.Enabled = !node.Enabled

		// If toggling a session, toggle all children too
		if node.Type == NodeTypeSession {
			for _, child := range node.Children {
				child.Enabled = node.Enabled
			}
		}
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
	case NodeTypeMain, NodeTypeAgent:
		return node.SessionID
	}
	return ""
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
		return mutedStyle.Render("No sessions")
	}

	var b strings.Builder

	for i, node := range t.nodes {
		// Determine indent (sessions are depth 0, main/agents are depth 1)
		depth := t.getDepth(node) - 1 // -1 because we skip the hidden root
		if depth < 0 {
			depth = 0
		}
		indent := strings.Repeat("  ", depth)

		// Checkbox
		checkbox := "☑"
		checkStyle := treeCheckedStyle
		if !node.Enabled {
			checkbox = "☐"
			checkStyle = treeUncheckedStyle
		}

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
			if node.IsActive {
				icon = "📁 "
			} else {
				icon = "📂 "
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
		if !node.IsActive && node.Type != NodeTypeSession {
			name = mutedStyle.Render(node.Name)
		}

		line := fmt.Sprintf("%s%s%s %s%s",
			indent,
			branch,
			checkStyle.Render(checkbox),
			icon,
			name,
		)

		// Apply selection style
		if i == t.cursor {
			line = treeSelectedStyle.Render(line)
		} else if !node.IsActive && node.Type != NodeTypeSession {
			// Keep inactive agents muted even without selection
			line = mutedStyle.Render(line)
		} else {
			line = treeNormalStyle.Render(line)
		}

		// Ensure consistent width
		if t.width > 0 {
			lineLen := lipglossWidth(line)
			if lineLen < t.width-2 {
				line += strings.Repeat(" ", t.width-2-lineLen)
			}
		}

		b.WriteString(line)
		if i < len(t.nodes)-1 {
			b.WriteString("\n")
		}
	}

	// Pad to fill height
	lines := strings.Count(b.String(), "\n") + 1
	for i := lines; i < t.height-2; i++ {
		b.WriteString("\n")
	}

	return b.String()
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

// lipglossWidth calculates visible width accounting for ANSI codes
func lipglossWidth(s string) int {
	return len([]rune(stripAnsi(s)))
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
