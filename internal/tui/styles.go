package tui

import (
	lipgloss "charm.land/lipgloss/v2"
)

// Shared lipgloss styles used across all screens.
var (
	// titleStyle is used for screen headers and box titles.
	titleStyle = lipgloss.NewStyle().Bold(true)

	// dimStyle renders text for unavailable / placeholder items.
	dimStyle = lipgloss.NewStyle().Faint(true)

	// selectedStyle highlights the currently focused list item.
	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("2")) // green

	// errorStyle renders inline error text.
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red

	// statusActiveStyle marks an authenticated cloud environment.
	statusActiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)

	// statusInactiveStyle marks an unauthenticated cloud environment.
	statusInactiveStyle = lipgloss.NewStyle().Faint(true)

	// helpStyle renders the keybinding hint line at the bottom of screens.
	helpStyle = lipgloss.NewStyle().Faint(true)

	// boxStyle draws the outer border used by most screens.
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8")) // grey

	// headerStyle pads content inside the box.
	headerStyle = lipgloss.NewStyle().Padding(0, 1)
)
