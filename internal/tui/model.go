// Package tui implements the terminal user interface for GOrg CloudTools.
// It is built on charm.land/bubbletea/v2 (Elm architecture: Model/Update/View).
//
// The root Model delegates to a stack of screen models. All cloud operations
// (credential validation, instance listing, script execution) happen via
// tea.Cmd so they never block the event loop.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/toddwbucy/GOrg-CloudTools/internal/config"
	"gorm.io/gorm"
)

// Screen identifies which screen is currently rendered.
type Screen int

const (
	ScreenMainMenu Screen = iota
	ScreenCredentialInput
	ScreenOSTools
	ScreenCloudTools
	ScreenInstanceSelector
	ScreenExecution
	ScreenOutputViewer
	ScreenJobHistory
	ScreenScriptLibrary
	ScreenChanges
)

// ── Inter-screen message types ────────────────────────────────────────────────
// Child screens communicate back to the root model only by returning these
// message types from tea.Cmd. The root model type-switches on them in Update.

// navigateMsg asks the root model to switch to a different screen.
type navigateMsg struct {
	screen      Screen
	batchID     uint     // non-zero when navigating to ScreenExecution
	toolID      uint     // non-zero when navigating to ScreenInstanceSelector or ScreenExecution
	instanceIDs []string // non-nil when navigating to ScreenExecution from InstanceSelector
}

// credentialsLoadedMsg is sent after a successful credential validation.
// The root model stores the CloudEnv in its map and returns to the previous screen.
// pendingToolID mirrors showCredentialModalMsg.pendingToolID: when non-zero the
// root model navigates to ScreenInstanceSelector instead of returnTo.
type credentialsLoadedMsg struct {
	provider      string // e.g. "aws"
	env           string // e.g. "com" or "gov"
	ce            *CloudEnv
	returnTo      Screen
	pendingToolID uint
}

// showCredentialModalMsg asks the root model to open the credential input
// screen. returnTo is the screen to return to after successful credential entry.
// pendingToolID, when non-zero, is forwarded so that after credentials are
// accepted the user lands directly on ScreenInstanceSelector for that tool.
type showCredentialModalMsg struct {
	returnTo      Screen
	pendingToolID uint
}

// Model is the root application model. It holds all top-level state and
// delegates rendering and input handling to the currently active child model.
type Model struct {
	cfg       *config.Config
	db        *gorm.DB
	screen    Screen
	active    tea.Model
	cloudEnvs map[string]*CloudEnv // keyed by envKey("aws","com") etc.
	width     int
	height    int
}

// New creates the root Model. The TUI starts with no credentials loaded and
// shows the main menu immediately — no blocking startup checks.
func New(cfg *config.Config, db *gorm.DB) Model {
	m := Model{
		cfg:       cfg,
		db:        db,
		screen:    ScreenMainMenu,
		cloudEnvs: make(map[string]*CloudEnv),
	}
	m.active = newMainMenuModel(&m)
	return m
}

// Init satisfies tea.Model. Initialises the active child screen.
func (m Model) Init() tea.Cmd {
	if m.active == nil {
		return nil
	}
	return m.active.Init()
}

// Update handles global messages (window resize, quit, navigation, credential
// storage) then delegates everything else to the active child screen.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case navigateMsg:
		m.screen = msg.screen
		m.active = m.screenModel(msg)
		return m, m.active.Init()

	case showCredentialModalMsg:
		m.screen = ScreenCredentialInput
		m.active = newCredentialInputModel(&m, msg.returnTo, msg.pendingToolID)
		return m, m.active.Init()

	case credentialsLoadedMsg:
		m.cloudEnvs[envKey(msg.provider, msg.env)] = msg.ce
		// If there is a pending tool, jump straight to the instance selector.
		// Otherwise return to whichever screen triggered the credential prompt.
		nav := navigateMsg{screen: msg.returnTo}
		if msg.pendingToolID != 0 {
			nav = navigateMsg{screen: ScreenInstanceSelector, toolID: msg.pendingToolID}
		}
		m.screen = nav.screen
		m.active = m.screenModel(nav)
		return m, m.active.Init()
	}

	if m.active == nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.active, cmd = m.active.Update(msg)
	return m, cmd
}

// View renders the active child screen in the alternate screen buffer.
// AltScreen is set on every view so the program stays in full-screen mode
// even when the active screen changes.
func (m Model) View() tea.View {
	var v tea.View
	if m.active == nil {
		v = tea.NewView("")
	} else {
		v = m.active.View()
	}
	v.AltScreen = true
	return v
}

// hasCredentials reports whether credentials are loaded for the given provider+env.
func (m *Model) hasCredentials(provider, env string) bool {
	return m.cloudEnvs[envKey(provider, env)] != nil
}

// statusBar renders the credential status line shown at the bottom of every
// screen. Format: "AWS COM: ● active   AWS GOV: ○ no credentials"
func (m *Model) statusBar() string {
	type envStatus struct{ key, label string }
	envs := []envStatus{
		{envKey("aws", "com"), "AWS COM"},
		{envKey("aws", "gov"), "AWS GOV"},
	}
	var parts []string
	for _, e := range envs {
		if m.cloudEnvs[e.key] != nil {
			parts = append(parts, statusActiveStyle.Render(e.label+": ● active"))
		} else {
			parts = append(parts, statusInactiveStyle.Render(e.label+": ○ no credentials"))
		}
	}
	return strings.Join(parts, "   ")
}

// screenModel constructs the tea.Model for the given navigation message.
func (m *Model) screenModel(nav navigateMsg) tea.Model {
	switch nav.screen {
	case ScreenMainMenu:
		return newMainMenuModel(m)
	case ScreenCredentialInput:
		return newCredentialInputModel(m, nav.screen, 0)
	case ScreenOSTools:
		return newOSToolsModel(m)
	case ScreenCloudTools:
		return newCloudToolsModel(m)
	case ScreenInstanceSelector:
		return newInstanceSelectorModel(m, nav.toolID)
	case ScreenJobHistory:
		return newJobHistoryModel(m)
	default:
		// Placeholder for screens not yet implemented.
		return newPlaceholderModel(fmt.Sprintf("Screen %d — coming soon\n\n[Esc] Back", nav.screen))
	}
}
