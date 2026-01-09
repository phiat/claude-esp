package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/phiat/claude-esp/internal/parser"
)

// StreamView displays the stacked stream of items
type StreamView struct {
	viewport     viewport.Model
	items        []parser.StreamItem
	width        int
	height       int
	autoScroll   bool
	maxLines     int // max lines per item

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
		autoScroll:     true,
		maxLines:       50,
		showThinking:   true,
		showToolInput:  true,
		showToolOutput: true,
		showText:       false, // hide main text by default
		enabledFilters: []EnabledFilter{},
	}
}

// SetSize updates dimensions
func (s *StreamView) SetSize(width, height int) {
	s.width = width
	s.height = height
	s.viewport.Width = width - 2 // account for border
	s.viewport.Height = height - 2
	s.updateContent()
}

// AddItem adds a new item to the stream
func (s *StreamView) AddItem(item parser.StreamItem) {
	s.items = append(s.items, item)
	// Keep last 1000 items to prevent memory issues
	if len(s.items) > 1000 {
		s.items = s.items[len(s.items)-1000:]
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

// ToggleAutoScroll toggles auto-scroll
func (s *StreamView) ToggleAutoScroll() {
	s.autoScroll = !s.autoScroll
}

// ScrollUp scrolls the viewport up
func (s *StreamView) ScrollUp(lines int) {
	s.autoScroll = false
	s.viewport.LineUp(lines)
}

// ScrollDown scrolls the viewport down
func (s *StreamView) ScrollDown(lines int) {
	s.viewport.LineDown(lines)
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
		header := toolOutputStyle.Render(toolOutputIcon + " Output")
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		content := s.truncateContent(item.Content, width)
		b.WriteString(toolOutputContentStyle.Render(content))

	case parser.TypeText:
		header := textStyle.Render(textIcon + " Response")
		b.WriteString(fmt.Sprintf("%s%s%s\n", agentName, sep, header))
		content := s.truncateContent(item.Content, width)
		b.WriteString(content)
	}

	// Add separator line
	b.WriteString("\n" + separatorStyle.Render(strings.Repeat("─", min(width, 60))))

	return b.String()
}

func (s *StreamView) truncateContent(content string, width int) string {
	lines := strings.Split(content, "\n")

	// Truncate number of lines
	if len(lines) > s.maxLines {
		lines = lines[:s.maxLines]
		lines = append(lines, mutedStyle.Render(fmt.Sprintf("... (%d more lines)", len(lines)-s.maxLines)))
	}

	// Word wrap each line
	var wrapped []string
	for _, line := range lines {
		if len(line) > width && width > 0 {
			// Simple word wrap
			for len(line) > width {
				wrapped = append(wrapped, line[:width])
				line = line[width:]
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

// View renders the stream
func (s *StreamView) View() string {
	return s.viewport.View()
}

// Update handles viewport messages
func (s *StreamView) Update(msg any) {
	switch msg := msg.(type) {
	case viewport.Model:
		s.viewport = msg
	}
}
