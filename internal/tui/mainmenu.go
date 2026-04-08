package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// menuItem describes one entry in the main menu.
type menuItem struct {
	key    string
	label  string
	screen Screen
}

var mainMenuItems = []menuItem{
	{"O", "OS Tools", ScreenOSTools},
	{"C", "Cloud Tools", ScreenCloudTools},
	{"S", "Script Library", ScreenScriptLibrary},
	{"H", "Job History", ScreenJobHistory},
	{"X", "Changes", ScreenChanges},
}

// mainMenuModel is the full main menu screen.
type mainMenuModel struct {
	root    *Model
	cursor  int
}

func newMainMenuModel(root *Model) mainMenuModel {
	return mainMenuModel{root: root}
}

func (m mainMenuModel) Init() tea.Cmd { return nil }

func (m mainMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(mainMenuItems)-1 {
				m.cursor++
			}
		case "enter", " ":
			return m, func() tea.Msg {
				return navigateMsg{screen: mainMenuItems[m.cursor].screen}
			}
		case "a", "A":
			return m, func() tea.Msg {
				return showCredentialModalMsg{returnTo: ScreenMainMenu}
			}
		case "q", "Q":
			return m, tea.Quit
		default:
			// Direct key navigation (O, C, S, H, X)
			key := strings.ToUpper(msg.String())
			for _, item := range mainMenuItems {
				if item.key == key {
					return m, func() tea.Msg {
						return navigateMsg{screen: item.screen}
					}
				}
			}
		}
	}
	return m, nil
}

func (m mainMenuModel) View() tea.View {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("╔═ GOrg CloudTools ══════════════════════════════════╗"))
	sb.WriteString("\n")

	for i, item := range mainMenuItems {
		line := fmt.Sprintf("  [%s] %-30s", item.key, item.label)
		if i == m.cursor {
			sb.WriteString(selectedStyle.Render("› "+line))
		} else {
			sb.WriteString("  " + line)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString("  " + m.root.statusBar())
	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("  [↑↓/jk] Navigate   [Enter] Select   [A] Credentials   [Q] Quit"))
	sb.WriteString("\n")

	return tea.NewView(sb.String())
}
