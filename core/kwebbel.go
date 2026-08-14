package core

import (
	"fmt"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/conn"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Group struct {
	id    string
	peers []string
}

type MCAttributes struct {
	room     *rooms.Room
	mcServer *rooms.MCServer
}

type Kwebbelaar struct {
	host         host.Host
	identity     *identity.IdentityManager
	audioEgress  *audio.AudioEgress
	webrtcBridge *conn.WebRTCAudioBridge
	group        *Group
	mcAttributes *MCAttributes
}

func NewKwebbelaar(host host.Host, identity *identity.IdentityManager, webrtcBridge *conn.WebRTCAudioBridge) *Kwebbelaar {
	return &Kwebbelaar{
		host:         host,
		identity:     identity,
		webrtcBridge: webrtcBridge,
	}
}

func (k *Kwebbelaar) BecomeMC() {
	room := rooms.NewRoom("default")
	room.AddPeer(peer.ID(k.host.ID()))
	mcServer := rooms.NewMCServer(room)
	mcServer.Serve()
	k.host.SetStreamHandler(rooms.RoomProtocol, mcServer.AttachStream)
	k.mcAttributes = &MCAttributes{
		room:     room,
		mcServer: mcServer,
	}
}

func (k *Kwebbelaar) StartAudioOutput(mixer *audio.Mixer) error {
	audioOutput, err := audio.NewAudioOutput(mixer)
	if err != nil {
		return fmt.Errorf("failed to create audio output: %w", err)
	}
	err = audioOutput.Start()
	if err != nil {
		return fmt.Errorf("failed to start audio output: %w", err)
	}
	return nil
}

func (k *Kwebbelaar) StartAudioInput(mic *audio.AudioInput) (*audio.AudioEgress, error) {
	err := mic.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start audio input: %w", err)
	}

	egress := audio.NewAudioEgress(mic.OutputChan)
	egress.Start()
	err = mic.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start audio input: %w", err)
	}
	k.audioEgress = egress
	return egress, nil
}
