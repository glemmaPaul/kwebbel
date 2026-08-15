package transport

import (
	"errors"
	"slices"
	"testing"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/pion/webrtc/v4/pkg/media"
)

func TestMixerImplementsPusher(t *testing.T) {
	mixer := audio.NewMixer()
	var output Pusher = mixer

	output.Push("peer-a", []int16{120, -80})

	if got, want := mixer.GetMixedSamples(2), []int16{120, -80}; !slices.Equal(got, want) {
		t.Fatalf("mixed samples = %v, want %v", got, want)
	}
}

func TestOpusAudioTransportPublishesToRegisteredTracks(t *testing.T) {
	transport := NewOpusAudioTransport(nil)
	first := &fakeOpusTrack{}
	second := &fakeOpusTrack{}
	transport.AddTrack("first", first)
	transport.AddTrack("second", second)

	packet := []byte{1, 2, 3}
	if err := transport.publishPacket(packet); err != nil {
		t.Fatalf("publish: %v", err)
	}
	transport.RemoveTrack("second")
	if err := transport.publishPacket(packet); err != nil {
		t.Fatalf("publish after removal: %v", err)
	}

	if got := len(first.samples); got != 2 {
		t.Fatalf("first track samples = %d, want 2", got)
	}
	if got := len(second.samples); got != 1 {
		t.Fatalf("second track samples = %d, want 1", got)
	}
}

func TestOpusAudioTransportReturnsTrackErrors(t *testing.T) {
	transport := NewOpusAudioTransport(nil)
	transport.AddTrack("failed", &fakeOpusTrack{err: errors.New("write failed")})

	if err := transport.publishPacket([]byte{1}); err == nil {
		t.Fatal("publish returned nil error")
	}
}

type fakeOpusTrack struct {
	samples []media.Sample
	err     error
}

func (t *fakeOpusTrack) WriteSample(sample media.Sample) error {
	t.samples = append(t.samples, sample)
	return t.err
}
