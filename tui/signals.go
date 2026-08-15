package tui

// Signals are view-to-controller callbacks. Nil funcs are no-ops so the TUI
// can run without a controller attached.
type Signals struct {
	OnConnect    func(peerID string)
	OnMute       func()
	OnDisconnect func()
}

func (s Signals) connect(peerID string) {
	if s.OnConnect != nil {
		s.OnConnect(peerID)
	}
}

func (s Signals) mute() {
	if s.OnMute != nil {
		s.OnMute()
	}
}

func (s Signals) disconnect() {
	if s.OnDisconnect != nil {
		s.OnDisconnect()
	}
}

// Caller is display state for one participant in the calling view.
type Caller struct {
	ID    string
	Label string
	Muted bool
	Local bool
}
