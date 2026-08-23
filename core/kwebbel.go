package core

import (
	"context"
	"fmt"
	"time"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/logging"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/kwebbelkorp/kwebbel/transport"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
)

type MCAttributes struct {
	room     *rooms.Room
	mcServer *rooms.MCServer
}

type Kwebbelaar struct {
	host        host.Host
	identity    *identity.IdentityManager
	audioEgress *audio.AudioEgress
	room        *rooms.Room
	mcServer    *rooms.MCServer
	peerTracker *transport.PeerTracker
}

func NewKwebbelaar(host host.Host, identity *identity.IdentityManager, room *rooms.Room, tracker *transport.PeerTracker) *Kwebbelaar {
	return &Kwebbelaar{
		host:        host,
		identity:    identity,
		peerTracker: tracker,
		room:        room,
	}
}

func (k *Kwebbelaar) HostRoom(ctx context.Context) error {
	if k.mcServer != nil {
		return fmt.Errorf("you are already hosting a room")
	}
	if k.room == nil {
		return fmt.Errorf("cannot host without a configured room")
	}
	k.room.AddPeer(peer.ID(k.host.ID()))
	mcServer := rooms.NewMCServer(k.room)
	if err := mcServer.Serve(ctx); err != nil {
		return fmt.Errorf("start room server: %w", err)
	}
	k.host.SetStreamHandler(rooms.RoomProtocol, mcServer.AttachStream)
	k.mcServer = mcServer

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

func (k *Kwebbelaar) StartAudioInput(mic *audio.AudioInput) (*audio.AudioEgress, error) {
	err := mic.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start audio input: %w", err)
	}

	egress := audio.NewAudioEgress(mic.OutputChan)
	egress.Start()
	k.audioEgress = egress
	return egress, nil
}

func (k *Kwebbelaar) Attend(ctx context.Context, addr *peer.AddrInfo) error {
	logger := logging.FromContext(ctx)
	if addr == nil {
		return fmt.Errorf("cannot attend room without a peer address")
	}
	if k.room == nil {
		return fmt.Errorf("cannot attend without a configured room")
	}

	// Relay connections are "limited" - must opt-in to use them for streams
	k.host.Peerstore().AddAddrs(addr.ID, addr.Addrs, peerstore.PermanentAddrTTL)

	logger.Printf("Opening room stream to: %s", addr.ID)
	roomStream, err := k.host.NewStream(network.WithAllowLimitedConn(ctx, string(rooms.RoomProtocol)), addr.ID, rooms.RoomProtocol)
	if err != nil {
		return fmt.Errorf("open room stream to %s: %w", addr.ID, err)
	}

	listener := rooms.NewRoomListener(roomStream, k.room, &rooms.RoomListenerSignals{
		OnUpdatedAllowedPeers: func(_ []peer.ID) {
			// TODO: Find a better way to connect the peer tracking to the allowed peers
			if err := k.peerTracker.SyncPeers(); err != nil {
				logger.Printf("Failed to sync room peers: %v", err)
			}
		},
	})

	joinCtx, joinCancel := context.WithTimeout(ctx, 10*time.Second)
	defer joinCancel()
	err = listener.Join(joinCtx, rooms.JoinRequest{
		PeerID: k.host.ID().String(),
	})
	if err != nil {
		_ = roomStream.Reset()
		return fmt.Errorf("join room hosted by %s: %w", addr.ID, err)
	}
	listener.Start()

	if err := k.peerTracker.Dial(ctx, *addr); err != nil {
		logger.Printf("Initial WebRTC dial failed; retries remain active for %s: %v", addr.ID, err)
	}
	return nil
}
