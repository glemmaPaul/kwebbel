package kwebbel

import (
	"context"
	"fmt"
	"log"

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

type Kwebbelaar struct {
	host         host.Host
	conn         *conn.ConnectionManager
	identity     *identity.IdentityManager
	audioIngress *audio.AudioIngress
	audioEgress  *audio.AudioEgress
	// Room management
	roomManager *rooms.RoomManager
}

func NewKwebbelaar(host host.Host, conn *conn.ConnectionManager, identity *identity.IdentityManager) *Kwebbelaar {
	return &Kwebbelaar{
		host:     host,
		conn:     conn,
		identity: identity,
	}
}

func (k *Kwebbelaar) Start() error {
	return nil
}

func (k *Kwebbelaar) JoinRoom(peerID peer.ID) error {
	log.Println("Joining room", peerID)
	roomManager, err := rooms.AttendRoom(context.Background(), k.host, peerID)
	if err != nil {
		return fmt.Errorf("failed to join room: %w", err)
	}
	k.roomManager = roomManager
	return nil
}

func (k *Kwebbelaar) BecomeHost(room rooms.Room) error {
	log.Println("Becoming host", room)
	roomManager, err := rooms.HostRoom(context.Background(), k.host, room)
	if err != nil {
		return fmt.Errorf("failed to become host: %w", err)
	}
	k.roomManager = roomManager
	return nil
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

func (k *Kwebbelaar) StartAudioEgress() (*audio.AudioEgress, error) {
	// 2. Start Audio Input (Mic)
	mic, err := audio.NewAudioInput()
	if err != nil {
		return nil, fmt.Errorf("failed to create audio input: %w", err)
	}

	egress := audio.NewAudioEgress(mic.OutputChan)
	egress.StartProcessing()
	err = mic.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start audio input: %w", err)
	}
	k.audioEgress = egress
	return egress, nil
}
