package tui

import (
	tea "charm.land/bubbletea/v2"
)

// placeholderModel is a generic stub for screens not yet implemented.
// It renders a message and returns to the main menu on Esc or q.
type placeholderModel struct{ text string }

func newPlaceholderModel(text string) placeholderModel { return placeholderModel{text: text} }

func (m placeholderModel) Init() tea.Cmd { return nil }

func (m placeholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "esc", "q":
			return m, func() tea.Msg { return navigateMsg{screen: ScreenMainMenu} }
		}
	}
	return m, nil
}

func (m placeholderModel) View() tea.View {
	return tea.NewView(m.text)
}
