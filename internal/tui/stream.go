package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mattn/go-runewidth"
	"github.com/phiat/claude-esp/internal/parser"
)

const (
	// MaxStreamItems is the maximum number of items to keep in the stream
	MaxStreamItems = 1000
	// MaxLinesPerItem is the maximum lines to display per stream item
	MaxLinesPerItem = 50
)

// StreamView displays the stacked stream of items
type StreamView struct {
	viewport    viewport.Model
	items       []parser.StreamItem
	seenToolIDs map[string]bool // dedupe tool input/output by ToolID
	width       int
	height      int
	autoScroll  bool
	maxLines    int // max lines per item

	// Filters
	showThinking   bool
	showToolInput  bool
	showToolOutput bool
	showText       bool

	// Session/Agent filter (from tree)
	enabledFilters []EnabledFilter
}

// NewStreamView creates a new stream view
func NewStreamView() *StreamView {
	vp := viewport.New(80, 20)
	return &StreamView{
		viewport:       vp,
		items:          make([]parser.StreamItem, 0),
		seenToolIDs:    make(map[string]bool),
		autoScroll:     true,
		maxLines:       MaxLinesPerItem,
		showThinking:   true,
		showToolInput:  true,
		showToolOutput: true,
		showText:       true,
		enabledFilters: []EnabledFilter{},
	}
}

// SetSize updates dimensions.
//
// `width` is the OUTER width of the bordered pane the stream is rendered
// into. streamBorderStyle uses `Border()` (2 cols) + `Padding(0, 1)`
// (2 cols) = 4 cols of horizontal chrome. Vertical is border-only
// (Padding(0, 1) is vertical=0, horizontal=1), so the viewport height is
// `height - 2` for top + bottom border.
//
// Previously this used `width - 2`, which left the viewport 2 cols wider
// than the actual content area and pushed every wrapped line past the
// right border. That worked OK for ASCII but interacted with the tree
// pane's over-wide padding to push the whole TUI past its viewport.
func (s *StreamView) SetSize(width, height int) {
	s.width = width
	s.height = height
	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	innerHeight := height - 2
	if innerHeight < 1 {
		innerHeight = 1
	}
	s.viewport.Width = innerWidth
	s.viewport.Height = innerHeight
	s.updateContent()
}

// AddItem adds a new item to the stream
func (s *StreamView) AddItem(item parser.StreamItem) {
	// Deduplicate by (ToolID, Type) so tool input and output
	// with the same tool_id are both kept
	if item.ToolID != "" {
		dedupKey := fmt.Sprintf("%s:%s", item.ToolID, item.Type)
		if s.seenToolIDs[dedupKey] {
			return // Skip duplicate
		}
		s.seenToolIDs[dedupKey] = true
	}

	s.items = append(s.items, item)
	// Keep last MaxStreamItems items to prevent memory issues
	if len(s.items) > MaxStreamItems {
		s.items = s.items[len(s.items)-MaxStreamItems:]
	}
	s.updateContent()
}

// SetEnabledFilters updates which session/agent combos are visible
func (s *StreamView) SetEnabledFilters(filters []EnabledFilter) {
	s.enabledFilters = filters
	s.updateContent()
}

// ToggleThinking toggles thinking visibility
func (s *StreamView) ToggleThinking() {
	s.showThinking = !s.showThinking
	s.updateContent()
}

// ToggleToolInput toggles tool input visibility
func (s *StreamView) ToggleToolInput() {
	s.showToolInput = !s.showToolInput
	s.updateContent()
}

// ToggleToolOutput toggles tool output visibility
func (s *StreamView) ToggleToolOutput() {
	s.showToolOutput = !s.showToolOutput
	s.updateContent()
}

// ToggleText toggles text visibility
func (s *StreamView) ToggleText() {
	s.showText = !s.showText
	s.updateContent()
}

// ToggleAutoScroll toggles auto-scroll
func (s *StreamView) ToggleAutoScroll() {
	s.autoScroll = !s.autoScroll
}

// ScrollUp scrolls the viewport up
func (s *StreamView) ScrollUp(lines int) {
	s.autoScroll = false
	s.viewport.ScrollUp(lines)
}

// ScrollDown scrolls the viewport down
func (s *StreamView) ScrollDown(lines int) {
	s.viewport.ScrollDown(lines)
}

// IsThinkingEnabled returns thinking filter state
func (s *StreamView) IsThinkingEnabled() bool {
	return s.showThinking
}

// IsToolInputEnabled returns tool input filter state
func (s *StreamView) IsToolInputEnabled() bool {
	return s.showToolInput
}

// IsToolOutputEnabled returns tool output filter state
func (s *StreamView) IsToolOutputEnabled() bool {
	return s.showToolOutput
}

// IsTextEnabled returns text filter state
func (s *StreamView) IsTextEnabled() bool {
	return s.showText
}

// IsAutoScrollEnabled returns auto-scroll state
func (s *StreamView) IsAutoScrollEnabled() bool {
	return s.autoScroll
}

func (s *StreamView) updateContent() {
	var b strings.Builder
	contentWidth := s.width - 4 // account for borders and padding

	for _, item := range s.items {
		// Check session/agent filter
		if !s.isItemEnabled(item) {
			continue
		}

		// Check type filter
		switch item.Type {
		case parser.TypeThinking:
			if !s.showThinking {
				continue
			}
		case parser.TypeToolInput:
			if !s.showToolInput {
				continue
			}
		case parser.TypeToolOutput:
			if !s.showToolOutput {
				continue
			}
		case parser.TypeText:
			if !s.showText {
				continue
			}
		}

		b.WriteString(s.renderItem(item, contentWidth))
		b.WriteString("\n")
	}

	s.viewport.SetContent(b.String())
	if s.autoScroll {
		s.viewport.GotoBottom()
	}
}

func (s *StreamView) isItemEnabled(item parser.StreamItem) bool {
	for _, f := range s.enabledFilters {
		if f.SessionID == item.SessionID && f.AgentID == item.AgentID {
			return true
		}
	}
	return false
}

func (s *StreamView) renderItem(item parser.StreamItem, width int) string {
	// Turn markers are a standalone single-line divider — no agent header,
	// no trailing separator. Return early so the universal separator tail
	// below doesn't double up.
	if item.Type == parser.TypeTurnMarker {
		dur := formatDuration(item.DurationMs)
		text := fmt.Sprintf("── turn ended %s ──", dur)
		return mutedStyle.Render(text)
	}
	if item.Type == parser.TypeCompactMarker {
		text := "── compacted ──"
		if item.Content != "" {
			text = fmt.Sprintf("── compacted (%s) ──", item.Content)
		}
		return mutedStyle.Render(text)
	}
	if item.Type == parser.TypePRLink {
		return mutedStyle.Render(fmt.Sprintf("── %s ──", item.Content))
	}

	var b strings.Builder

	// Agent name styling
	agentStyle := mainAgentStyle
	if item.AgentID != "" {
		agentStyle = subAgentStyle
	}
	agentName := agentStyle.Render(item.AgentName)

	// Separator
	sep := separatorStyle.Render(" » ")

	switch item.Type {
	case parser.TypeThinking:
		header := thinkingStyle.Render(thinkingIcon + " Thinking")
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		content := s.truncateContent(item.Content, width)
		b.WriteString(thinkingContentStyle.Render(content))

	case parser.TypeToolInput:
		toolName := toolInputStyle.Render(toolInputIcon + " " + item.ToolName)
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, toolName))
		content := s.truncateContent(item.Content, width)
		b.WriteString(toolInputContentStyle.Render(content))

	case parser.TypeToolOutput:
		// Look up tool name from matching ToolInput
		toolName := ""
		if item.ToolID != "" {
			for _, other := range s.items {
				if other.Type == parser.TypeToolInput && other.ToolID == item.ToolID {
					toolName = other.ToolName
					break
				}
			}
		}
		var outputLabel string
		if toolName != "" {
			outputLabel = toolOutputIcon + " " + toolName + " result"
		} else {
			outputLabel = toolOutputIcon + " Output"
		}
		if item.DurationMs > 0 {
			outputLabel += " " + formatDuration(item.DurationMs)
		}
		header := toolOutputStyle.Render(outputLabel)
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		content := s.truncateContent(item.Content, width)
		b.WriteString(toolOutputContentStyle.Render(content))

	case parser.TypeText:
		header := textStyle.Render(textIcon + " Response")
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		content := s.truncateContent(item.Content, width)
		b.WriteString(content)

	case parser.TypeHookOutput:
		label := hookIcon + " Hook"
		if item.ToolName != "" {
			label += " " + item.ToolName
		}
		if item.DurationMs > 0 {
			label += " " + formatDuration(item.DurationMs)
		}
		header := hookStyle.Render(label)
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		if item.Content != "" {
			content := s.truncateContent(item.Content, width)
			b.WriteString(hookContentStyle.Render(content))
		}

	case parser.TypeDiagnostics:
		label := diagnosticsIcon + " Diagnostics"
		if item.ToolName != "" {
			label += " " + item.ToolName
		}
		header := diagnosticsStyle.Render(label)
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		if item.Content != "" {
			content := s.truncateContent(item.Content, width)
			b.WriteString(diagnosticsContentStyle.Render(content))
		}

	case parser.TypeDebug:
		label := debugIcon + " Debug"
		if item.ToolName != "" {
			label += " " + item.ToolName
		}
		header := debugStyle.Render(label)
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		if item.Content != "" {
			content := s.truncateContent(item.Content, width)
			b.WriteString(debugContentStyle.Render(content))
		}
	}

	// Add separator line
	b.WriteString("\n" + separatorStyle.Render(strings.Repeat("─", min(width, 60))))

	return b.String()
}

func (s *StreamView) truncateContent(content string, width int) string {
	lines := strings.Split(content, "\n")

	// Truncate number of lines
	if len(lines) > s.maxLines {
		remaining := len(lines) - s.maxLines
		lines = lines[:s.maxLines]
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("... (%d more lines)", remaining)))
	}

	// Word wrap each line using display width (handles CJK/emoji correctly)
	var wrapped []string
	for _, line := range lines {
		lineWidth := runewidth.StringWidth(line)
		if lineWidth > width && width > 0 {
			for runewidth.StringWidth(line) > width {
				col := 0
				splitAt := 0
				for i, r := range line {
					cw := runewidth.RuneWidth(r)
					if col+cw > width {
						break
					}
					col += cw
					splitAt = i + len(string(r))
				}
				if splitAt == 0 {
					// Single char wider than width — force advance past first rune
					_, size := utf8.DecodeRuneInString(line)
					splitAt = size
				}
				wrapped = append(wrapped, line[:splitAt])
				line = line[splitAt:]
			}
			if len(line) > 0 {
				wrapped = append(wrapped, line)
			}
		} else {
			wrapped = append(wrapped, line)
		}
	}

	return strings.Join(wrapped, "\n")
}

// formatDuration formats a duration in milliseconds to a human-readable string
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("(%dms)", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("(%.1fs)", secs)
	}
	mins := secs / 60.0
	return fmt.Sprintf("(%.1fm)", mins)
}

// View renders the stream
func (s *StreamView) View() string {
	return s.viewport.View()
}
