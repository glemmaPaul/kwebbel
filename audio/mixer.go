package audio

import (
	"sync"
)

var (
	clampMax int32 = 32767
	clampMin int32 = -32768
)

// Mixer combines multiple incoming PCM streams into one
type Mixer struct {
	mu sync.Mutex
	// Map of PeerID -> Buffer of Audio Samples
	peerBuffers map[string][]int16
}

func NewMixer() *Mixer {
	return &Mixer{
		peerBuffers: make(map[string][]int16),
	}
}

// PushAudio adds new audio from a specific peer to their buffer
// Call this from your Network Reader loop after decoding Opus -> Int16
func (m *Mixer) PushAudio(peerID string, pcmData []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Append data to this peer's specific buffer
	m.peerBuffers[peerID] = append(m.peerBuffers[peerID], pcmData...)
}

// GetMixedSamples is called by AudioOutput (Malgo)
// It sums up all the buffers and returns the final mix
func (m *Mixer) GetMixedSamples(samplesNeeded int) []int16 {
	m.mu.Lock()
	defer m.mu.Unlock()

	mixedOutput := make([]int16, samplesNeeded)
	// Use int32 for math to avoid overflow before clamping
	tempSum := make([]int32, samplesNeeded)

	for peerID, buffer := range m.peerBuffers {
		// If peer has enough data
		if len(buffer) >= samplesNeeded {
			// 1. Summation
			for i := 0; i < samplesNeeded; i++ {
				tempSum[i] += int32(buffer[i])
			}

			// 2. Shift buffer (Remove played audio)
			m.peerBuffers[peerID] = buffer[samplesNeeded:]
		} else {
			// Handle Underrun: If peer is lagging, we skip them (Silence)
			// Optional: In production, add PLC (Packet Loss Concealment) here
		}
	}

	// 3. Clamping (Anti-Distortion)
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
