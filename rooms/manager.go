package rooms

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type RoomManager struct {
	CurrentRoom *Room
	mcServer    *MCServer
}

func NewRoomManager() *RoomManager {
	return &RoomManager{}
}

func AttendRoom(ctx context.Context, host host.Host, peerID peer.ID) (*RoomManager, error) {
	rm := NewRoomManager()
	go rm.JoinRoom(ctx, host, peerID)
	return rm, nil
}

func HostRoom(ctx context.Context, host host.Host, room Room) (*RoomManager, error) {
	rm := NewRoomManager()
	go rm.HostRoom(ctx, host, room)
	return rm, nil
}

func (rm *RoomManager) JoinRoom(ctx context.Context, host host.Host, peerID peer.ID) error {
	// 1. Dial Owner
	log.Println("Dialing room", peerID)
	supported, err := host.Peerstore().SupportsProtocols(peerID, RoomProtocol)
	if err != nil {
		log.Println("Failed to check if peer supports room protocol", peerID, err)
		return fmt.Errorf("failed to check if peer supports room protocol: %w", err)
	}
	if len(supported) == 0 {
		log.Println("Peer does not support room protocol", peerID)
		return fmt.Errorf("peer does not support room protocol")
	}

	log.Println("Peer supports room protocol", peerID, supported)

	maxRetries := 3
	var stream network.Stream
	for i := 0; i < maxRetries; i++ {
		stream, err = host.NewStream(context.Background(), peerID, RoomProtocol)
		if err != nil {
			log.Println("Failed to create stream to", peerID, err)
			continue
		}
		log.Println("Joined room", peerID)
	}

	if stream == nil {
		return fmt.Errorf("failed to join room after %d retries", maxRetries)
	}

	enc := json.NewEncoder(stream)
	dec := json.NewDecoder(stream)
	ticket := host.ID().String()

	enc.Encode(LobbyMessage{
		Type: "request-to-join",
		Payload: JoinRequest{
			PeerID: host.ID().String(),
			Ticket: ticket,
		},
	})

	// 3. Keep stream open and listen for state changes
	for {
		var msg LobbyMessage
		if err := dec.Decode(&msg); err != nil {
			log.Println("Disconnected from MC Lobby")
			return fmt.Errorf("disconnected from MC Lobby")
		}

		switch msg.Type {
		case "allowed-to-join":
			if msg.Payload.(bool) {
				log.Println("MC approved our entry!")
			}
		case "current-allowed-peers":
			log.Println("Current allowed peers", msg.Payload)
		default:
			log.Println("Unknown message type", msg.Type)
		}
	}
}

func (rm *RoomManager) HostRoom(ctx context.Context, host host.Host, room Room) error {
	mcServer := NewMCServer()
	err := mcServer.Serve(host)
	if err != nil {
		return fmt.Errorf("failed to serve MC server: %w", err)
	}
	go mcServer.Heartbeat(ctx)
	rm.mcServer = mcServer
	return nil
}
