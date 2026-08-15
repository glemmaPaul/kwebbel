package tui

import "testing"

func TestViewCallerGridLayouts(t *testing.T) {
	for n := 0; n <= 7; n++ {
		callers := make([]Caller, n)
		for i := range callers {
			callers[i] = Caller{ID: "id", Label: "peer"}
		}
		out := viewCallerGrid(callers, 80, 24)
		if out == "" {
			t.Fatalf("empty grid for %d callers", n)
		}
	}
}

func TestIdleAndCallingViewsRender(t *testing.T) {
	m := newModel(Signals{})
	m.width = 80
	m.height = 24
	if m.View() == "" {
		t.Fatal("idle view empty")
	}
	m.screen = screenCalling
	m.calling.callers = []Caller{{ID: "a", Label: "A", Local: true}}
	if m.View() == "" {
		t.Fatal("calling view empty")
	}
}
