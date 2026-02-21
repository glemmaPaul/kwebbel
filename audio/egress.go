package audio

import (
	"encoding/binary"
	"log"

	"github.com/hraban/opus"
)

type AudioEgress struct {
	Input  <-chan []byte // Raw bytes from Malgo
	Output func([]byte)  // ConnectionManager.Broadcast
}

func (t *AudioEgress) StartProcessing() {
	// 1. Initialize Opus Encoder
	// 48000 Hz, 1 Channel, VoIP Application type
	enc, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if err != nil {
		log.Fatalf("Failed to create Opus encoder: %v", err)
	}

	go func() {
		// Buffer for the Opus output (1KB is plenty for voice)
		opusBuffer := make([]byte, 1000)

		for rawBytes := range t.Input {
			// 2. Convert []byte (Malgo) to []int16 (Opus)
			pcmData := BytesToInt16(rawBytes)

			// 3. Safety Check: Ensure we have exactly 960 samples (20ms)
			// Opus will error if frame size is wrong.
			if len(pcmData) != 960 {
				log.Printf("Warning: Incorrect frame size: %d samples", len(pcmData))
				continue
			}

			// 4. Encode
			n, err := enc.Encode(pcmData, opusBuffer)
			if err != nil {
				log.Printf("Opus encode error: %v", err)
				continue
			}

			// 5. Send encoded data to network
			// We must slice it to 'n' bytes, otherwise we send empty zeros
			encodedPacket := make([]byte, n)
			copy(encodedPacket, opusBuffer[:n])

			t.Output(encodedPacket)
		}
	}()
}

// BytesToInt16 converts raw bytes (Little Endian) to []int16
// Malgo outputs Little Endian by default on most architectures.
func BytesToInt16(data []byte) []int16 {
	// 2 bytes = 1 int16
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

func NewChannel() <-chan []byte {
	return make(chan []byte, 100)
}
