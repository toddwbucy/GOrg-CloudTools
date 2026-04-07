// Package tui implements the terminal user interface for GOrg CloudTools.
// It is built on charm.land/bubbletea/v2 (Elm architecture: Model/Update/View).
//
// The root Model delegates to a stack of screen models. All cloud operations
// (credential validation, instance listing, script execution) happen via
// tea.Cmd so they never block the event loop.
package tui

import (
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

// Model is the root application model. It holds all top-level state and
// delegates rendering and input handling to the currently active child model.
type Model struct {
	cfg    *config.Config
	db     *gorm.DB
	screen Screen
	active tea.Model
	width  int
	height int
	err    error
}

// New creates the root Model. The TUI starts with no credentials loaded and
// shows the main menu immediately — no blocking startup checks.
func New(cfg *config.Config, db *gorm.DB) Model {
	return Model{
		cfg:    cfg,
		db:     db,
		screen: ScreenMainMenu,
		active: newMainMenu(),
	}
}

// Init satisfies tea.Model. It initialises the active child screen.
func (m Model) Init() tea.Cmd {
	if m.active == nil {
		return nil
	}
	return m.active.Init()
}

// Update handles global messages (window resize, quit) then delegates to the
// active child screen. The child's returned Cmd is always propagated upward.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	if m.active == nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.active, cmd = m.active.Update(msg)
	return m, cmd
}

// View renders the active child screen in the alternate screen buffer
// (full-window mode). AltScreen is set on every View so the program stays
// in full-screen even when the child screen changes.
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
