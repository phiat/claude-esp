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
	_, err := ParseLine("not json at all")
	if err == nil {
		t.Error("ParseLine with invalid JSON should return error")
	}
	if !strings.Contains(err.Error(), "failed to parse JSON") {
		t.Errorf("error should mention JSON parsing, got: %v", err)
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
	line := `{"type":"system","timestamp":"2025-01-01T12:00:00Z","message":{}}`
	items, err := ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for unknown type, got %d", len(items))
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
