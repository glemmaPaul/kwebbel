package audio

import (
	"encoding/binary"
	"log"

	"github.com/jj11hh/opus"
)

type AudioEgress struct {
	// Input receives little-endian int16 PCM frames (20ms, 960 samples @ 48kHz).
	Input <-chan []byte
	// Output produces Opus packets to publish over WebRTC.
	Output chan []byte
}

func NewAudioEgress(input <-chan []byte) *AudioEgress {
	return &AudioEgress{
		Input:  input,
		Output: make(chan []byte, 100),
	}
}

func (t *AudioEgress) StartProcessing() {
	// 1. Initialize Opus Encoder
	// 48000 Hz, 1 Channel, VoIP Application type
	enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("Failed to create Opus encoder: %v", err)
	}

	go func() {
		// Buffer for the Opus output (1KB is enough for typical voice frames).
		opusBuffer := make([]byte, 1000)

		for rawBytes := range t.Input {
			pcmData := BytesToInt16(rawBytes)

			// Opus expects fixed frame size for this setup (20ms @ 48kHz mono).
			if len(pcmData) != 960 {
				log.Printf("Warning: Incorrect frame size: %d samples", len(pcmData))
				continue
			}

			n, err := enc.Encode(pcmData, opusBuffer)
			if err != nil {
				log.Printf("Opus encode error: %v", err)
				continue
			}

			// Forward only the encoded bytes for this frame.
			encodedPacket := make([]byte, n)
			copy(encodedPacket, opusBuffer[:n])
			t.Output <- encodedPacket
		}
	}()
}

// BytesToInt16 converts little-endian PCM bytes to int16 samples.
func BytesToInt16(data []byte) []int16 {
	if len(data)%2 != 0 {
		log.Println("Odd byte length, data corruption?")
		return nil
	}

	numSamples := len(data) / 2
	out := make([]int16, numSamples)

	for i := 0; i < numSamples; i++ {
		// Read 2 bytes at a time
		// byte index: i*2
		sample := binary.LittleEndian.Uint16(data[i*2 : i*2+2])
		out[i] = int16(sample)
	}

	return out
}
