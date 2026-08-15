package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func apply(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func TestIdleConnectEmitsSignalAndStaysIdle(t *testing.T) {
	var got string
	m := newModel(Signals{
		OnConnect: func(peerID string) { got = peerID },
	})
	m.idle.peerID.SetValue("12D3KooWpeer")
	m.idle.focus = idleFocusConnect

	m = apply(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got != "12D3KooWpeer" {
		t.Fatalf("OnConnect got %q", got)
	}
	if m.screen != screenIdle {
		t.Fatal("connect should not enter calling; controller must call EnterCall")
	}
	if m.status != "Connecting..." {
		t.Fatalf("status = %q", m.status)
	}
}

func TestIdleConnectRequiresPeerID(t *testing.T) {
	called := false
	m := newModel(Signals{
		OnConnect: func(string) { called = true },
	})
	m.idle.focus = idleFocusConnect
	m = apply(m, tea.KeyMsg{Type: tea.KeyEnter})
	if called {
		t.Fatal("expected no OnConnect")
	}
	if m.status != "Peer ID required" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestComingSoonMenus(t *testing.T) {
	m := newModel(Signals{})
	m.idle.menuIndex = menuAddressBook
	m = apply(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != "Coming soon" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestControllerEnterAndLeaveCall(t *testing.T) {
	m := newModel(Signals{})
	m = apply(m, enterCallMsg{callers: []Caller{
		{ID: "a", Label: "Alice", Local: true},
		{ID: "b", Label: "Bob"},
	}})
	if m.screen != screenCalling {
		t.Fatal("expected calling screen")
	}
	if len(m.calling.callers) != 2 {
		t.Fatalf("callers = %d", len(m.calling.callers))
	}

	m = apply(m, leaveCallMsg{})
	if m.screen != screenIdle {
		t.Fatal("expected idle screen")
	}
	if len(m.calling.callers) != 0 {
		t.Fatal("callers should clear on leave")
	}
}

func TestCallingMuteAndDisconnectSignals(t *testing.T) {
	muted := 0
	disconnected := 0
	m := newModel(Signals{
		OnMute:       func() { muted++ },
		OnDisconnect: func() { disconnected++ },
	})
	m = apply(m, enterCallMsg{callers: []Caller{{ID: "me", Local: true}}})
	m = apply(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if !m.calling.muted || !m.calling.callers[0].Muted {
		t.Fatal("mute should toggle local UI immediately")
	}
	if muted != 1 {
		t.Fatalf("OnMute calls = %d", muted)
	}

	m = apply(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	if disconnected != 1 {
		t.Fatalf("OnDisconnect calls = %d", disconnected)
	}
	if m.screen != screenCalling {
		t.Fatal("disconnect should not leave calling; controller must call LeaveCall")
	}
}

func TestCapCallers(t *testing.T) {
	callers := make([]Caller, 8)
	got := capCallers(callers)
	if len(got) != 6 {
		t.Fatalf("len = %d", len(got))
	}
}
