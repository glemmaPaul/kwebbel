package audio

import (
	"encoding/binary"
	"io"
	"log"

	"github.com/hraban/opus"
	"github.com/libp2p/go-libp2p/core/network"
)

type AudioIngress struct {
	mixer *Mixer
}

func NewAudioIngress(mixer *Mixer) *AudioIngress {
	return &AudioIngress{
		mixer: mixer,
	}
}

// 3. The Loop (Network -> Decoder -> Mixer)
func (im *AudioIngress) ReadStream(s network.Stream) {
	defer s.Close()

	peerID := s.Conn().RemotePeer().String()

	// CRITICAL: Create a NEW Decoder for THIS SPECIFIC PEER
	// Opus is stateful. You cannot share this decoder instance!
	decoder, err := opus.NewDecoder(48000, 1)
	if err != nil {
		log.Println("Failed to create decoder:", err)
		return
	}

	// Reusable buffer for reading length headers (2 bytes)
	headerBuf := make([]byte, 2)

	// Buffer for PCM output (20ms at 48kHz = 960 samples)
	pcmBuffer := make([]int16, 960)

	for {
		// A. Read the Length Header (Blocking)
		_, err := io.ReadFull(s, headerBuf)
		if err != nil {
			return // Stream closed or error
		}

		// B. Parse Length
		packetLen := binary.BigEndian.Uint16(headerBuf)

		// C. Read the Opus Payload
		opusBuf := make([]byte, packetLen)
		_, err = io.ReadFull(s, opusBuf)
		if err != nil {
			return
		}

		// D. DECODE (Opus -> PCM)
		// We decode directly into pcmBuffer to save memory
		n, err := decoder.Decode(opusBuf, pcmBuffer)
		if err != nil {
			log.Println("Decode error:", err)
			continue
		}

		// E. PUSH TO MIXER
		// We pass the decoded samples (only the valid 'n' samples)
		if im.mixer != nil {
			// We must copy the data because pcmBuffer is reused in the next loop
			samples := make([]int16, n)
			copy(samples, pcmBuffer[:n])

			im.mixer.PushAudio(peerID, samples)
		}
	}
}
