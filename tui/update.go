package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case enterCallMsg:
		m.screen = screenCalling
		m.calling.callers = capCallers(msg.callers)
		m.status = ""
		m.idle.peerID.Blur()
		return m, nil
	case leaveCallMsg:
		m.screen = screenIdle
		m.calling = callingModel{}
		m.status = ""
		m.idle.menuIndex = menuConnect
		m.idle.focus = idleFocusPeerID
		return m, m.idle.syncFocus()
	case setCallersMsg:
		m.calling.callers = capCallers(msg.callers)
		return m, nil
	case setMutedMsg:
		m.calling.muted = msg.muted
		for i := range m.calling.callers {
			if m.calling.callers[i].Local {
				m.calling.callers[i].Muted = msg.muted
			}
		}
		return m, nil
	case setStatusMsg:
		m.status = msg.text
		return m, nil
	}

	switch m.screen {
	case screenIdle:
		return m.updateIdle(msg)
	case screenCalling:
		return m.updateCalling(msg)
	}
	return m, nil
}

func (m model) updateIdle(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if m.idle.menuIndex == menuConnect && m.idle.focus == idleFocusPeerID {
			var cmd tea.Cmd
			m.idle.peerID, cmd = m.idle.peerID.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch key.String() {
	case "up":
		if m.idle.menuIndex == menuConnect && m.idle.focus == idleFocusConnect {
			m.idle.focus = idleFocusPeerID
			return m, m.idle.syncFocus()
		}
		m.idle.menuIndex = (m.idle.menuIndex - 1 + menuCount) % menuCount
		if m.idle.menuIndex == menuConnect {
			m.idle.focus = idleFocusConnect
		}
		return m, m.idle.syncFocus()
	case "down":
		if m.idle.menuIndex == menuConnect && m.idle.focus == idleFocusPeerID {
			m.idle.focus = idleFocusConnect
			return m, m.idle.syncFocus()
		}
		m.idle.menuIndex = (m.idle.menuIndex + 1) % menuCount
		if m.idle.menuIndex == menuConnect {
			m.idle.focus = idleFocusPeerID
		}
		return m, m.idle.syncFocus()
	case "tab":
		if m.idle.menuIndex == menuConnect {
			if m.idle.focus == idleFocusPeerID {
				m.idle.focus = idleFocusConnect
			} else {
				m.idle.focus = idleFocusPeerID
			}
			return m, m.idle.syncFocus()
		}
		return m, nil
	case "enter":
		return m.idleEnter()
	}

	if m.idle.menuIndex == menuConnect && m.idle.focus == idleFocusPeerID {
		var cmd tea.Cmd
		m.idle.peerID, cmd = m.idle.peerID.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) idleEnter() (tea.Model, tea.Cmd) {
	switch m.idle.menuIndex {
	case menuConnect:
		if m.idle.focus == idleFocusPeerID {
			if strings.TrimSpace(m.idle.peerID.Value()) == "" {
				return m, nil
			}
		}
		return m.tryConnect()
	case menuAddressBook, menuProfile:
		m.status = "Coming soon"
		return m, nil
	}
	return m, nil
}

func (m model) tryConnect() (tea.Model, tea.Cmd) {
	peerID := strings.TrimSpace(m.idle.peerID.Value())
	if peerID == "" {
		m.status = "Peer ID required"
		return m, nil
	}
	m.status = "Connecting..."
	m.signals.connect(peerID)
	return m, nil
}

func (m model) updateCalling(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "m", "M":
		m.calling.muted = !m.calling.muted
		for i := range m.calling.callers {
			if m.calling.callers[i].Local {
				m.calling.callers[i].Muted = m.calling.muted
			}
		}
		m.signals.mute()
		return m, nil
	case "Q":
		m.signals.disconnect()
		return m, nil
	}
	return m, nil
}

func capCallers(callers []Caller) []Caller {
	if len(callers) > 6 {
		callers = callers[:6]
	}
	return cloneCallers(callers)
}
