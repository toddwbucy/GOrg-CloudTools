package tui

import (
	tea "charm.land/bubbletea/v2"
)

// mainMenu is a placeholder for the main menu screen.
// The full implementation is in PR-2.
type mainMenu struct{}

func newMainMenu() mainMenu { return mainMenu{} }

func (m mainMenu) Init() tea.Cmd { return nil }

func (m mainMenu) Update(_ tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (m mainMenu) View() tea.View {
	return tea.NewView("GOrg CloudTools — loading...\n\nPress Ctrl+C to quit.")
}
