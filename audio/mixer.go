package audio

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

var (
	clampMax int32 = 32767
	clampMin int32 = -32768
)

// PeerAudioMix holds one peer's queued PCM plus playback controls.
type PeerAudioMix struct {
	buffer []int16
	level  int
	muted  bool
}

// Mixer combines multiple incoming PCM streams into one stream towards audio outputs.
// Holds per peer state for the buffer and playback controls.
type Mixer struct {
	mu sync.Mutex
	// Map of PeerID -> per-peer mix state
	peers map[string]*PeerAudioMix
}

func NewMixer() *Mixer {
	return &Mixer{
		peers: make(map[string]*PeerAudioMix),
	}
}

func (m *Mixer) peerMix(peerID string) *PeerAudioMix {
	mix, ok := m.peers[peerID]
	if !ok {
		mix = &PeerAudioMix{level: 100}
		m.peers[peerID] = mix
	}
	return mix
}

// Push adds new audio from a specific peer to their buffer.
// Call this from your Network Reader loop after decoding Opus -> Int16
func (m *Mixer) Push(peerID peer.ID, pcmData []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()

	mix := m.peerMix(peerID.String())
	mix.buffer = append(mix.buffer, pcmData...)
}

// ChangeVolume sets a peer's playback level from 0 (silence) to 100 (full).
// Values outside that range are clamped. The setting is kept even if the peer
// has not sent audio yet.
func (m *Mixer) ChangeVolume(peerID peer.ID, volume int) {
	if volume < 0 {
		volume = 0
	} else if volume > 100 {
		volume = 100
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerMix(peerID.String()).level = volume
}

// MutePeer silences or unsilences a peer without changing their volume level.
func (m *Mixer) MutePeer(peerID peer.ID, muted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.peerMix(peerID.String()).muted = muted
}

// GetMixedSamples is called by AudioOutput (Malgo)
// It sums up all the buffers and returns the final mix
func (m *Mixer) GetMixedSamples(samplesNeeded int) []int16 {
	m.mu.Lock()
	defer m.mu.Unlock()

	mixedOutput := make([]int16, samplesNeeded)
	// Use int32 for math to avoid overflow before clamping
	tempSum := make([]int32, samplesNeeded)

	for _, mix := range m.peers {
		// If peer has enough data
		if len(mix.buffer) >= samplesNeeded {
			if !mix.muted && mix.level > 0 {
				for i := 0; i < samplesNeeded; i++ {
					tempSum[i] += int32(mix.buffer[i]) * int32(mix.level) / 100
				}
			}

			// Shift buffer (Remove played audio), including muted/zero-volume peers
			// so they do not backlog while silenced.
			mix.buffer = mix.buffer[samplesNeeded:]
		} else {
			// Handle Underrun: If peer is lagging, we skip them (Silence)
			// Optional: In production, add PLC (Packet Loss Concealment) here
		}
	}

	// Primitive clamping
	// clamping is to prevent overflow, giving distorted audio
	for i := 0; i < samplesNeeded; i++ {
		val := tempSum[i]
		if val > clampMax {
			val = clampMax
		} else if val < clampMin {
			val = clampMin
		}
		mixedOutput[i] = int16(val)
	}

	return mixedOutput
}
