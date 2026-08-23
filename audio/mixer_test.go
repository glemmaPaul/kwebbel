package audio

import (
	"slices"
	"testing"
)

func TestMixerDefaultVolumeIsFull(t *testing.T) {
	mixer := NewMixer()
	mixer.Push("peer-a", []int16{120, -80})

	if got, want := mixer.GetMixedSamples(2), []int16{120, -80}; !slices.Equal(got, want) {
		t.Fatalf("mixed samples = %v, want %v", got, want)
	}
}

func TestChangeVolumeScalesPeer(t *testing.T) {
	mixer := NewMixer()
	mixer.Push("peer-a", []int16{100, -80})
	mixer.ChangeVolume("peer-a", 50)

	if got, want := mixer.GetMixedSamples(2), []int16{50, -40}; !slices.Equal(got, want) {
		t.Fatalf("mixed samples = %v, want %v", got, want)
	}
}

func TestChangeVolumeClampsAndAppliesBeforePush(t *testing.T) {
	mixer := NewMixer()
	mixer.ChangeVolume("peer-a", 250)
	mixer.Push("peer-a", []int16{80})

	if got, want := mixer.GetMixedSamples(1), []int16{80}; !slices.Equal(got, want) {
		t.Fatalf("clamped full volume = %v, want %v", got, want)
	}

	mixer.ChangeVolume("peer-a", -10)
	mixer.Push("peer-a", []int16{80})

	if got, want := mixer.GetMixedSamples(1), []int16{0}; !slices.Equal(got, want) {
		t.Fatalf("clamped silence = %v, want %v", got, want)
	}
}

func TestMutePeerSilencesWithoutChangingVolume(t *testing.T) {
	mixer := NewMixer()
	mixer.ChangeVolume("peer-a", 50)
	mixer.Push("peer-a", []int16{100, -80})
	mixer.MutePeer("peer-a", true)

	if got, want := mixer.GetMixedSamples(2), []int16{0, 0}; !slices.Equal(got, want) {
		t.Fatalf("muted samples = %v, want %v", got, want)
	}

	mixer.Push("peer-a", []int16{100, -80})
	mixer.MutePeer("peer-a", false)

	if got, want := mixer.GetMixedSamples(2), []int16{50, -40}; !slices.Equal(got, want) {
		t.Fatalf("unmuted samples = %v, want %v", got, want)
	}
}

func TestMutedPeerDoesNotBacklog(t *testing.T) {
	mixer := NewMixer()
	mixer.Push("peer-a", []int16{100, 200})
	mixer.MutePeer("peer-a", true)
	mixer.GetMixedSamples(2)
	mixer.MutePeer("peer-a", false)
	mixer.Push("peer-a", []int16{40})

	if got, want := mixer.GetMixedSamples(1), []int16{40}; !slices.Equal(got, want) {
		t.Fatalf("samples after unmute = %v, want %v", got, want)
	}
}
