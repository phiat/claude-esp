package parser

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// StreamItemType represents the type of content in a stream
type StreamItemType string

const (
	TypeThinking      StreamItemType = "thinking"
	TypeToolInput     StreamItemType = "tool_input"
	TypeToolOutput    StreamItemType = "tool_output"
	TypeText          StreamItemType = "text"
	TypeTurnMarker    StreamItemType = "turn_marker"    // turn boundary + duration (system.turn_duration)
	TypeCompactMarker StreamItemType = "compact_marker" // conversation compaction boundary (system.compact_boundary)
	TypeHookOutput    StreamItemType = "hook_output"    // hook execution result (attachment.hook_success)
	TypeDiagnostics   StreamItemType = "diagnostics"    // post-edit LSP diagnostics (attachment.diagnostics)
	TypePRLink        StreamItemType = "pr_link"        // PR creation event (type=pr-link)
	TypeDebug         StreamItemType = "debug"          // raw line type/subtype (only emitted when DebugAll is on)
	TypeSessionTitle  StreamItemType = "session_title"  // session label update (agent-name / custom-title)
	TypeCacheMiss     StreamItemType = "cache_miss"     // prompt-cache invalidation (assistant.diagnostics.cache_miss_reason)
	TypeSessionEvent  StreamItemType = "session_event"  // misc session-state change (queue-op, plan/auto mode, tool/MCP/skill deltas, permission mode, away recap)
	TypeAPIError      StreamItemType = "api_error"      // API request failure + retry progress (system.api_error)
	TypeArtifactLink  StreamItemType = "artifact_link"  // artifact published to claude.ai (type=frame-link)

	// AgentIDDisplayLength is how many chars of agent ID to show in display name
	AgentIDDisplayLength = 7

	// debugPreviewLen caps the raw-line preview shown in TypeDebug items.
	debugPreviewLen = 240
)

// DebugAll, when true, makes ParseLine emit a TypeDebug stream item for every
// line whose type (or attachment subtype) is otherwise dropped by the parser.
// Set this once at startup based on a CLI flag; safe to leave at false in
// production. Reads/writes are not synchronized — flip before parsing starts.
var DebugAll bool

// agentDisplayName returns "Main" for the top-level session or "Agent-<id>"
// (truncated to AgentIDDisplayLength) for subagents.
func agentDisplayName(agentID string) string {
	if agentID == "" {
		return "Main"
	}
	return fmt.Sprintf("Agent-%s", agentID[:min(AgentIDDisplayLength, len(agentID))])
}

// StreamItem represents a single item in the output stream
type StreamItem struct {
	Type                StreamItemType
	SessionID           string // which session this belongs to
	AgentID             string // empty for main session, "abc123" for subagents
	AgentName           string // human-readable name derived from agent type or ID
	Timestamp           time.Time
	Content             string
	ToolName            string // for tool_input/tool_output
	ToolID              string // to correlate input with output
	DurationMs          int64  // tool execution duration in ms (0 = not available)
	InputTokens         int64  // usage.input_tokens from assistant messages
	OutputTokens        int64  // usage.output_tokens from assistant messages
	CacheCreationTokens int64  // usage.cache_creation_input_tokens
	CacheReadTokens     int64  // usage.cache_read_input_tokens
	Model               string // message.model from assistant messages (e.g. "claude-opus-4-7")
}

// RawMessage represents a line from the JSONL file
type RawMessage struct {
	Type          string          `json:"type"`
	Subtype       string          `json:"subtype,omitempty"`
	AgentID       string          `json:"agentId,omitempty"`
	SessionID     string          `json:"sessionId"`
	Timestamp     string          `json:"timestamp"`
	DurationMs    int64           `json:"durationMs,omitempty"`
	MessageCount  int             `json:"messageCount,omitempty"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult,omitempty"`
	// AgentTitle, CustomTitle and AITitle carry session-level labels on
	// type="agent-name", type="custom-title" and type="ai-title" lines
	// respectively. agent-name is the legacy spelling; Claude Code now emits
	// ai-title (which has no timestamp field).
	AgentTitle  string `json:"agentName,omitempty"`
	CustomTitle string `json:"customTitle,omitempty"`
	AITitle     string `json:"aiTitle,omitempty"`
	// PermissionMode carries the mode on type="permission-mode" lines
	// ("default", "auto", ...).
	PermissionMode string `json:"permissionMode,omitempty"`
	// CompactMetadata carries trigger + preTokens on system.compact_boundary lines.
	CompactMetadata *CompactMetadata `json:"compactMetadata,omitempty"`
	// Attachment carries hook output / diagnostics / etc on type="attachment" lines.
	Attachment *Attachment `json:"attachment,omitempty"`
	// PR link fields (type=pr-link).
	PRNumber     int    `json:"prNumber,omitempty"`
	PRURL        string `json:"prUrl,omitempty"`
	PRRepository string `json:"prRepository,omitempty"`
	// Queue-operation fields (type="queue-operation"): "enqueue" or "remove",
	// with the queued prompt body in Content. Content also carries the recap
	// text on system.away_summary lines.
	Operation string `json:"operation,omitempty"`
	Content   string `json:"content,omitempty"`
	// Level is the severity on system.informational lines ("info", "warning").
	Level string `json:"level,omitempty"`
	// API-error fields (system.api_error).
	APIError     *APIErrorDetail `json:"error,omitempty"`
	RetryAttempt int             `json:"retryAttempt,omitempty"`
	MaxRetries   int             `json:"maxRetries,omitempty"`
	// Artifact-link fields (type="frame-link"). Only the first line for an
	// artifact carries the URL and title; follow-ups carry just ArtifactCount.
	FrameURL      string `json:"frameUrl,omitempty"`
	Title         string `json:"title,omitempty"`
	ArtifactCount int    `json:"artifactCount,omitempty"`
	// ContinuedInSessionID is the successor session on type="continued-in".
	ContinuedInSessionID string `json:"continuedInSessionId,omitempty"`
	// Cost-state fields (type="cost-state"), written at session end. The
	// line has no timestamp; StartTime+TotalDuration (both ms) reconstructs it.
	TotalCostUSD      float64               `json:"totalCostUSD,omitempty"`
	TotalLinesAdded   int64                 `json:"totalLinesAdded,omitempty"`
	TotalLinesRemoved int64                 `json:"totalLinesRemoved,omitempty"`
	TotalDuration     int64                 `json:"totalDuration,omitempty"`
	StartTime         int64                 `json:"startTime,omitempty"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage,omitempty"`
}

// ModelUsage is one model's entry in cost-state.modelUsage. Only the cost is
// read; token counts are already tracked per assistant message.
type ModelUsage struct {
	CostUSD float64 `json:"costUSD,omitempty"`
}

// APIErrorDetail is the error payload on system.api_error lines.
type APIErrorDetail struct {
	Formatted string `json:"formatted,omitempty"` // e.g. "529 Overloaded"
	Status    int    `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
}

// CompactMetadata describes a conversation-compaction event.
type CompactMetadata struct {
	Trigger   string `json:"trigger"`
	PreTokens int64  `json:"preTokens"`
}

// Attachment is the payload on type="attachment" lines. Subtype-dependent
// fields are kept in one struct to avoid per-subtype unmarshalling.
type Attachment struct {
	Type      string `json:"type"`
	HookName  string `json:"hookName,omitempty"`
	HookEvent string `json:"hookEvent,omitempty"`
	// Content is omitted because subtypes disagree on its shape
	// (hook_success: string; task_reminder: array). Use Stdout for hooks.
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Command    string `json:"command,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	// Diagnostics fields (attachment.type=diagnostics)
	Files []DiagnosticFile `json:"files,omitempty"`
	// plan_mode_exit
	PlanFilePath string `json:"planFilePath,omitempty"`
	PlanExists   bool   `json:"planExists,omitempty"`
	// deferred_tools_delta and mcp_instructions_delta share these
	AddedNames   []string `json:"addedNames,omitempty"`
	RemovedNames []string `json:"removedNames,omitempty"`
	ReaddedNames []string `json:"readdedNames,omitempty"`
	// skill_listing
	SkillCount int  `json:"skillCount,omitempty"`
	IsInitial  bool `json:"isInitial,omitempty"`
	// task_status (background subagent progress)
	TaskID       string `json:"taskId,omitempty"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status,omitempty"`
	DeltaSummary string `json:"deltaSummary,omitempty"`
}

// DiagnosticFile is one file's worth of LSP diagnostics.
type DiagnosticFile struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostic is a single LSP finding.
type Diagnostic struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
	Code     string `json:"code"`
}

// RawToolUseResult represents the toolUseResult field on user messages
type RawToolUseResult struct {
	DurationMs int64 `json:"durationMs"`
}

// AssistantMessage represents the message field for assistant responses
type AssistantMessage struct {
	Role        string                `json:"role"`
	Model       string                `json:"model,omitempty"`
	Content     []ContentBlock        `json:"content"`
	Usage       *UsageInfo            `json:"usage,omitempty"`
	Diagnostics *AssistantDiagnostics `json:"diagnostics,omitempty"`
}

// UsageInfo represents token usage from assistant messages
type UsageInfo struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// AssistantDiagnostics carries the diagnostics object on assistant messages.
// Present (non-null) when the prompt cache busts; CacheMissReason explains
// why and how many tokens had to be re-sent.
type AssistantDiagnostics struct {
	CacheMissReason *CacheMissReason `json:"cache_miss_reason,omitempty"`
}

// CacheMissReason carries the cache-invalidation cause. Observed values for
// Type: "tools_changed" (tool list mutated mid-session, common after
// ToolSearch); "previous_message_not_found" (broken parent chain).
type CacheMissReason struct {
	Type                   string `json:"type"`
	CacheMissedInputTokens int64  `json:"cache_missed_input_tokens,omitempty"`
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
	Role    string       `json:"role"`
	Content []ToolResult `json:"content,omitempty"`
}

// ToolResult represents a tool result in a user message
type ToolResult struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ToolInput represents the input field for various tools
type ToolInput struct {
	Command      string `json:"command,omitempty"`
	Description  string `json:"description,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	Path         string `json:"path,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	Content      string `json:"content,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	Query        string `json:"query,omitempty"`
	Skill        string `json:"skill,omitempty"`
	Args         string `json:"args,omitempty"`
	Reason       string `json:"reason,omitempty"`
	DelaySeconds int64  `json:"delaySeconds,omitempty"`
	Subject      string `json:"subject,omitempty"`
	TaskID       string `json:"taskId,omitempty"`
	TaskIDSnake  string `json:"task_id,omitempty"`
	Cron         string `json:"cron,omitempty"`
	// SendUserFile
	Files   []string `json:"files,omitempty"`
	Caption string   `json:"caption,omitempty"`
	// AskUserQuestion
	Questions []ToolInputQuestion `json:"questions,omitempty"`
	// SendMessage
	To      string `json:"to,omitempty"`
	Message string `json:"message,omitempty"`
	// Artifact
	Action string `json:"action,omitempty"`
	URL    string `json:"url,omitempty"`
}

// ToolInputQuestion is one entry in AskUserQuestion's questions array.
type ToolInputQuestion struct {
	Question string `json:"question"`
}

// ParseLine parses a single JSONL line and returns stream items
func ParseLine(line string) ([]StreamItem, error) {
	if strings.TrimSpace(line) == "" {
		return nil, nil
	}

	var raw RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		// Gracefully skip malformed/truncated lines (e.g. base64 images
		// that exceeded the scanner buffer). A single bad line shouldn't
		// crash the app.
		return nil, nil
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
	case "system":
		items = parseSystemMessage(raw, timestamp)
		if DebugAll && len(items) == 0 {
			items = []StreamItem{debugItem(raw, line, timestamp)}
		}
	case "agent-name":
		items = parseSessionTitle(raw, timestamp, raw.AgentTitle)
	case "custom-title":
		items = parseSessionTitle(raw, timestamp, raw.CustomTitle)
	case "ai-title":
		items = parseSessionTitle(raw, timestamp, raw.AITitle)
	case "permission-mode":
		if raw.PermissionMode != "" {
			items = sessionEvent(raw, timestamp, agentDisplayName(raw.AgentID), "permission mode", raw.PermissionMode)
		}
	case "attachment":
		items = parseAttachment(raw, timestamp)
		if DebugAll && len(items) == 0 {
			items = []StreamItem{debugItem(raw, line, timestamp)}
		}
	case "pr-link":
		items = parsePRLink(raw, timestamp)
	case "frame-link":
		items = parseFrameLink(raw, timestamp)
		if DebugAll && len(items) == 0 {
			items = []StreamItem{debugItem(raw, line, timestamp)}
		}
	case "continued-in":
		if raw.ContinuedInSessionID != "" {
			items = sessionEvent(raw, timestamp, agentDisplayName(raw.AgentID), "continued in", raw.ContinuedInSessionID)
		}
	case "cost-state":
		items = parseCostState(raw, timestamp)
	case "queue-operation":
		items = parseQueueOperation(raw, timestamp)
	default:
		if DebugAll {
			items = []StreamItem{debugItem(raw, line, timestamp)}
		}
	}

	return items, nil
}

// debugItem builds a TypeDebug stream item describing a line that the parser
// would otherwise drop. The label is "<type>" or "<type>:<subtype>" for system
// lines, or "attachment.<subtype>" for attachments. Content is a truncated
// raw-JSON preview to help diagnose new fields.
func debugItem(raw RawMessage, line string, timestamp time.Time) StreamItem {
	label := raw.Type
	switch {
	case raw.Type == "system" && raw.Subtype != "":
		label = "system:" + raw.Subtype
	case raw.Type == "attachment" && raw.Attachment != nil && raw.Attachment.Type != "":
		label = "attachment." + raw.Attachment.Type
	}
	preview := line
	if len(preview) > debugPreviewLen {
		// Back off to a rune boundary: the raw line is unescaped UTF-8, so a
		// multi-byte character can straddle debugPreviewLen.
		end := debugPreviewLen
		for end > 0 && !utf8.RuneStart(preview[end]) {
			end--
		}
		preview = preview[:end] + "…"
	}
	agentName := agentDisplayName(raw.AgentID)
	return StreamItem{
		Type:      TypeDebug,
		SessionID: raw.SessionID,
		AgentID:   raw.AgentID,
		AgentName: agentName,
		Timestamp: timestamp,
		ToolName:  label,
		Content:   preview,
	}
}

// parseAttachment dispatches on attachment.type. Surfaces hook_success and
// diagnostics; every other subtype is intentionally dropped (the DebugAll
// flag will surface the rest as TypeDebug items).
func parseAttachment(raw RawMessage, timestamp time.Time) []StreamItem {
	if raw.Attachment == nil {
		return nil
	}
	agentName := agentDisplayName(raw.AgentID)

	switch raw.Attachment.Type {
	case "hook_success":
		body := raw.Attachment.Stdout
		return []StreamItem{{
			Type:       TypeHookOutput,
			SessionID:  raw.SessionID,
			AgentID:    raw.AgentID,
			AgentName:  agentName,
			Timestamp:  timestamp,
			ToolName:   raw.Attachment.HookName,
			Content:    body,
			DurationMs: raw.Attachment.DurationMs,
		}}
	case "diagnostics":
		return diagnosticsItems(raw, timestamp, agentName)
	case "plan_mode_exit":
		return sessionEvent(raw, timestamp, agentName, "plan mode exit", planModeExitDetail(raw.Attachment))
	case "auto_mode":
		return sessionEvent(raw, timestamp, agentName, "auto mode", "")
	case "deferred_tools_delta":
		if detail := nameDeltaDetail(raw.Attachment); detail != "" {
			return sessionEvent(raw, timestamp, agentName, "tools", detail)
		}
	case "mcp_instructions_delta":
		if detail := nameDeltaDetail(raw.Attachment); detail != "" {
			return sessionEvent(raw, timestamp, agentName, "MCP", detail)
		}
	case "skill_listing":
		// Initial listing is the session-start bulk dump — drop.
		// Updates surface as a compact count.
		if !raw.Attachment.IsInitial && raw.Attachment.SkillCount > 0 {
			return sessionEvent(raw, timestamp, agentName, "skills", fmt.Sprintf("%d total", raw.Attachment.SkillCount))
		}
	case "task_status":
		// Background subagent progress: "task running: <description> — <delta>".
		if detail := taskStatusDetail(raw.Attachment); detail != "" {
			label := "task"
			if raw.Attachment.Status != "" {
				label = "task " + raw.Attachment.Status
			}
			return sessionEvent(raw, timestamp, agentName, label, detail)
		}
	}
	return nil
}

// taskStatusDetail joins a task_status attachment's description and delta
// summary. Returns "" when both are empty (caller should drop the event).
func taskStatusDetail(a *Attachment) string {
	if a == nil {
		return ""
	}
	switch {
	case a.Description != "" && a.DeltaSummary != "":
		return a.Description + " — " + a.DeltaSummary
	case a.Description != "":
		return a.Description
	default:
		return a.DeltaSummary
	}
}

// sessionEvent builds a TypeSessionEvent marker. label goes in ToolName so
// the renderer can show "<label>: <detail>" or just "<label>" when detail
// is empty.
func sessionEvent(raw RawMessage, timestamp time.Time, agentName, label, detail string) []StreamItem {
	return []StreamItem{{
		Type:      TypeSessionEvent,
		SessionID: raw.SessionID,
		AgentID:   raw.AgentID,
		AgentName: agentName,
		Timestamp: timestamp,
		ToolName:  label,
		Content:   detail,
	}}
}

// planModeExitDetail returns "saved" when the plan was written to disk,
// otherwise "" so the marker reads simply "plan mode exit".
func planModeExitDetail(a *Attachment) string {
	if a != nil && a.PlanExists {
		return "saved"
	}
	return ""
}

// nameDeltaDetail summarises added/removed/readded name lists into a compact
// "+3 -1" detail. Returns "" when nothing changed (caller should drop the
// event).
func nameDeltaDetail(a *Attachment) string {
	if a == nil {
		return ""
	}
	var parts []string
	if n := len(a.AddedNames); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d", n))
	}
	if n := len(a.RemovedNames); n > 0 {
		parts = append(parts, fmt.Sprintf("-%d", n))
	}
	if n := len(a.ReaddedNames); n > 0 {
		parts = append(parts, fmt.Sprintf("~%d", n))
	}
	return strings.Join(parts, " ")
}

// parseQueueOperation surfaces top-level type="queue-operation" lines, which
// CC emits when the user enqueues a follow-up prompt or removes one from the
// queue. The enqueue carries the prompt body; the remove is a bare ack.
// (attachment.queued_command duplicates this pair — same prompt, emitted at
// delivery time — so it is intentionally left to the DebugAll path.)
//
// Enqueues whose content is a <task-notification> blob are dropped — those
// are internal TaskStop result re-injections, not user-typed prompts.
func parseQueueOperation(raw RawMessage, timestamp time.Time) []StreamItem {
	agentName := agentDisplayName(raw.AgentID)
	switch raw.Operation {
	case "enqueue":
		if strings.HasPrefix(strings.TrimSpace(raw.Content), "<task-notification>") {
			return nil
		}
		return sessionEvent(raw, timestamp, agentName, "queued", raw.Content)
	case "remove":
		return sessionEvent(raw, timestamp, agentName, "dequeued", "")
	}
	return nil
}

// diagnosticsItems turns one diagnostics attachment (potentially multi-file)
// into one StreamItem per file. Files with zero diagnostics are skipped.
func diagnosticsItems(raw RawMessage, timestamp time.Time, agentName string) []StreamItem {
	var items []StreamItem
	for _, f := range raw.Attachment.Files {
		if len(f.Diagnostics) == 0 {
			continue
		}
		items = append(items, StreamItem{
			Type:      TypeDiagnostics,
			SessionID: raw.SessionID,
			AgentID:   raw.AgentID,
			AgentName: agentName,
			Timestamp: timestamp,
			ToolName:  diagnosticsHeader(f),
			Content:   diagnosticsBody(f.Diagnostics),
		})
	}
	return items
}

// diagnosticsHeader returns "<file> (2 errors, 5 hints)".
func diagnosticsHeader(f DiagnosticFile) string {
	counts := map[string]int{}
	for _, d := range f.Diagnostics {
		counts[strings.ToLower(d.Severity)]++
	}
	var parts []string
	for _, sev := range []string{"error", "warning", "info", "hint"} {
		if n := counts[sev]; n > 0 {
			label := sev + "s"
			if n == 1 {
				label = sev
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	name := f.URI
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(parts, ", "))
}

// diagnosticsBody renders each diagnostic as "[severity] message (source)".
func diagnosticsBody(ds []Diagnostic) string {
	lines := make([]string, 0, len(ds))
	for _, d := range ds {
		sev := d.Severity
		if sev == "" {
			sev = "?"
		}
		line := fmt.Sprintf("[%s] %s", sev, d.Message)
		if d.Source != "" {
			line += fmt.Sprintf(" (%s)", d.Source)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// parsePRLink emits a TypePRLink marker for type="pr-link" events.
func parsePRLink(raw RawMessage, timestamp time.Time) []StreamItem {
	if raw.PRNumber == 0 && raw.PRURL == "" {
		return nil
	}
	var content string
	switch {
	case raw.PRRepository != "" && raw.PRURL != "":
		content = fmt.Sprintf("PR #%d %s → %s", raw.PRNumber, raw.PRRepository, raw.PRURL)
	case raw.PRURL != "":
		content = fmt.Sprintf("PR #%d → %s", raw.PRNumber, raw.PRURL)
	default:
		content = fmt.Sprintf("PR #%d", raw.PRNumber)
	}
	return []StreamItem{{
		Type:      TypePRLink,
		SessionID: raw.SessionID,
		Timestamp: timestamp,
		Content:   content,
	}}
}

// parseFrameLink surfaces type="frame-link" lines, written when the Artifact
// tool publishes a page to claude.ai. The first line for an artifact carries
// its title and URL; later lines (redeploys, watch bookkeeping) carry only an
// artifact count and are dropped.
func parseFrameLink(raw RawMessage, timestamp time.Time) []StreamItem {
	if raw.FrameURL == "" {
		return nil
	}
	content := "artifact → " + raw.FrameURL
	if raw.Title != "" {
		content = fmt.Sprintf("artifact %q → %s", raw.Title, raw.FrameURL)
	}
	return []StreamItem{{
		Type:      TypeArtifactLink,
		SessionID: raw.SessionID,
		Timestamp: timestamp,
		Content:   content,
	}}
}

// parseCostState surfaces type="cost-state" lines as a session-cost marker:
// "$13.75 · +1003/-168 lines · opus-5 sonnet-5 haiku-4-5". Claude Code
// writes the line at session end without a timestamp, so the event time is
// reconstructed from startTime + totalDuration when both are present.
func parseCostState(raw RawMessage, fallback time.Time) []StreamItem {
	if raw.TotalCostUSD == 0 && len(raw.ModelUsage) == 0 {
		return nil
	}
	timestamp := fallback
	if raw.StartTime > 0 && raw.TotalDuration > 0 {
		timestamp = time.UnixMilli(raw.StartTime + raw.TotalDuration)
	}
	parts := []string{fmt.Sprintf("$%.2f", raw.TotalCostUSD)}
	if raw.TotalLinesAdded > 0 || raw.TotalLinesRemoved > 0 {
		parts = append(parts, fmt.Sprintf("+%d/-%d lines", raw.TotalLinesAdded, raw.TotalLinesRemoved))
	}
	if names := costStateModels(raw.ModelUsage); names != "" {
		parts = append(parts, names)
	}
	return sessionEvent(raw, timestamp, agentDisplayName(raw.AgentID), "session cost", strings.Join(parts, " · "))
}

// costStateModels lists the models in a cost-state entry, most expensive
// first, with the "claude-" prefix and any dated suffix stripped so the
// marker stays short: "opus-5 sonnet-5 haiku-4-5".
func costStateModels(usage map[string]ModelUsage) string {
	if len(usage) == 0 {
		return ""
	}
	names := make([]string, 0, len(usage))
	for name := range usage {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		ci, cj := usage[names[i]].CostUSD, usage[names[j]].CostUSD
		if ci != cj {
			return ci > cj
		}
		return names[i] < names[j]
	})
	for i, name := range names {
		names[i] = shortModelName(name)
	}
	return strings.Join(names, " ")
}

// shortModelName turns "claude-haiku-4-5-20251001" into "haiku-4-5".
func shortModelName(model string) string {
	name := strings.TrimPrefix(model, "claude-")
	if i := strings.LastIndex(name, "-"); i > 0 && len(name)-i-1 == 8 {
		if _, err := time.Parse("20060102", name[i+1:]); err == nil {
			name = name[:i]
		}
	}
	return name
}

// parseSessionTitle emits a TypeSessionTitle item carrying a human-readable
// label for the session. Both type="agent-name" (Claude's auto-generated
// title) and type="custom-title" (user-set) map to this.
func parseSessionTitle(raw RawMessage, timestamp time.Time, title string) []StreamItem {
	if title == "" {
		return nil
	}
	return []StreamItem{{
		Type:      TypeSessionTitle,
		SessionID: raw.SessionID,
		Timestamp: timestamp,
		Content:   title,
	}}
}

// parseSystemMessage handles system-type JSONL lines. Surfaces:
//   - subtype=turn_duration → TypeTurnMarker (turn ended + duration)
//   - subtype=compact_boundary → TypeCompactMarker (auto/manual compaction with preTokens)
//   - subtype=api_error → TypeAPIError (failed API request + retry progress)
//   - subtype=away_summary → TypeSessionEvent (while-you-were-away recap)
//   - subtype=local_command → TypeSessionEvent (slash command invoked)
//   - subtype=informational → TypeSessionEvent (transient notice, e.g. backgrounding)
//   - subtype=agents_killed → TypeSessionEvent (subagents terminated)
//
// Other subtypes are intentionally dropped.
func parseSystemMessage(raw RawMessage, timestamp time.Time) []StreamItem {
	agentName := agentDisplayName(raw.AgentID)

	switch raw.Subtype {
	case "api_error":
		return []StreamItem{{
			Type:      TypeAPIError,
			SessionID: raw.SessionID,
			AgentID:   raw.AgentID,
			AgentName: agentName,
			Timestamp: timestamp,
			Content:   formatAPIError(raw),
		}}
	case "away_summary":
		if raw.Content != "" {
			return sessionEvent(raw, timestamp, agentName, "recap", raw.Content)
		}
		return nil
	case "turn_duration":
		return []StreamItem{{
			Type:       TypeTurnMarker,
			SessionID:  raw.SessionID,
			AgentID:    raw.AgentID,
			AgentName:  agentName,
			Timestamp:  timestamp,
			DurationMs: raw.DurationMs,
		}}
	case "compact_boundary":
		content := formatCompactSummary(raw.CompactMetadata)
		return []StreamItem{{
			Type:      TypeCompactMarker,
			SessionID: raw.SessionID,
			AgentID:   raw.AgentID,
			AgentName: agentName,
			Timestamp: timestamp,
			Content:   content,
		}}
	case "local_command":
		if detail := localCommandDetail(raw.Content); detail != "" {
			return sessionEvent(raw, timestamp, agentName, "command", detail)
		}
		return nil
	case "informational":
		if raw.Content != "" {
			return sessionEvent(raw, timestamp, agentName, informationalLabel(raw.Level), raw.Content)
		}
		return nil
	case "agents_killed":
		return sessionEvent(raw, timestamp, agentName, "agents killed", "")
	}
	return nil
}

// localCommandDetail renders a system.local_command body into "/name args".
// The body is an XML-ish blob: <command-name>/skills</command-name>
// <command-message>…</command-message><command-args>…</command-args>.
func localCommandDetail(content string) string {
	name := xmlTagValue(content, "command-name")
	if name == "" {
		return ""
	}
	if args := xmlTagValue(content, "command-args"); args != "" {
		return name + " " + args
	}
	return name
}

// informationalLabel maps a system.informational level to a stream label.
// Unknown or absent levels read simply "note".
func informationalLabel(level string) string {
	if strings.EqualFold(level, "warning") {
		return "warning"
	}
	return "note"
}

// xmlTagValue extracts the trimmed text between <tag> and </tag>. Returns ""
// when the tag is absent or empty.
func xmlTagValue(content, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	start := strings.Index(content, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(content[start:], close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}

// formatAPIError renders a system.api_error line into a short label like
// "529 Overloaded, retry 1/10". Falls back to the raw error message (or a
// bare status code) when the pre-formatted summary is absent.
func formatAPIError(raw RawMessage) string {
	var parts []string
	if e := raw.APIError; e != nil {
		switch {
		case e.Formatted != "":
			parts = append(parts, e.Formatted)
		case e.Message != "":
			parts = append(parts, e.Message)
		case e.Status != 0:
			parts = append(parts, fmt.Sprintf("status %d", e.Status))
		}
	}
	if raw.RetryAttempt > 0 && raw.MaxRetries > 0 {
		parts = append(parts, fmt.Sprintf("retry %d/%d", raw.RetryAttempt, raw.MaxRetries))
	}
	return strings.Join(parts, ", ")
}

// formatCompactSummary renders compaction metadata into a short label like
// "auto, 179k pre-tokens". Returns "" when no metadata is present.
func formatCompactSummary(m *CompactMetadata) string {
	if m == nil {
		return ""
	}
	var parts []string
	if m.Trigger != "" {
		parts = append(parts, m.Trigger)
	}
	if m.PreTokens > 0 {
		parts = append(parts, fmt.Sprintf("%s pre-tokens", formatTokenCount(m.PreTokens)))
	}
	return strings.Join(parts, ", ")
}

// formatTokenCount renders a token count as 1.2k / 179k / 2.3M.
func formatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ContextWindowFor returns the max context window in tokens for a given
// Claude model identifier. Defaults to 200k for unknown models (the safe
// minimum across the lineup). Update this table when new models ship.
//
// Matched by prefix so dated suffixes like "-20251001" or future point
// releases of the same family resolve correctly.
func ContextWindowFor(model string) int64 {
	switch {
	case strings.HasPrefix(model, "claude-fable-5"),
		strings.HasPrefix(model, "claude-mythos-5"),
		strings.HasPrefix(model, "claude-opus-5"),
		strings.HasPrefix(model, "claude-sonnet-5"),
		strings.HasPrefix(model, "claude-opus-4-8"),
		strings.HasPrefix(model, "claude-opus-4-7"),
		strings.HasPrefix(model, "claude-opus-4-6"),
		strings.HasPrefix(model, "claude-sonnet-4-6"):
		return 1_000_000
	case strings.HasPrefix(model, "claude-haiku-4-5"),
		strings.HasPrefix(model, "claude-sonnet-4-5"),
		strings.HasPrefix(model, "claude-haiku-4"):
		return 200_000
	}
	return 200_000
}

func parseAssistantMessage(raw RawMessage, timestamp time.Time) []StreamItem {
	var msg AssistantMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return nil
	}

	var items []StreamItem
	agentName := agentDisplayName(raw.AgentID)

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
				ToolName:  PrettyToolName(block.Name),
				ToolID:    block.ID,
			})
		}
	}

	// Attach token usage + model to the first item only
	if len(items) > 0 && msg.Usage != nil {
		items[0].InputTokens = msg.Usage.InputTokens
		items[0].OutputTokens = msg.Usage.OutputTokens
		items[0].CacheCreationTokens = msg.Usage.CacheCreationInputTokens
		items[0].CacheReadTokens = msg.Usage.CacheReadInputTokens
	}
	if len(items) > 0 && msg.Model != "" && msg.Model != "<synthetic>" {
		items[0].Model = msg.Model
	}

	// Emit a cache-miss marker when the assistant message reports one. This
	// explains why per-agent context% can spike — e.g. ToolSearch loading a
	// new tool busts the prompt cache, forcing tens of thousands of input
	// tokens to be re-sent.
	if msg.Diagnostics != nil && msg.Diagnostics.CacheMissReason != nil {
		r := msg.Diagnostics.CacheMissReason
		detail := r.Type
		if r.CacheMissedInputTokens > 0 {
			detail = fmt.Sprintf("%s, +%s tokens", r.Type, formatTokenCount(r.CacheMissedInputTokens))
		}
		items = append(items, StreamItem{
			Type:      TypeCacheMiss,
			SessionID: raw.SessionID,
			AgentID:   raw.AgentID,
			AgentName: agentName,
			Timestamp: timestamp,
			Content:   detail,
		})
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

	// Parse toolUseResult for duration
	var durationMs int64
	if len(raw.ToolUseResult) > 0 {
		var tur RawToolUseResult
		if err := json.Unmarshal(raw.ToolUseResult, &tur); err == nil {
			durationMs = tur.DurationMs
		}
	}

	var items []StreamItem
	agentName := agentDisplayName(raw.AgentID)

	for _, result := range results {
		if result.Type == "tool_result" {
			items = append(items, StreamItem{
				Type:       TypeToolOutput,
				AgentID:    raw.AgentID,
				AgentName:  agentName,
				Timestamp:  timestamp,
				Content:    extractToolResultContent(result.Content),
				ToolID:     result.ToolUseID,
				DurationMs: durationMs,
			})
		}
	}

	return items
}

// extractToolResultContent handles both string and array-of-blocks content.
// Built-in tools return a plain string; MCP tools return [{"type":"text","text":"..."}].
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as plain string first (built-in tools)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try as array of content blocks (MCP tools)
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// Fallback: return raw JSON
	return string(raw)
}

func formatToolInput(toolName string, inputRaw json.RawMessage) string {
	var input ToolInput
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		// Return raw JSON if we can't parse the input
		return string(inputRaw)
	}

	switch toolName {
	case "Bash", "Monitor":
		// Monitor shares Bash's shape: a shell command plus a description.
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
	case "Task", "Agent":
		// "Task" is the legacy name; "Agent" is current (Claude Code 2.x).
		if input.Description != "" {
			return input.Description
		}
		return input.Prompt
	case "Skill":
		if input.Args != "" {
			return fmt.Sprintf("%s — %s", input.Skill, input.Args)
		}
		return input.Skill
	case "ToolSearch":
		return input.Query
	case "ScheduleWakeup":
		if input.Reason != "" {
			return input.Reason
		}
		if input.DelaySeconds > 0 {
			return fmt.Sprintf("delay %ds", input.DelaySeconds)
		}
		return string(inputRaw)
	case "TaskCreate":
		return input.Subject
	case "TaskUpdate":
		if input.TaskID != "" {
			return fmt.Sprintf("task %s", input.TaskID)
		}
		return string(inputRaw)
	case "TaskStop":
		return input.TaskIDSnake
	case "EnterPlanMode":
		return "(enter plan mode)"
	case "ExitPlanMode":
		return "(exit plan mode)"
	case "CronCreate":
		if input.Cron != "" && input.Prompt != "" {
			return fmt.Sprintf("%s: %s", input.Cron, input.Prompt)
		}
		return string(inputRaw)
	case "SendUserFile":
		names := make([]string, 0, len(input.Files))
		for _, f := range input.Files {
			names = append(names, filepath.Base(f))
		}
		files := strings.Join(names, ", ")
		switch {
		case files != "" && input.Caption != "":
			return fmt.Sprintf("%s\n  # %s", files, input.Caption)
		case files != "":
			return files
		case input.Caption != "":
			return input.Caption
		}
		return string(inputRaw)
	case "AskUserQuestion":
		questions := make([]string, 0, len(input.Questions))
		for _, q := range input.Questions {
			if q.Question != "" {
				questions = append(questions, q.Question)
			}
		}
		if len(questions) > 0 {
			return strings.Join(questions, "\n")
		}
		return string(inputRaw)
	case "SendMessage":
		if input.To != "" {
			return fmt.Sprintf("→ %s: %s", input.To, input.Message)
		}
		if input.Message != "" {
			return input.Message
		}
		return string(inputRaw)
	case "ListAgents":
		return "(list agents)"
	case "Artifact":
		action := input.Action
		if action == "" {
			action = "publish"
		}
		switch {
		case input.FilePath != "" && input.URL != "":
			return fmt.Sprintf("%s %s → %s", action, input.FilePath, input.URL)
		case input.FilePath != "":
			return fmt.Sprintf("%s %s", action, input.FilePath)
		case input.URL != "":
			return fmt.Sprintf("%s %s", action, input.URL)
		}
		return action
	default:
		return string(inputRaw)
	}
}

// PrettyToolName returns a display-friendly version of a tool name.
// Long MCP names like mcp__plugin_context7_context7__query-docs are shortened
// to mcp:query-docs; other names are returned unchanged.
func PrettyToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	idx := strings.LastIndex(name, "__")
	if idx <= len("mcp__")-2 || idx == len(name)-2 {
		return name
	}
	return "mcp:" + name[idx+2:]
}
