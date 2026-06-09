package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
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

	// Input and output with same ToolID should both be kept (different types)
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
	s.AddItem(item2)

	if len(s.items) != 2 {
		t.Errorf("expected 2 items (input + output kept), got %d", len(s.items))
	}

	// Duplicate of same type + same ToolID should be skipped
	s.AddItem(item1)
	if len(s.items) != 2 {
		t.Errorf("expected 2 items (true duplicate skipped), got %d", len(s.items))
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

func TestTruncateContent_CJK(t *testing.T) {
	s := NewStreamView()

	// CJK characters are 3 bytes in UTF-8 but 2 display columns wide
	// This should not panic and should wrap correctly
	content := "# Step 5: 測試 focus-pane 回到原 pane"
	result := s.truncateContent(content, 20)

	// Should not panic, and each wrapped line should be <= 20 display columns
	for _, line := range strings.Split(result, "\n") {
		w := runewidth.StringWidth(line)
		if w > 20 {
			t.Errorf("wrapped line exceeds width 20: %q (display width %d)", line, w)
		}
	}
}

func TestTruncateContent_Emoji(t *testing.T) {
	s := NewStreamView()

	// Emoji are 4 bytes in UTF-8 but 2 display columns wide
	content := "Hello 🔧🔧🔧🔧🔧🔧🔧🔧🔧🔧 world"
	result := s.truncateContent(content, 15)

	for _, line := range strings.Split(result, "\n") {
		w := runewidth.StringWidth(line)
		if w > 15 {
			t.Errorf("wrapped line exceeds width 15: %q (display width %d)", line, w)
		}
	}
}

// TestStreamView_NarrowResizeDoesNotPanic guards against a regression where
// SetSize stored a small/negative raw width in s.width while clamping only
// the viewport, causing strings.Repeat in renderItem to panic with
// "negative Repeat count" once an item was rendered.
func TestStreamView_NarrowResizeDoesNotPanic(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)
	s.SetEnabledFilters([]EnabledFilter{{SessionID: "sess1", AgentID: ""}})
	// Cover every render branch that uses width.
	for _, typ := range []parser.StreamItemType{
		parser.TypeThinking,
		parser.TypeToolInput,
		parser.TypeToolOutput,
		parser.TypeText,
		parser.TypeHookOutput,
		parser.TypeDiagnostics,
		parser.TypeDebug,
	} {
		s.AddItem(newTestItem(typ, "sess1", "", "content"))
	}

	// Each width here corresponds to a real callsite scenario where
	// model.go passes m.width-m.treeWidth-5 (or similar) and the result
	// underflows the inner content width.
	for _, width := range []int{0, 1, 2, 3, 4, 5, -1, -10} {
		// Should never panic, even with an unrenderably narrow width.
		s.SetSize(width, 5)
		_ = s.View()
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

func TestStreamView_ToolIndexesPrunedOnTruncation(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	// Push well past MaxStreamItems with unique tool IDs; the dedup and
	// name indexes must track only the surviving items, not grow forever.
	for i := 0; i < MaxStreamItems+500; i++ {
		item := newTestItem(parser.TypeToolInput, "sess1", "", "input")
		item.ToolID = fmt.Sprintf("tool-%d", i)
		item.ToolName = "Bash"
		s.AddItem(item)
	}

	if len(s.items) != MaxStreamItems {
		t.Fatalf("expected %d items, got %d", MaxStreamItems, len(s.items))
	}
	if len(s.seenToolIDs) > MaxStreamItems {
		t.Errorf("seenToolIDs grew unboundedly: %d entries for %d items", len(s.seenToolIDs), len(s.items))
	}
	if len(s.toolNames) > MaxStreamItems {
		t.Errorf("toolNames grew unboundedly: %d entries for %d items", len(s.toolNames), len(s.items))
	}
}

func TestStreamView_ToolOutputLabelFromIndex(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)
	s.SetEnabledFilters([]EnabledFilter{{SessionID: "sess1", AgentID: ""}})

	in := newTestItem(parser.TypeToolInput, "sess1", "", "ls")
	in.ToolID = "t1"
	in.ToolName = "Bash"
	s.AddItem(in)

	out := newTestItem(parser.TypeToolOutput, "sess1", "", "file.txt")
	out.ToolID = "t1"
	s.AddItem(out)

	rendered := s.renderItem(out, 76)
	if !strings.Contains(rendered, "Bash result") {
		t.Errorf("expected output labeled with tool name, got %q", rendered)
	}
}

func TestStreamView_APIErrorMarker(t *testing.T) {
	s := NewStreamView()
	s.SetSize(80, 24)

	item := newTestItem(parser.TypeAPIError, "sess1", "", "529 Overloaded, retry 1/10")
	rendered := s.renderItem(item, 76)
	if !strings.Contains(rendered, "API error: 529 Overloaded, retry 1/10") {
		t.Errorf("unexpected api error rendering: %q", rendered)
	}
}
