package audio

import "testing"

func TestAudioEgressMuted(t *testing.T) {
	egress := NewAudioEgress(nil)

	if egress.Muted() {
		t.Fatal("new audio egress should not be muted")
	}

	egress.SetMuted(true)
	if !egress.Muted() {
		t.Fatal("audio egress should be muted after SetMuted(true)")
	}

	egress.SetMuted(false)
	if egress.Muted() {
		t.Fatal("audio egress should not be muted after SetMuted(false)")
	}
}
