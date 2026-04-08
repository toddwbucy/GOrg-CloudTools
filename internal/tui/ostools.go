package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// osToolsLoadedMsg carries the DB query result for OS tools.
type osToolsLoadedMsg struct {
	tools []models.Tool
	err   error
}

// osToolsModel displays all Tool records with scope="os", grouped by platform.
type osToolsModel struct {
	root   *Model
	tools  []models.Tool
	cursor int
	loaded bool
	err    string
}

func newOSToolsModel(root *Model) *osToolsModel {
	return &osToolsModel{root: root}
}

func (m *osToolsModel) Init() tea.Cmd {
	return m.loadCmd()
}

func (m *osToolsModel) loadCmd() tea.Cmd {
	db := m.root.db
	return func() tea.Msg {
		var tools []models.Tool
		if err := db.Where("scope = ?", models.ScopeOS).
			Order("platform, name").
			Find(&tools).Error; err != nil {
			return osToolsLoadedMsg{err: err}
		}
		return osToolsLoadedMsg{tools: tools}
	}
}

func (m *osToolsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case osToolsLoadedMsg:
		m.loaded = true
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.tools = msg.tools
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q":
			return m, func() tea.Msg { return navigateMsg{screen: ScreenMainMenu} }
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.tools)-1 {
				m.cursor++
			}
		case "enter", " ":
			if m.cursor < len(m.tools) {
				tool := m.tools[m.cursor]
				// Check credentials — OS tools need cloud creds to reach instances.
				if !m.root.hasCredentials("aws", "com") && !m.root.hasCredentials("aws", "gov") {
					toolID := tool.ID
					return m, func() tea.Msg {
						return showCredentialModalMsg{
							returnTo:      ScreenOSTools,
							pendingToolID: toolID,
						}
					}
				}
				return m, func() tea.Msg {
					return navigateMsg{screen: ScreenInstanceSelector, toolID: tool.ID}
				}
			}
		}
	}
	return m, nil
}

func (m *osToolsModel) View() tea.View {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("OS Tools"))
	sb.WriteString("\n\n")

	if !m.loaded {
		sb.WriteString(helpStyle.Render("  Loading..."))
		return tea.NewView(sb.String())
	}
	if m.err != "" {
		sb.WriteString(errorStyle.Render("  Error: " + m.err))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}
	if len(m.tools) == 0 {
		sb.WriteString(dimStyle.Render("  No OS tools found. Add tools via the Script Library."))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}

	platform := ""
	for i, t := range m.tools {
		if t.Platform != platform {
			platform = t.Platform
			label := platform
			if label == "" {
				label = "Unknown"
			} else {
				label = strings.ToUpper(label[:1]) + label[1:]
			}
			sb.WriteString("\n  " + titleStyle.Render(label) + "\n")
			sb.WriteString("  " + strings.Repeat("─", 40) + "\n")
		}
		desc := t.Description
		if desc == "" {
			desc = t.ToolType
		}
		line := fmt.Sprintf("  %-26s  %s", t.Name, dimStyle.Render(desc))
		if i == m.cursor {
			sb.WriteString(selectedStyle.Render("› " + line))
		} else {
			sb.WriteString("  " + line)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString("  " + m.root.statusBar())
	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("  [↑↓/jk] Navigate   [Enter] Select Instances   [Esc] Back"))
	sb.WriteString("\n")

	return tea.NewView(sb.String())
}
