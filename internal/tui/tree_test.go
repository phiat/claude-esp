package tui

import (
	"testing"
)

func TestTreeView_AddSession(t *testing.T) {
	tv := NewTreeView()
	session := tv.AddSession("sess1", "home/user/project")

	if session == nil {
		t.Fatal("AddSession returned nil")
	}
	if session.ID != "sess1" {
		t.Errorf("session ID = %q, want %q", session.ID, "sess1")
	}
	if session.Type != NodeTypeSession {
		t.Errorf("session type = %d, want %d", session.Type, NodeTypeSession)
	}
	if len(session.Children) != 1 {
		t.Fatalf("expected 1 child (Main), got %d", len(session.Children))
	}
	if session.Children[0].Type != NodeTypeMain {
		t.Errorf("child type = %d, want %d (Main)", session.Children[0].Type, NodeTypeMain)
	}
	if session.Children[0].Name != "Main" {
		t.Errorf("child name = %q, want %q", session.Children[0].Name, "Main")
	}
}

func TestTreeView_AddSessionDuplicate(t *testing.T) {
	tv := NewTreeView()
	s1 := tv.AddSession("sess1", "project")
	s2 := tv.AddSession("sess1", "project")

	if s1 != s2 {
		t.Error("duplicate AddSession should return same node")
	}
	if len(tv.Root.Children) != 1 {
		t.Errorf("expected 1 session, got %d", len(tv.Root.Children))
	}
}

func TestTreeView_AddAgent(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent123456789")

	session := tv.Root.Children[0]
	if len(session.Children) != 2 {
		t.Fatalf("expected 2 children (Main + Agent), got %d", len(session.Children))
	}
	agent := session.Children[1]
	if agent.Type != NodeTypeAgent {
		t.Errorf("agent type = %d, want %d", agent.Type, NodeTypeAgent)
	}
	if agent.Name != "Agent-agent12" {
		t.Errorf("agent name = %q, want %q", agent.Name, "Agent-agent12")
	}
	if agent.ID != "agent123456789" {
		t.Errorf("agent ID = %q, want %q", agent.ID, "agent123456789")
	}
}

func TestTreeView_AddAgentNoSession(t *testing.T) {
	tv := NewTreeView()
	// Should not panic when adding agent to non-existent session
	tv.AddAgent("nonexistent", "agent1")
	if len(tv.Root.Children) != 0 {
		t.Error("should not add anything for non-existent session")
	}
}

func TestTreeView_AddAgentDuplicate(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent1")
	tv.AddAgent("sess1", "agent1")

	session := tv.Root.Children[0]
	if len(session.Children) != 2 {
		t.Errorf("expected 2 children (Main + 1 Agent), got %d", len(session.Children))
	}
}

func TestTreeView_AddBackgroundTask(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddBackgroundTask("sess1", "", "toolu_123", "Bash: npm install", "/path/output.txt", false)

	main := tv.Root.Children[0].Children[0]
	if len(main.Children) != 1 {
		t.Fatalf("expected 1 background task under Main, got %d", len(main.Children))
	}
	task := main.Children[0]
	if task.Type != NodeTypeBackgroundTask {
		t.Errorf("type = %d, want %d", task.Type, NodeTypeBackgroundTask)
	}
	if task.ID != "toolu_123" {
		t.Errorf("ID = %q, want %q", task.ID, "toolu_123")
	}
	if task.IsComplete {
		t.Error("task should not be complete")
	}
}

func TestTreeView_AddBackgroundTaskUnderAgent(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent1")
	tv.AddBackgroundTask("sess1", "agent1", "toolu_456", "Task: explore", "/path/out.txt", true)

	agent := tv.Root.Children[0].Children[1]
	if len(agent.Children) != 1 {
		t.Fatalf("expected 1 background task under Agent, got %d", len(agent.Children))
	}
	if !agent.Children[0].IsComplete {
		t.Error("task should be complete")
	}
}

func TestTreeView_Toggle(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")

	// Cursor is at session node (index 0)
	tv.cursor = 0
	session := tv.Root.Children[0]

	if !session.Enabled {
		t.Error("session should be enabled by default")
	}

	tv.Toggle()
	if session.Enabled {
		t.Error("session should be disabled after toggle")
	}
	// Children should also be disabled
	for _, child := range session.Children {
		if child.Enabled {
			t.Error("child should be disabled when session is toggled off")
		}
	}

	tv.Toggle()
	if !session.Enabled {
		t.Error("session should be re-enabled after second toggle")
	}
}

func TestTreeView_RemoveSession(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project1")
	tv.AddSession("sess2", "project2")

	if len(tv.Root.Children) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(tv.Root.Children))
	}

	tv.RemoveSession("sess1")

	if len(tv.Root.Children) != 1 {
		t.Fatalf("expected 1 session after removal, got %d", len(tv.Root.Children))
	}
	if tv.Root.Children[0].ID != "sess2" {
		t.Errorf("remaining session ID = %q, want %q", tv.Root.Children[0].ID, "sess2")
	}
}

func TestTreeView_RemoveNonExistent(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")

	tv.RemoveSession("nonexistent")
	if len(tv.Root.Children) != 1 {
		t.Error("removing non-existent session should not affect tree")
	}
}

func TestTreeView_GetEnabledFilters(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent1")

	filters := tv.GetEnabledFilters()
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters (Main + Agent), got %d", len(filters))
	}

	// Find the main filter
	var foundMain, foundAgent bool
	for _, f := range filters {
		if f.SessionID == "sess1" && f.AgentID == "" {
			foundMain = true
		}
		if f.SessionID == "sess1" && f.AgentID == "agent1" {
			foundAgent = true
		}
	}
	if !foundMain {
		t.Error("missing Main filter")
	}
	if !foundAgent {
		t.Error("missing Agent filter")
	}
}

func TestTreeView_GetEnabledFiltersDisabled(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")

	// Toggle session off (cursor should be at session)
	tv.cursor = 0
	tv.Toggle()

	filters := tv.GetEnabledFilters()
	if len(filters) != 0 {
		t.Errorf("expected 0 filters when session disabled, got %d", len(filters))
	}
}

func TestTreeView_Navigation(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent1")
	// Nodes: session, main, agent = 3 nodes

	if tv.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", tv.cursor)
	}

	tv.MoveDown()
	if tv.cursor != 1 {
		t.Errorf("after MoveDown, cursor = %d, want 1", tv.cursor)
	}

	tv.MoveDown()
	if tv.cursor != 2 {
		t.Errorf("after second MoveDown, cursor = %d, want 2", tv.cursor)
	}

	// Should not go past the end
	tv.MoveDown()
	if tv.cursor != 2 {
		t.Errorf("cursor should not exceed node count, got %d", tv.cursor)
	}

	tv.MoveUp()
	if tv.cursor != 1 {
		t.Errorf("after MoveUp, cursor = %d, want 1", tv.cursor)
	}

	// Move to top
	tv.MoveUp()
	tv.MoveUp() // should stay at 0
	if tv.cursor != 0 {
		t.Errorf("cursor should not go below 0, got %d", tv.cursor)
	}
}

func TestTreeView_UpdateActivity(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")

	main := tv.Root.Children[0].Children[0]
	if !main.IsActive {
		t.Error("main should be active initially")
	}

	tv.UpdateActivity("sess1", "", false)
	if main.IsActive {
		t.Error("main should be inactive after update")
	}

	tv.UpdateActivity("sess1", "", true)
	if !main.IsActive {
		t.Error("main should be active after re-enabling")
	}
}

func TestTreeView_GetSelectedNode(t *testing.T) {
	tv := NewTreeView()

	// Empty tree
	node := tv.GetSelectedNode()
	if node != nil {
		t.Error("GetSelectedNode on empty tree should return nil")
	}

	tv.AddSession("sess1", "project")
	node = tv.GetSelectedNode()
	if node == nil {
		t.Fatal("GetSelectedNode should return a node")
	}
	if node.Type != NodeTypeSession {
		t.Errorf("selected node type = %d, want %d (Session)", node.Type, NodeTypeSession)
	}
}

func TestTreeView_GetSelectedSession(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")
	tv.AddAgent("sess1", "agent1")

	// At session
	tv.cursor = 0
	if id := tv.GetSelectedSession(); id != "sess1" {
		t.Errorf("at session: got %q, want %q", id, "sess1")
	}

	// At main
	tv.cursor = 1
	if id := tv.GetSelectedSession(); id != "sess1" {
		t.Errorf("at main: got %q, want %q", id, "sess1")
	}

	// At agent
	tv.cursor = 2
	if id := tv.GetSelectedSession(); id != "sess1" {
		t.Errorf("at agent: got %q, want %q", id, "sess1")
	}
}

func TestTreeView_IsEnabled(t *testing.T) {
	tv := NewTreeView()
	tv.AddSession("sess1", "project")

	if !tv.IsEnabled("sess1", "") {
		t.Error("main should be enabled by default")
	}

	if tv.IsEnabled("nonexistent", "") {
		t.Error("nonexistent session should not be enabled")
	}
}

func TestTreeView_ViewEmpty(t *testing.T) {
	tv := NewTreeView()
	view := tv.View()
	if view == "" {
		t.Error("empty tree should still render something")
	}
}

func TestTreeView_ProjectNameTruncation(t *testing.T) {
	tv := NewTreeView()
	session := tv.AddSession("sess1", "a/very/long/project/directory/name/that/exceeds/limit")

	// Name should be truncated to 15 chars
	if len(session.Name) > 15 {
		t.Errorf("session name length = %d, want <= 15", len(session.Name))
	}
}
