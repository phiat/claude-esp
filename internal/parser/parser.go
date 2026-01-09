package parser

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StreamItemType represents the type of content in a stream
type StreamItemType string

const (
	TypeThinking   StreamItemType = "thinking"
	TypeToolInput  StreamItemType = "tool_input"
	TypeToolOutput StreamItemType = "tool_output"
	TypeText       StreamItemType = "text"
)

// StreamItem represents a single item in the output stream
type StreamItem struct {
	Type      StreamItemType
	SessionID string    // which session this belongs to
	AgentID   string    // empty for main session, "abc123" for subagents
	AgentName string    // human-readable name derived from agent type or ID
	Timestamp time.Time
	Content   string
	ToolName  string // for tool_input/tool_output
	ToolID    string // to correlate input with output
}

// RawMessage represents a line from the JSONL file
type RawMessage struct {
	Type      string          `json:"type"`
	AgentID   string          `json:"agentId,omitempty"`
	SessionID string          `json:"sessionId"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// AssistantMessage represents the message field for assistant responses
type AssistantMessage struct {
	Role    string           `json:"role"`
	Content []ContentBlock   `json:"content"`
}

// ContentBlock represents a single content item in assistant response
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// UserMessage represents the message field for user messages (including tool results)
type UserMessage struct {
	Role    string        `json:"role"`
	Content []ToolResult  `json:"content,omitempty"`
}

// ToolResult represents a tool result in a user message
type ToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// ToolInput represents the input field for various tools
type ToolInput struct {
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Path        string `json:"path,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Content     string `json:"content,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	Query       string `json:"query,omitempty"`
}

// ParseLine parses a single JSONL line and returns stream items
func ParseLine(line string) ([]StreamItem, error) {
	if strings.TrimSpace(line) == "" {
		return nil, nil
	}

	var raw RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	timestamp, err := time.Parse(time.RFC3339, raw.Timestamp)
	if err != nil {
		timestamp = time.Now() // fallback to current time if parse fails
	}

	var items []StreamItem

	switch raw.Type {
	case "assistant":
		items = parseAssistantMessage(raw, timestamp)
	case "user":
		items = parseUserMessage(raw, timestamp)
	}

	return items, nil
}

func parseAssistantMessage(raw RawMessage, timestamp time.Time) []StreamItem {
	var msg AssistantMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil
	}

	var items []StreamItem
	agentName := "Main"
	if raw.AgentID != "" {
		agentName = fmt.Sprintf("Agent-%s", raw.AgentID[:min(7, len(raw.AgentID))])
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				items = append(items, StreamItem{
					Type:      TypeThinking,
					AgentID:   raw.AgentID,
					AgentName: agentName,
					Timestamp: timestamp,
					Content:   block.Thinking,
				})
			}
		case "text":
			if block.Text != "" {
				items = append(items, StreamItem{
					Type:      TypeText,
					AgentID:   raw.AgentID,
					AgentName: agentName,
					Timestamp: timestamp,
					Content:   block.Text,
				})
			}
		case "tool_use":
			content := formatToolInput(block.Name, block.Input)
			items = append(items, StreamItem{
				Type:      TypeToolInput,
				AgentID:   raw.AgentID,
				AgentName: agentName,
				Timestamp: timestamp,
				Content:   content,
				ToolName:  block.Name,
				ToolID:    block.ID,
			})
		}
	}

	return items
}

func parseUserMessage(raw RawMessage, timestamp time.Time) []StreamItem {
	// First try to parse as array of tool results
	var results []ToolResult
	if err := json.Unmarshal(raw.Message, &struct {
		Content *[]ToolResult `json:"content"`
	}{Content: &results}); err != nil {
		return nil
	}

	var items []StreamItem
	agentName := "Main"
	if raw.AgentID != "" {
		agentName = fmt.Sprintf("Agent-%s", raw.AgentID[:min(7, len(raw.AgentID))])
	}

	for _, result := range results {
		if result.Type == "tool_result" {
			items = append(items, StreamItem{
				Type:      TypeToolOutput,
				AgentID:   raw.AgentID,
				AgentName: agentName,
				Timestamp: timestamp,
				Content:   result.Content,
				ToolID:    result.ToolUseID,
			})
		}
	}

	return items
}

func formatToolInput(toolName string, inputRaw json.RawMessage) string {
	var input ToolInput
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		// Return raw JSON if we can't parse the input
		return string(inputRaw)
	}

	switch toolName {
	case "Bash":
		if input.Description != "" {
			return fmt.Sprintf("%s\n  # %s", input.Command, input.Description)
		}
		return input.Command
	case "Read":
		return input.FilePath
	case "Write":
		return fmt.Sprintf("%s (%d bytes)", input.FilePath, len(input.Content))
	case "Edit":
		return input.FilePath
	case "Glob":
		if input.Path != "" {
			return fmt.Sprintf("%s in %s", input.Pattern, input.Path)
		}
		return input.Pattern
	case "Grep":
		if input.Path != "" {
			return fmt.Sprintf("/%s/ in %s", input.Pattern, input.Path)
		}
		return fmt.Sprintf("/%s/", input.Pattern)
	case "WebFetch":
		return input.Prompt
	case "WebSearch":
		return input.Query
	case "Task":
		return input.Prompt
	default:
		// Return raw JSON for unknown tools
		return string(inputRaw)
	}
}
