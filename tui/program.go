package tui

import tea "github.com/charmbracelet/bubbletea"

// Program wraps a Bubble Tea program and exposes controller levers.
type Program struct {
	tea *tea.Program
}

// New builds a TUI program. Call Run to start the event loop.
func New(signals Signals) *Program {
	return &Program{
		tea: tea.NewProgram(newModel(signals), tea.WithAltScreen()),
	}
}

func (p *Program) Run() error {
	_, err := p.tea.Run()
	return err
}

func (p *Program) Quit() {
	p.tea.Quit()
}

// EnterCall switches the view to the calling screen.
func (p *Program) EnterCall(callers []Caller) {
	p.tea.Send(enterCallMsg{callers: cloneCallers(callers)})
}

// LeaveCall returns the view to the idle screen.
func (p *Program) LeaveCall() {
	p.tea.Send(leaveCallMsg{})
}

// SetCallers replaces the participants shown on the calling screen.
func (p *Program) SetCallers(callers []Caller) {
	p.tea.Send(setCallersMsg{callers: cloneCallers(callers)})
}

// SetMuted updates mute state shown in the calling controls.
func (p *Program) SetMuted(muted bool) {
	p.tea.Send(setMutedMsg{muted: muted})
}

// SetStatus sets a status line (connecting, error, or idle hint).
func (p *Program) SetStatus(msg string) {
	p.tea.Send(setStatusMsg{text: msg})
}

func cloneCallers(callers []Caller) []Caller {
	if len(callers) == 0 {
		return nil
	}
	out := make([]Caller, len(callers))
	copy(out, callers)
	return out
}
