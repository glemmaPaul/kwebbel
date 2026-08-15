package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenIdle screen = iota
	screenCalling
)

const (
	menuConnect = iota
	menuAddressBook
	menuProfile
	menuCount
)

type idleFocus int

const (
	idleFocusPeerID idleFocus = iota
	idleFocusConnect
)

type model struct {
	screen  screen
	width   int
	height  int
	status  string
	signals Signals
	idle    idleModel
	calling callingModel
}

type idleModel struct {
	menuIndex int
	focus     idleFocus
	peerID    textinput.Model
}

type callingModel struct {
	callers []Caller
	muted   bool
}

func newModel(signals Signals) model {
	ti := textinput.New()
	ti.Placeholder = "Peer ID"
	ti.CharLimit = 128
	ti.Width = 36
	ti.Prompt = "> "
	ti.PromptStyle = stylePrompt
	ti.TextStyle = styleInput
	ti.PlaceholderStyle = stylePlaceholder
	ti.Cursor.Style = styleInput

	return model{
		screen:  screenIdle,
		signals: signals,
		idle: idleModel{
			menuIndex: menuConnect,
			focus:     idleFocusPeerID,
			peerID:    ti,
		},
	}
}

func (m model) Init() tea.Cmd {
	return m.idle.peerID.Focus()
}

func (m *idleModel) syncFocus() tea.Cmd {
	if m.menuIndex == menuConnect && m.focus == idleFocusPeerID {
		return m.peerID.Focus()
	}
	m.peerID.Blur()
	return nil
}
