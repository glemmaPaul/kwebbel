package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"gopkg.in/hraban/opus.v2"
)

const (
	opusSampleRate = 48000
	opusChannels   = 1
	// Opus supports frames up to 120ms: 5,760 samples at 48kHz.
	maxOpusFrameSamples = 5760
)

// Pusher receives decoded PCM samples grouped by their source.
type Pusher interface {
	Push(peerID peer.ID, samples []int16)
}

type OpusTrack interface {
	WriteSample(sample media.Sample) error
}

// OpusAudioTransport reads Opus from WebRTC tracks and forwards decoded PCM to a Pusher.
// handles both incoming and outgoing tracks
type OpusAudioTransport struct {
	output Pusher
	mu     sync.RWMutex
	tracks map[peer.ID]*opusTrackState
}

type opusTrackState struct {
	mu               sync.Mutex
	track            OpusTrack
	publishedPackets uint64
}

func NewOpusAudioTransport(output Pusher) *OpusAudioTransport {
	return &OpusAudioTransport{
		output: output,
		tracks: make(map[peer.ID]*opusTrackState),
	}
}

func (t *OpusAudioTransport) AcceptTrack(peerID peer.ID, track *webrtc.TrackRemote) error {
	go t.ReadTrack(peerID, track)
	return nil
}

func (t *OpusAudioTransport) AddTrack(peerID peer.ID, track OpusTrack) {
	t.mu.Lock()
	t.tracks[peerID] = &opusTrackState{track: track}
	t.mu.Unlock()
}

func (t *OpusAudioTransport) RemoveTrack(peerID peer.ID) {
	t.mu.Lock()
	delete(t.tracks, peerID)
	t.mu.Unlock()
}

func (t *OpusAudioTransport) Publish(ctx context.Context, opusPackets <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case packet, ok := <-opusPackets:
			if !ok {
				return
			}
			if err := t.publishPacket(packet); err != nil {
				log.Printf("failed publishing Opus packet: %v", err)
			}
		}
	}
}

func (t *OpusAudioTransport) publishPacket(opusPacket []byte) error {
	t.mu.RLock()
	tracks := make(map[peer.ID]*opusTrackState, len(t.tracks))
	for id, state := range t.tracks {
		tracks[id] = state
	}
	t.mu.RUnlock()

	errs := make([]error, 0)
	for peerID, state := range tracks {
		state.mu.Lock()
		err := state.track.WriteSample(media.Sample{
			Data:     opusPacket,
			Duration: 20 * time.Millisecond,
		})
		if err == nil {
			state.publishedPackets++
		}
		state.mu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("publish Opus packet to %s: %w", peerID, err))
		}
	}
	return errors.Join(errs...)
}

func (t *OpusAudioTransport) ReadTrack(peerID peer.ID, track *webrtc.TrackRemote) error {
	if track == nil {
		return errors.New("webrtc OpusAudioTransport requires a track")
	}
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return fmt.Errorf("unsupported track kind %s", track.Kind())
	}
	if !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeOpus) {
		return fmt.Errorf("unsupported audio codec %q", track.Codec().MimeType)
	}

	decoder, err := opus.NewDecoder(opusSampleRate, opusChannels)
	if err != nil {
		return fmt.Errorf("create Opus decoder: %w", err)
	}

	pcmBuffer := make([]int16, maxOpusFrameSamples)
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read Opus RTP packet: %w", err)
		}
		if len(packet.Payload) == 0 {
			continue
		}

		sampleCount, err := decoder.Decode(packet.Payload, pcmBuffer)
		if err != nil {
			return fmt.Errorf("decode Opus packet: %w", err)
		}
		if sampleCount == 0 {
			continue
		}

		samples := append([]int16(nil), pcmBuffer[:sampleCount]...)
		t.output.Push(peerID, samples)
	}
}
