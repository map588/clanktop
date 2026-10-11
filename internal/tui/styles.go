package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var noColor = os.Getenv("NO_COLOR") != ""

func color(c string) lipgloss.TerminalColor {
	if noColor {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color(c)
}

var (
	// Panel borders
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(color("240"))

	focusedPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(color("62"))

	// Title style
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(color("255"))

	// Role colors
	orchestratorStyle = lipgloss.NewStyle().Bold(true).Foreground(color("255")) // bold white
	subAgentStyle     = lipgloss.NewStyle().Foreground(color("6"))              // cyan
	toolStyle         = lipgloss.NewStyle().Foreground(color("2"))              // green
	mcpStyle          = lipgloss.NewStyle().Foreground(color("3"))              // yellow

	// Status colors
	runningStyle = lipgloss.NewStyle().Foreground(color("2"))   // green
	busyStyle    = lipgloss.NewStyle().Foreground(color("11"))  // bright yellow: a tool call runs now
	dimStyle     = lipgloss.NewStyle().Foreground(color("245")) // gray
	errStyle     = lipgloss.NewStyle().Foreground(color("1"))   // red

	// Alert
	alertStyle = lipgloss.NewStyle().Bold(true).Foreground(color("3"))

	// Cursor in the focused panel, and the selected agent when its panel has no focus
	cursorStyle   = lipgloss.NewStyle().Background(color("237")).Foreground(color("255"))
	selectedStyle = lipgloss.NewStyle().Background(color("235"))

	// Header bar
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(color("255")).Background(color("62")).Padding(0, 1)

	// Help
	helpKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(color("6"))
	helpDescStyle = lipgloss.NewStyle().Foreground(color("245"))
)

// SetNoColor turns colors off. The styles above are made when the package
// loads, before the flag is parsed, so the color profile is changed instead.
func SetNoColor(v bool) {
	if v {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}
