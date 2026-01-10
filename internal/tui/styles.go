package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	primaryColor   = lipgloss.Color("#7C3AED") // Purple
	secondaryColor = lipgloss.Color("#10B981") // Green
	warningColor   = lipgloss.Color("#F59E0B") // Yellow/Orange
	errorColor     = lipgloss.Color("#EF4444") // Red
	mutedColor     = lipgloss.Color("#6B7280") // Gray
	bgColor        = lipgloss.Color("#1F2937") // Dark gray

	// Thinking style - purple
	thinkingIcon  = "🧠"
	thinkingStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true)
	thinkingContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A78BFA"))

	// Tool input style - yellow
	toolInputIcon  = "🔧"
	toolInputStyle = lipgloss.NewStyle().
			Foreground(warningColor).
			Bold(true)
	toolInputContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FCD34D"))

	// Tool output style - green
	toolOutputIcon  = "📤"
	toolOutputStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Bold(true)
	toolOutputContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6EE7B7"))

	// Text style - white (but we probably won't show this)
	textIcon  = "💬"
	textStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F9FAFB"))

	// Agent name styles
	mainAgentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#60A5FA")).
			Bold(true)
	subAgentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F472B6")).
			Bold(true)

	// Tree styles
	treeSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#374151")).
				Foreground(lipgloss.Color("#F9FAFB")).
				Bold(true)
	treeNormalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D1D5DB"))
	treeCheckedStyle = lipgloss.NewStyle().
				Foreground(secondaryColor)
	treeUncheckedStyle = lipgloss.NewStyle().
				Foreground(mutedColor)

	// Border styles
	treeBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(mutedColor).
			Padding(0, 1)

	streamBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(mutedColor).
				Padding(0, 1)

	// Header/toggle bar
	headerBgColor = lipgloss.Color("#374151")
	headerFgColor = lipgloss.Color("#F9FAFB")

	headerStyle = lipgloss.NewStyle().
			Background(headerBgColor).
			Foreground(headerFgColor).
			Padding(0, 1)

	toggleOnStyle = lipgloss.NewStyle().
			Background(headerBgColor).
			Foreground(secondaryColor).
			Bold(true)
	toggleOffStyle = lipgloss.NewStyle().
			Background(headerBgColor).
			Foreground(mutedColor)
	headerMutedStyle = lipgloss.NewStyle().
			Background(headerBgColor).
			Foreground(mutedColor)

	// Help bar at bottom
	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	// Separator
	separatorStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	// Muted text style (for truncation messages etc)
	mutedStyle = lipgloss.NewStyle().
			Foreground(mutedColor)
)

// Helper to truncate strings
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
