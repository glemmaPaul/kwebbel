package tui

type enterCallMsg struct {
	callers []Caller
}

type leaveCallMsg struct{}

type setCallersMsg struct {
	callers []Caller
}

type setMutedMsg struct {
	muted bool
}

type setStatusMsg struct {
	text string
}
