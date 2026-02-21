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

func (im *AudioIngress) ReadStream(s network.Stream) {
	defer s.Close()

	peerID := s.Conn().RemotePeer().String()

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
		// Read the length header
		_, err := io.ReadFull(s, headerBuf)
		if err != nil {
			return // TODO: Handle errorStream closed or error
		}

		// header represents the length of the packet
		packetLen := binary.BigEndian.Uint16(headerBuf)

		// read the opus payload
		opusBuf := make([]byte, packetLen)
		_, err = io.ReadFull(s, opusBuf)
		if err != nil {
			return
		}

		// decode the opus payload
		n, err := decoder.Decode(opusBuf, pcmBuffer)
		if err != nil {
			log.Println("Decode error:", err)
			continue
		}

		// push the decoded samples to the mixer
		if im.mixer != nil {
			// copy the data because pcmBuffer is reused in the next loop
			samples := make([]int16, n)
			copy(samples, pcmBuffer[:n])

			im.mixer.PushAudio(peerID, samples)
		}
	}
}
