package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
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

	// Role colors — all roles fully visible
	orchestratorStyle = lipgloss.NewStyle().Bold(true).Foreground(color("255")) // bold white
	subAgentStyle     = lipgloss.NewStyle().Foreground(color("6"))              // cyan
	toolStyle         = lipgloss.NewStyle().Foreground(color("2"))              // green
	runtimeStyle      = lipgloss.NewStyle().Foreground(color("4"))              // blue
	infraStyle        = lipgloss.NewStyle().Foreground(color("245"))            // gray (system utils)
	mcpStyle          = lipgloss.NewStyle().Foreground(color("3"))              // yellow
	unknownStyle      = lipgloss.NewStyle().Foreground(color("255"))            // white

	// Status colors
	runningStyle = lipgloss.NewStyle().Foreground(color("2"))   // green
	exitOkStyle  = lipgloss.NewStyle().Foreground(color("245")) // dim
	exitErrStyle = lipgloss.NewStyle().Foreground(color("1"))   // red
	zombieStyle  = lipgloss.NewStyle().Foreground(color("3"))   // yellow

	// Alert
	alertStyle = lipgloss.NewStyle().Bold(true).Foreground(color("3")).Background(color("0"))

	// Cursor / selection
	cursorStyle = lipgloss.NewStyle().Background(color("237"))

	// Header bar
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(color("255")).Background(color("62")).Padding(0, 1)

	// Help
	helpKeyStyle  = lipgloss.NewStyle().Bold(true).Foreground(color("6"))
	helpDescStyle = lipgloss.NewStyle().Foreground(color("245"))
)

// SetNoColor allows the CLI to force no-color mode.
func SetNoColor(v bool) {
	noColor = v
}
