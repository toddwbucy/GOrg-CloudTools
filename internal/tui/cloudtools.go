package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// cloudToolsLoadedMsg carries the DB query result for cloud tools.
type cloudToolsLoadedMsg struct {
	tools []models.Tool
	err   error
}

// cloudToolsModel displays all Tool records with scope="cloud", grouped by
// provider (stored in the Platform field for cloud tools: "aws", "azure", "gcp").
// Tools whose provider credentials are not loaded are shown but dimmed.
type cloudToolsModel struct {
	root   *Model
	tools  []models.Tool
	cursor int
	loaded bool
	err    string
}

func newCloudToolsModel(root *Model) *cloudToolsModel {
	return &cloudToolsModel{root: root}
}

func (m *cloudToolsModel) Init() tea.Cmd {
	return m.loadCmd()
}

func (m *cloudToolsModel) loadCmd() tea.Cmd {
	db := m.root.db
	return func() tea.Msg {
		var tools []models.Tool
		if err := db.Where("scope = ?", models.ScopeCloud).
			Order("platform, name").
			Find(&tools).Error; err != nil {
			return cloudToolsLoadedMsg{err: err}
		}
		return cloudToolsLoadedMsg{tools: tools}
	}
}

func (m *cloudToolsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cloudToolsLoadedMsg:
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
				provider, env, err := cloudProvider(tool.Platform)
				if err != nil {
					m.err = err.Error()
					return m, nil
				}
				if !m.root.hasCredentials(provider, env) {
					return m, func() tea.Msg {
						return showCredentialModalMsg{returnTo: ScreenCloudTools}
					}
				}
				return m, func() tea.Msg {
					return navigateMsg{screen: ScreenExecution, toolID: tool.ID}
				}
			}
		}
	}
	return m, nil
}

func (m *cloudToolsModel) View() tea.View {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("Cloud Tools"))
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
		sb.WriteString(dimStyle.Render("  No cloud tools found. Add tools via the Script Library."))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}

	provider := ""
	for i, t := range m.tools {
		p, env, _ := cloudProvider(t.Platform)
		if t.Platform != provider {
			provider = t.Platform
			label := strings.ToUpper(t.Platform)
			if label == "" {
				label = "UNKNOWN"
			}
			sb.WriteString("\n  " + titleStyle.Render(label) + "\n")
			sb.WriteString("  " + strings.Repeat("─", 40) + "\n")
		}
		hasCredentials := m.root.hasCredentials(p, env)
		desc := t.Description
		if desc == "" {
			desc = t.ToolType
		}
		line := fmt.Sprintf("  %-26s  %s", t.Name, dimStyle.Render(desc))
		if !hasCredentials {
			line = fmt.Sprintf("  %-26s  %s", t.Name, dimStyle.Render("(no credentials) "+desc))
		}
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
	sb.WriteString(helpStyle.Render("  [↑↓/jk] Navigate   [Enter] Run Tool   [Esc] Back"))
	sb.WriteString("\n")

	return tea.NewView(sb.String())
}

// cloudProvider maps a Tool.Platform value to the provider/env pair used by
// hasCredentials. Returns an error for unsupported platforms so callers do not
// silently fall back to the wrong cloud credentials.
func cloudProvider(platform string) (provider, env string, err error) {
	switch platform {
	case "aws", "":
		return "aws", "com", nil
	case "aws-gov":
		return "aws", "gov", nil
	default:
		return "", "", fmt.Errorf("unsupported cloud platform %q", platform)
	}
}
