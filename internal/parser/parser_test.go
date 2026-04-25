package parser

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseLine_EmptyLine(t *testing.T) {
	tests := []string{"", "   ", "\t", "\n"}
	for _, line := range tests {
		items, err := ParseLine(line)
		if err != nil {
			t.Errorf("ParseLine(%q) returned error: %v", line, err)
		}
		if items != nil {
			t.Errorf("ParseLine(%q) = %v, want nil", line, items)
		}
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	// Invalid JSON should be silently skipped, not return an error
	items, err := ParseLine("not json at all")
	if err != nil {
		t.Errorf("ParseLine should skip invalid JSON, got error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for invalid JSON, got %d", len(items))
	}
}

func TestParseLine_MissingTimestamp(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"test"}]}}`
	before := time.Now()
	items, err := ParseLine(line)
	after := time.Now()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Timestamp.Before(before) || items[0].Timestamp.After(after) {
		t.Error("missing timestamp should fall back to ~time.Now()")
	}
}

func TestParseLine_AssistantThinking(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"I need to analyze this"}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Type != TypeThinking {
		t.Errorf("type = %q, want %q", item.Type, TypeThinking)
	}
	if item.Content != "I need to analyze this" {
		t.Errorf("content = %q, want %q", item.Content, "I need to analyze this")
	}
	if item.AgentName != "Main" {
		t.Errorf("agentName = %q, want %q", item.AgentName, "Main")
	}
	if item.AgentID != "" {
		t.Errorf("agentID = %q, want empty", item.AgentID)
	}
}

func TestParseLine_AssistantToolUse(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    map[string]interface{}
		wantSub  string // substring expected in content
	}{
		{
			name:     "Bash",
			toolName: "Bash",
			input:    map[string]interface{}{"command": "ls -la", "description": "list files"},
			wantSub:  "ls -la",
		},
		{
			name:     "Read",
			toolName: "Read",
			input:    map[string]interface{}{"file_path": "/tmp/test.go"},
			wantSub:  "/tmp/test.go",
		},
		{
			name:     "Edit",
			toolName: "Edit",
			input:    map[string]interface{}{"file_path": "/tmp/test.go"},
			wantSub:  "/tmp/test.go",
		},
		{
			name:     "Glob",
			toolName: "Glob",
			input:    map[string]interface{}{"pattern": "**/*.go", "path": "/src"},
			wantSub:  "**/*.go",
		},
		{
			name:     "Grep",
			toolName: "Grep",
			input:    map[string]interface{}{"pattern": "TODO", "path": "/src"},
			wantSub:  "/TODO/",
		},
		{
			name:     "Write",
			toolName: "Write",
			input:    map[string]interface{}{"file_path": "/tmp/out.go", "content": "package main"},
			wantSub:  "/tmp/out.go",
		},
		{
			name:     "WebSearch",
			toolName: "WebSearch",
			input:    map[string]interface{}{"query": "golang testing"},
			wantSub:  "golang testing",
		},
		{
			name:     "Task",
			toolName: "Task",
			input:    map[string]interface{}{"prompt": "explore the codebase"},
			wantSub:  "explore the codebase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, _ := json.Marshal(tt.input)
			line := buildAssistantLine(t, tt.toolName, "toolu_test123", inputJSON)

			items, err := ParseLine(line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("expected 1 item, got %d", len(items))
			}
			item := items[0]
			if item.Type != TypeToolInput {
				t.Errorf("type = %q, want %q", item.Type, TypeToolInput)
			}
			if item.ToolName != tt.toolName {
				t.Errorf("toolName = %q, want %q", item.ToolName, tt.toolName)
			}
			if item.ToolID != "toolu_test123" {
				t.Errorf("toolID = %q, want %q", item.ToolID, "toolu_test123")
			}
			if !strings.Contains(item.Content, tt.wantSub) {
				t.Errorf("content = %q, want substring %q", item.Content, tt.wantSub)
			}
		})
	}
}

func TestParseLine_UserToolResult(t *testing.T) {
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"file contents here"}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Type != TypeToolOutput {
		t.Errorf("type = %q, want %q", item.Type, TypeToolOutput)
	}
	if item.ToolID != "toolu_abc" {
		t.Errorf("toolID = %q, want %q", item.ToolID, "toolu_abc")
	}
	if item.Content != "file contents here" {
		t.Errorf("content = %q, want %q", item.Content, "file contents here")
	}
}

func TestParseLine_MCPToolResult(t *testing.T) {
	// MCP tools return content as an array of content blocks, not a plain string
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_mcp1","content":[{"type":"text","text":"MCP result here"}]}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.Type != TypeToolOutput {
		t.Errorf("type = %q, want %q", item.Type, TypeToolOutput)
	}
	if item.ToolID != "toolu_mcp1" {
		t.Errorf("toolID = %q, want %q", item.ToolID, "toolu_mcp1")
	}
	if item.Content != "MCP result here" {
		t.Errorf("content = %q, want %q", item.Content, "MCP result here")
	}
}

func TestParseLine_MCPToolResultMultiBlock(t *testing.T) {
	// MCP tools can return multiple content blocks
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_mcp2","content":[{"type":"text","text":"block one"},{"type":"text","text":"block two"}]}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Content != "block one\nblock two" {
		t.Errorf("content = %q, want %q", items[0].Content, "block one\nblock two")
	}
}

func TestParseLine_SubagentMessage(t *testing.T) {
	line := `{"type":"assistant","agentId":"abc1234567890","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"subagent thinking"}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.AgentID != "abc1234567890" {
		t.Errorf("agentID = %q, want %q", item.AgentID, "abc1234567890")
	}
	if item.AgentName != "Agent-abc1234" {
		t.Errorf("agentName = %q, want %q", item.AgentName, "Agent-abc1234")
	}
}

func TestParseLine_MultipleBlocks(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"first thought"},{"type":"text","text":"hello"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Type != TypeThinking {
		t.Errorf("items[0].Type = %q, want %q", items[0].Type, TypeThinking)
	}
	if items[1].Type != TypeText {
		t.Errorf("items[1].Type = %q, want %q", items[1].Type, TypeText)
	}
	if items[2].Type != TypeToolInput {
		t.Errorf("items[2].Type = %q, want %q", items[2].Type, TypeToolInput)
	}
}

func TestParseLine_EmptyThinking(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":""}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty thinking, got %d", len(items))
	}
}

func TestParseLine_UnknownType(t *testing.T) {
	// System messages with unrecognized subtypes should be silently dropped.
	line := `{"type":"system","subtype":"something_else","timestamp":"2025-01-01T12:00:00Z","message":{}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for unknown system subtype, got %d", len(items))
	}
}

func TestParseLine_SessionTitleAgentName(t *testing.T) {
	line := `{"type":"agent-name","agentName":"auto-collapse-feature","sessionId":"sess-1"}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 title item, got %d", len(items))
	}
	if items[0].Type != TypeSessionTitle {
		t.Errorf("type = %q, want %q", items[0].Type, TypeSessionTitle)
	}
	if items[0].Content != "auto-collapse-feature" {
		t.Errorf("content = %q, want auto-collapse-feature", items[0].Content)
	}
	if items[0].SessionID != "sess-1" {
		t.Errorf("sessionID = %q, want sess-1", items[0].SessionID)
	}
}

func TestParseLine_SessionTitleCustomTitle(t *testing.T) {
	line := `{"type":"custom-title","customTitle":"my-custom-label","sessionId":"sess-2"}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Content != "my-custom-label" {
		t.Fatalf("expected my-custom-label, got %+v", items)
	}
}

func TestParseLine_TurnDuration(t *testing.T) {
	line := `{"type":"system","subtype":"turn_duration","timestamp":"2025-01-01T12:00:00Z","durationMs":41751,"messageCount":42,"sessionId":"abc"}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 turn marker, got %d", len(items))
	}
	if items[0].Type != TypeTurnMarker {
		t.Errorf("type = %q, want %q", items[0].Type, TypeTurnMarker)
	}
	if items[0].DurationMs != 41751 {
		t.Errorf("duration = %d, want 41751", items[0].DurationMs)
	}
}

func TestParseLine_CompactBoundary(t *testing.T) {
	line := `{"type":"system","subtype":"compact_boundary","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc","compactMetadata":{"trigger":"auto","preTokens":179698}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 compact marker, got %d", len(items))
	}
	if items[0].Type != TypeCompactMarker {
		t.Errorf("type = %q, want %q", items[0].Type, TypeCompactMarker)
	}
	if items[0].Content != "auto, 179k pre-tokens" {
		t.Errorf("content = %q, want %q", items[0].Content, "auto, 179k pre-tokens")
	}
	if items[0].SessionID != "abc" {
		t.Errorf("sessionID = %q, want abc", items[0].SessionID)
	}
}

func TestParseLine_CompactBoundary_NoMetadata(t *testing.T) {
	line := `{"type":"system","subtype":"compact_boundary","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc"}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Type != TypeCompactMarker {
		t.Fatalf("expected 1 compact marker, got %+v", items)
	}
	if items[0].Content != "" {
		t.Errorf("content = %q, want empty", items[0].Content)
	}
}

func TestParseLine_HookSuccess(t *testing.T) {
	line := `{"type":"attachment","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc","attachment":{"type":"hook_success","hookName":"SessionStart:startup","hookEvent":"SessionStart","stdout":"hello\nworld","exitCode":0,"durationMs":116,"command":"bd prime"}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 hook item, got %d", len(items))
	}
	if items[0].Type != TypeHookOutput {
		t.Errorf("type = %q, want %q", items[0].Type, TypeHookOutput)
	}
	if items[0].ToolName != "SessionStart:startup" {
		t.Errorf("toolName = %q, want SessionStart:startup", items[0].ToolName)
	}
	if items[0].DurationMs != 116 {
		t.Errorf("duration = %d, want 116", items[0].DurationMs)
	}
	if items[0].Content != "hello\nworld" {
		t.Errorf("content = %q, want hello\\nworld", items[0].Content)
	}
}

func TestParseLine_AttachmentUnknownSubtypeDropped(t *testing.T) {
	line := `{"type":"attachment","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc","attachment":{"type":"task_reminder","content":[],"itemCount":0}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items for unhandled subtype, got %d", len(items))
	}
}

func TestParseLine_Diagnostics(t *testing.T) {
	line := `{"type":"attachment","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc","attachment":{"type":"diagnostics","files":[{"uri":"/path/to/foo.go","diagnostics":[{"message":"unused parameter","severity":"Info","source":"unusedparams"},{"message":"loop can be modernized","severity":"Hint","source":"rangeint"}]}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 diagnostics item, got %d", len(items))
	}
	if items[0].Type != TypeDiagnostics {
		t.Errorf("type = %q, want %q", items[0].Type, TypeDiagnostics)
	}
	if items[0].ToolName != "foo.go (1 info, 1 hint)" {
		t.Errorf("toolName = %q, want %q", items[0].ToolName, "foo.go (1 info, 1 hint)")
	}
	if !strings.Contains(items[0].Content, "[Info] unused parameter (unusedparams)") {
		t.Errorf("content missing first diagnostic: %q", items[0].Content)
	}
}

func TestParseLine_DebugAll(t *testing.T) {
	prev := DebugAll
	DebugAll = true
	t.Cleanup(func() { DebugAll = prev })

	tests := []struct {
		name      string
		line      string
		wantLabel string
	}{
		{"unknown top-level", `{"type":"file-history-snapshot","sessionId":"s","timestamp":"2025-01-01T12:00:00Z"}`, "file-history-snapshot"},
		{"system unknown subtype", `{"type":"system","subtype":"foo","sessionId":"s","timestamp":"2025-01-01T12:00:00Z"}`, "system:foo"},
		{"attachment unhandled", `{"type":"attachment","sessionId":"s","timestamp":"2025-01-01T12:00:00Z","attachment":{"type":"task_reminder","content":[],"itemCount":0}}`, "attachment.task_reminder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items, err := ParseLine(tc.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(items) != 1 || items[0].Type != TypeDebug {
				t.Fatalf("expected 1 debug item, got %+v", items)
			}
			if items[0].ToolName != tc.wantLabel {
				t.Errorf("label = %q, want %q", items[0].ToolName, tc.wantLabel)
			}
		})
	}
}

func TestParseLine_DebugAllSkipsHandledLines(t *testing.T) {
	prev := DebugAll
	DebugAll = true
	t.Cleanup(func() { DebugAll = prev })

	// pr-link is handled → should NOT also produce a debug item.
	line := `{"type":"pr-link","sessionId":"s","prNumber":1,"prUrl":"http://x","prRepository":"a/b","timestamp":"2025-01-01T12:00:00Z"}`
	items, _ := ParseLine(line)
	if len(items) != 1 || items[0].Type != TypePRLink {
		t.Fatalf("expected exactly 1 pr_link item, got %+v", items)
	}
}

func TestParseLine_PRLink(t *testing.T) {
	line := `{"type":"pr-link","sessionId":"abc","prNumber":13,"prUrl":"https://github.com/phiat/claude-esp/pull/13","prRepository":"phiat/claude-esp","timestamp":"2025-01-01T12:00:00Z"}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Type != TypePRLink {
		t.Fatalf("expected 1 pr_link item, got %+v", items)
	}
	want := "PR #13 phiat/claude-esp → https://github.com/phiat/claude-esp/pull/13"
	if items[0].Content != want {
		t.Errorf("content = %q, want %q", items[0].Content, want)
	}
}

func TestParseLine_DiagnosticsEmptyFilesSkipped(t *testing.T) {
	line := `{"type":"attachment","timestamp":"2025-01-01T12:00:00Z","sessionId":"abc","attachment":{"type":"diagnostics","files":[{"uri":"/x.go","diagnostics":[]}]}}`
	items, _ := ParseLine(line)
	if len(items) != 0 {
		t.Fatalf("expected files with no diagnostics to be skipped, got %d items", len(items))
	}
}

func TestFormatToolInput(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		wantSub  string
	}{
		{"Bash with desc", "Bash", `{"command":"npm install","description":"install deps"}`, "# install deps"},
		{"Bash no desc", "Bash", `{"command":"npm install"}`, "npm install"},
		{"Read", "Read", `{"file_path":"/foo/bar.go"}`, "/foo/bar.go"},
		{"Write size", "Write", `{"file_path":"/foo/bar.go","content":"abc"}`, "3 bytes"},
		{"Glob with path", "Glob", `{"pattern":"*.go","path":"/src"}`, "*.go in /src"},
		{"Glob no path", "Glob", `{"pattern":"*.go"}`, "*.go"},
		{"Grep with path", "Grep", `{"pattern":"TODO","path":"/src"}`, "/TODO/ in /src"},
		{"Grep no path", "Grep", `{"pattern":"TODO"}`, "/TODO/"},
		{"Agent with desc", "Agent", `{"description":"audit deps","prompt":"check all deps"}`, "audit deps"},
		{"Agent prompt fallback", "Agent", `{"prompt":"do a thing"}`, "do a thing"},
		{"Task legacy alias", "Task", `{"description":"legacy task"}`, "legacy task"},
		{"Skill with args", "Skill", `{"skill":"beads:create","args":"--title x"}`, "beads:create — --title x"},
		{"Skill no args", "Skill", `{"skill":"beads:list"}`, "beads:list"},
		{"ToolSearch", "ToolSearch", `{"query":"select:Read","max_results":1}`, "select:Read"},
		{"ScheduleWakeup reason", "ScheduleWakeup", `{"delaySeconds":90,"reason":"watching build"}`, "watching build"},
		{"ScheduleWakeup delay only", "ScheduleWakeup", `{"delaySeconds":90}`, "delay 90s"},
		{"TaskCreate", "TaskCreate", `{"subject":"write docs","activeForm":"writing"}`, "write docs"},
		{"TaskUpdate", "TaskUpdate", `{"taskId":"42","status":"in_progress"}`, "task 42"},
		{"TaskStop", "TaskStop", `{"task_id":"abc123"}`, "abc123"},
		{"EnterPlanMode", "EnterPlanMode", `{}`, "enter plan mode"},
		{"ExitPlanMode", "ExitPlanMode", `{}`, "exit plan mode"},
		{"CronCreate", "CronCreate", `{"cron":"*/5 * * * *","prompt":"ping","recurring":true}`, "*/5 * * * *"},
		{"Unknown tool", "CustomTool", `{"foo":"bar"}`, `"foo"`},
		{"Invalid JSON", "Bash", `not json`, "not json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolInput(tt.toolName, json.RawMessage(tt.input))
			if !strings.Contains(result, tt.wantSub) {
				t.Errorf("formatToolInput(%q, %q) = %q, want substring %q", tt.toolName, tt.input, result, tt.wantSub)
			}
		})
	}
}

func TestParseLine_UserMessageWithImage(t *testing.T) {
	// User messages can contain image blocks (screenshots pasted into Claude Code)
	// These should be silently skipped, not cause errors
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"}}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine should not error on image content, got: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items (images skipped), got %d", len(items))
	}
}

func TestParseLine_UserMessageWithImageAndToolResult(t *testing.T) {
	// A user message can contain both image blocks and tool results
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBOR"}},{"type":"tool_result","tool_use_id":"toolu_img1","content":"tool output here"}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (tool_result only), got %d", len(items))
	}
	if items[0].Content != "tool output here" {
		t.Errorf("content = %q, want %q", items[0].Content, "tool output here")
	}
}

func TestParseLine_TruncatedJSON(t *testing.T) {
	// When a JSONL line exceeds the scanner buffer, it gets truncated
	// producing invalid JSON. This should be skipped gracefully, not crash.
	truncated := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"JVBER`
	items, err := ParseLine(truncated)
	if err != nil {
		t.Fatalf("ParseLine should gracefully skip truncated JSON, got error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for truncated JSON, got %d", len(items))
	}
}

// buildAssistantLine builds a valid JSONL line for an assistant tool_use message
func buildAssistantLine(t *testing.T, toolName, toolID string, inputJSON json.RawMessage) string {
	t.Helper()
	msg := map[string]interface{}{
		"type":      "assistant",
		"timestamp": "2025-01-01T12:00:00Z",
		"message": map[string]interface{}{
			"role": "assistant",
			"content": []map[string]interface{}{
				{
					"type":  "tool_use",
					"id":    toolID,
					"name":  toolName,
					"input": json.RawMessage(inputJSON),
				},
			},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to build test line: %v", err)
	}
	return string(data)
}

func TestParseLine_TokenUsageInAssistantMessage(t *testing.T) {
	// Test that usage data is correctly extracted from assistant messages
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hello world"}],"usage":{"input_tokens":123,"output_tokens":456}}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.InputTokens != 123 {
		t.Errorf("InputTokens = %d, want 123", item.InputTokens)
	}
	if item.OutputTokens != 456 {
		t.Errorf("OutputTokens = %d, want 456", item.OutputTokens)
	}
}

func TestParseLine_MultipleBlocks_TokensOnFirstBlock(t *testing.T) {
	// When an assistant message has multiple content blocks, tokens should be
	// attached to the FIRST item only (not duplicated across all blocks)
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"I think"},{"type":"text","text":"I respond"}],"usage":{"input_tokens":100,"output_tokens":200}}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// First item (thinking) should have token data
	if items[0].InputTokens != 100 {
		t.Errorf("items[0].InputTokens = %d, want 100", items[0].InputTokens)
	}
	if items[0].OutputTokens != 200 {
		t.Errorf("items[0].OutputTokens = %d, want 200", items[0].OutputTokens)
	}

	// Second item (text) should NOT have token data
	if items[1].InputTokens != 0 {
		t.Errorf("items[1].InputTokens = %d, want 0 (tokens only on first item)", items[1].InputTokens)
	}
	if items[1].OutputTokens != 0 {
		t.Errorf("items[1].OutputTokens = %d, want 0 (tokens only on first item)", items[1].OutputTokens)
	}
}

func TestParseLine_NoUsageInMessage(t *testing.T) {
	// Test that missing usage data results in 0 tokens
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", item.InputTokens)
	}
	if item.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", item.OutputTokens)
	}
}

func TestPrettyToolName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Bash", "Bash"},
		{"Read", "Read"},
		{"Skill", "Skill"},
		{"mcp__plugin_context7_context7__query-docs", "mcp:query-docs"},
		{"mcp__context7__resolve", "mcp:resolve"},
		{"mcp__claude_ai_Gmail__authenticate", "mcp:authenticate"},
		{"mcp__weird", "mcp__weird"}, // no trailing __method — passthrough
	}
	for _, tt := range tests {
		if got := PrettyToolName(tt.in); got != tt.want {
			t.Errorf("PrettyToolName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseLine_MCPToolUse_PrettifiesName(t *testing.T) {
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__plugin_context7_context7__query-docs","input":{"library":"react"}}]}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ToolName != "mcp:query-docs" {
		t.Errorf("ToolName = %q, want %q", items[0].ToolName, "mcp:query-docs")
	}
}

func TestParseLine_CacheTokensInAssistantMessage(t *testing.T) {
	// cache_creation_input_tokens and cache_read_input_tokens are often much
	// larger than the naked input_tokens, so undercounting them misleads users.
	line := `{"type":"assistant","timestamp":"2025-01-01T12:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":35656,"cache_read_input_tokens":1234}}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	if item.CacheCreationTokens != 35656 {
		t.Errorf("CacheCreationTokens = %d, want 35656", item.CacheCreationTokens)
	}
	if item.CacheReadTokens != 1234 {
		t.Errorf("CacheReadTokens = %d, want 1234", item.CacheReadTokens)
	}
	// existing fields still correct
	if item.InputTokens != 10 || item.OutputTokens != 5 {
		t.Errorf("Input/Output = %d/%d, want 10/5", item.InputTokens, item.OutputTokens)
	}
}

func TestParseLine_UserMessageHasNoTokens(t *testing.T) {
	// User messages should never have token data (they are not in the Anthropic API response)
	line := `{"type":"user","timestamp":"2025-01-01T12:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"result data"}],"usage":{"input_tokens":999,"output_tokens":999}}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	item := items[0]
	// Even if usage is in the JSON, it should be ignored for user messages
	if item.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (user messages don't have usage)", item.InputTokens)
	}
	if item.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0 (user messages don't have usage)", item.OutputTokens)
	}
}
