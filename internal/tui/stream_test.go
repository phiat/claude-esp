package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/phiat/claude-esp/internal/parser"
)

func newTestItem(typ parser.StreamItemType, sessionID, agentID, content string) parser.StreamItem {
	return parser.StreamItem{
		Type:      typ,
		SessionID: sessionID,
		AgentID:   agentID,
		AgentName: "Main",
		Content:   content,
		Timestamp: time.Now(),
	}
}

func TestStreamView_AddItem(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	// Enable a filter so items are visible
	s.SetEnabledFilters([]EnabledFilter{{SessionID: "sess1", AgentID: ""}})

	item := newTestItem(parser.TypeThinking, "sess1", "", "test thinking")
	s.AddItem(item)

	if len(s.items) != 1 {
		t.Errorf("expected 1 item, got %d", len(s.items))
	}
	if s.items[0].Content != "test thinking" {
		t.Errorf("content = %q, want %q", s.items[0].Content, "test thinking")
	}
}

func TestStreamView_Deduplication(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	item1 := parser.StreamItem{
		Type:      parser.TypeToolInput,
		SessionID: "sess1",
		ToolID:    "toolu_123",
		Content:   "first",
		Timestamp: time.Now(),
	}
	item2 := parser.StreamItem{
		Type:      parser.TypeToolOutput,
		SessionID: "sess1",
		ToolID:    "toolu_123",
		Content:   "second",
		Timestamp: time.Now(),
	}

	s.AddItem(item1)
	s.AddItem(item2) // same ToolID, should be skipped

	if len(s.items) != 1 {
		t.Errorf("expected 1 item (duplicate skipped), got %d", len(s.items))
	}
}

func TestStreamView_DedupAllowsDifferentToolIDs(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	s.AddItem(parser.StreamItem{Type: parser.TypeToolInput, ToolID: "toolu_1", Content: "a", Timestamp: time.Now()})
	s.AddItem(parser.StreamItem{Type: parser.TypeToolOutput, ToolID: "toolu_2", Content: "b", Timestamp: time.Now()})

	if len(s.items) != 2 {
		t.Errorf("expected 2 items (different ToolIDs), got %d", len(s.items))
	}
}

func TestStreamView_EmptyToolIDNotDeduped(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	s.AddItem(parser.StreamItem{Type: parser.TypeThinking, Content: "a", Timestamp: time.Now()})
	s.AddItem(parser.StreamItem{Type: parser.TypeThinking, Content: "b", Timestamp: time.Now()})

	if len(s.items) != 2 {
		t.Errorf("expected 2 items (empty ToolID not deduped), got %d", len(s.items))
	}
}

func TestStreamView_MaxItems(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	for i := 0; i < MaxStreamItems+50; i++ {
		s.AddItem(parser.StreamItem{
			Type:      parser.TypeThinking,
			Content:   "item",
			Timestamp: time.Now(),
		})
	}

	if len(s.items) != MaxStreamItems {
		t.Errorf("expected %d items (capped), got %d", MaxStreamItems, len(s.items))
	}
}

func TestStreamView_TypeFiltering(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)
	s.SetEnabledFilters([]EnabledFilter{{SessionID: "s1", AgentID: ""}})

	s.AddItem(newTestItem(parser.TypeThinking, "s1", "", "thinking"))
	s.AddItem(newTestItem(parser.TypeToolInput, "s1", "", "input"))
	s.AddItem(newTestItem(parser.TypeToolOutput, "s1", "", "output"))

	// All enabled by default
	view := s.View()
	if !strings.Contains(view, "thinking") {
		t.Error("thinking should be visible by default")
	}

	// Toggle thinking off
	s.ToggleThinking()
	view = s.View()
	if strings.Contains(view, "thinking") {
		t.Error("thinking should be hidden after toggle")
	}

	// Toggle back on
	s.ToggleThinking()
	if !s.IsThinkingEnabled() {
		t.Error("thinking should be re-enabled")
	}
}

func TestStreamView_SessionFiltering(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	s.AddItem(newTestItem(parser.TypeThinking, "sess1", "", "from session 1"))
	s.AddItem(newTestItem(parser.TypeThinking, "sess2", "", "from session 2"))

	// Only enable sess1
	s.SetEnabledFilters([]EnabledFilter{{SessionID: "sess1", AgentID: ""}})
	view := s.View()

	if !strings.Contains(view, "from session 1") {
		t.Error("session 1 content should be visible")
	}
	if strings.Contains(view, "from session 2") {
		t.Error("session 2 content should be hidden")
	}
}

func TestStreamView_AutoScroll(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	if !s.IsAutoScrollEnabled() {
		t.Error("auto-scroll should be enabled by default")
	}

	s.ToggleAutoScroll()
	if s.IsAutoScrollEnabled() {
		t.Error("auto-scroll should be disabled after toggle")
	}

	s.ToggleAutoScroll()
	if !s.IsAutoScrollEnabled() {
		t.Error("auto-scroll should be re-enabled after second toggle")
	}
}

func TestStreamView_ScrollUpDisablesAutoScroll(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	s.ScrollUp(1)
	if s.IsAutoScrollEnabled() {
		t.Error("scrolling up should disable auto-scroll")
	}
}

func TestTruncateContent(t *testing.T) {
	s := NewStreamView()

	// Build content with more than MaxLinesPerItem lines
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	content := strings.Join(lines, "\n")

	result := s.truncateContent(content, 200)
	resultLines := strings.Split(result, "\n")

	// Should have maxLines + 1 (truncation message)
	if len(resultLines) != MaxLinesPerItem+1 {
		t.Errorf("expected %d lines, got %d", MaxLinesPerItem+1, len(resultLines))
	}
	if !strings.Contains(resultLines[MaxLinesPerItem], "more lines") {
		t.Errorf("last line should indicate truncation, got %q", resultLines[MaxLinesPerItem])
	}
}

func TestStreamView_ToggleStates(t *testing.T) {
	s := NewStreamView()

	if !s.IsToolInputEnabled() {
		t.Error("tool input should be enabled by default")
	}
	s.ToggleToolInput()
	if s.IsToolInputEnabled() {
		t.Error("tool input should be disabled after toggle")
	}

	if !s.IsToolOutputEnabled() {
		t.Error("tool output should be enabled by default")
	}
	s.ToggleToolOutput()
	if s.IsToolOutputEnabled() {
		t.Error("tool output should be disabled after toggle")
	}
}
