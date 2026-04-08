package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	ec2pkg "github.com/toddwbucy/GOrg-CloudTools/internal/cloud/aws/ec2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/db/models"
)

// instancesLoadedMsg carries the async result of the DB+EC2 load.
type instancesLoadedMsg struct {
	tool      models.Tool
	instances []ec2pkg.Instance
	err       error
}

// instanceSelectorModel presents a multi-select list of running EC2 instances
// filtered by the selected tool's platform. The user selects instances then
// presses Enter to proceed to the execution screen.
type instanceSelectorModel struct {
	root      *Model
	toolID    uint
	tool      models.Tool
	instances []ec2pkg.Instance
	selected  map[int]bool
	cursor    int
	loaded    bool
	err       string
}

func newInstanceSelectorModel(root *Model, toolID uint) *instanceSelectorModel {
	return &instanceSelectorModel{
		root:     root,
		toolID:   toolID,
		selected: make(map[int]bool),
	}
}

func (m *instanceSelectorModel) Init() tea.Cmd {
	return m.loadCmd()
}

func (m *instanceSelectorModel) loadCmd() tea.Cmd {
	db := m.root.db
	toolID := m.toolID

	// Capture the active cloud env at dispatch time (not render time).
	var ce *CloudEnv
	if env := m.root.cloudEnvs[envKey("aws", "com")]; env != nil {
		ce = env
	} else if env := m.root.cloudEnvs[envKey("aws", "gov")]; env != nil {
		ce = env
	}

	return func() tea.Msg {
		var tool models.Tool
		if err := db.First(&tool, toolID).Error; err != nil {
			return instancesLoadedMsg{err: fmt.Errorf("loading tool: %w", err)}
		}
		if ce == nil {
			return instancesLoadedMsg{tool: tool, err: fmt.Errorf("no AWS credentials loaded")}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		instances, err := ec2pkg.ListRunning(ctx, ce.Cfg, ce.AccountID)
		if err != nil {
			return instancesLoadedMsg{tool: tool, err: err}
		}

		// Filter to matching platform when the tool specifies one.
		if tool.Platform != "" {
			filtered := instances[:0]
			for _, inst := range instances {
				if inst.Platform == tool.Platform {
					filtered = append(filtered, inst)
				}
			}
			instances = filtered
		}

		return instancesLoadedMsg{tool: tool, instances: instances}
	}
}

func (m *instanceSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case instancesLoadedMsg:
		m.loaded = true
		m.tool = msg.tool
		if msg.err != nil {
			m.err = msg.err.Error()
		} else {
			m.instances = msg.instances
		}

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q":
			return m, func() tea.Msg { return navigateMsg{screen: ScreenOSTools} }
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.instances)-1 {
				m.cursor++
			}
		case " ":
			if len(m.instances) > 0 {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
		case "a":
			// Select all / deselect all toggle. Count only true entries so
			// that false entries left by Space-deselect don't trigger a
			// premature deselect-all.
			trueCount := 0
			for _, v := range m.selected {
				if v {
					trueCount++
				}
			}
			if trueCount == len(m.instances) {
				m.selected = make(map[int]bool)
			} else {
				for i := range m.instances {
					m.selected[i] = true
				}
			}
		case "enter":
			ids := m.selectedIDs()
			if len(ids) == 0 {
				return m, nil // nothing selected — ignore
			}
			toolID := m.tool.ID
			return m, func() tea.Msg {
				return navigateMsg{
					screen:      ScreenExecution,
					toolID:      toolID,
					instanceIDs: ids,
				}
			}
		}
	}
	return m, nil
}

func (m *instanceSelectorModel) View() tea.View {
	var sb strings.Builder

	title := "Select Instances"
	if m.loaded && m.tool.Name != "" {
		title = fmt.Sprintf("Select Instances — %s", m.tool.Name)
	}
	sb.WriteString(titleStyle.Render(title))
	sb.WriteString("\n\n")

	if !m.loaded {
		sb.WriteString(helpStyle.Render("  Loading instances..."))
		return tea.NewView(sb.String())
	}
	if m.err != "" {
		sb.WriteString(errorStyle.Render("  Error: " + m.err))
		sb.WriteString("\n\n")
		sb.WriteString(helpStyle.Render("  [Esc] Back"))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}
	if len(m.instances) == 0 {
		sb.WriteString(dimStyle.Render("  No running instances found"))
		if m.tool.Platform != "" {
			sb.WriteString(dimStyle.Render(fmt.Sprintf(" for platform %q", m.tool.Platform)))
		}
		sb.WriteString(".\n\n")
		sb.WriteString(helpStyle.Render("  [Esc] Back"))
		sb.WriteString("\n")
		return tea.NewView(sb.String())
	}

	// Column header — AccountID uses %-12s to fit 12-digit AWS account IDs.
	sb.WriteString(fmt.Sprintf("  %-3s  %-21s  %-9s  %-12s  %s\n",
		"", "Instance ID", "Platform", "Account", "Name"))
	sb.WriteString("  " + strings.Repeat("─", 72) + "\n")

	for i, inst := range m.instances {
		check := "[ ]"
		if m.selected[i] {
			check = "[✓]"
		}
		name := inst.Name
		if name == "" {
			name = dimStyle.Render("—")
		}
		line := fmt.Sprintf("  %s  %-21s  %-9s  %-12s  %s",
			check, inst.InstanceID, inst.Platform, inst.AccountID, name)
		if i == m.cursor {
			sb.WriteString(selectedStyle.Render("›" + line[1:]))
		} else {
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}

	selCount := len(m.selectedIDs())
	sb.WriteString("\n")
	if selCount > 0 {
		sb.WriteString(fmt.Sprintf("  %s selected\n\n", statusActiveStyle.Render(fmt.Sprintf("%d instance(s)", selCount))))
	} else {
		sb.WriteString(dimStyle.Render("  No instances selected") + "\n\n")
	}
	sb.WriteString("  " + m.root.statusBar())
	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("  [↑↓/jk] Navigate   [Space] Toggle   [a] Select All   [Enter] Run   [Esc] Back"))
	sb.WriteString("\n")

	return tea.NewView(sb.String())
}

// selectedIDs returns the instance IDs for all checked rows.
func (m *instanceSelectorModel) selectedIDs() []string {
	ids := make([]string, 0, len(m.selected))
	for i := range m.instances {
		if m.selected[i] {
			ids = append(ids, m.instances[i].InstanceID)
		}
	}
	return ids
}
