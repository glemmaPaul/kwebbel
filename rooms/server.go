package rooms

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type MCServer struct {
	mu           sync.RWMutex
	ActiveGuests map[peer.ID]network.Stream
	AllowedPeers map[peer.ID]peer.ID // The "Ground Truth"
}

type LobbyMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

func NewMCServer() *MCServer {
	return &MCServer{
		ActiveGuests: make(map[peer.ID]network.Stream),
		AllowedPeers: make(map[peer.ID]peer.ID),
	}
}

func (mc *MCServer) Serve(host host.Host) error {
	host.SetStreamHandler(RoomProtocol, func(s network.Stream) {
		log.Println("Received stream from", s.Conn().RemotePeer())
		mc.ActiveGuests[s.Conn().RemotePeer()] = s
		mc.HandleLobbyBus(s)
	})
	return nil
}

func (mc *MCServer) HandleLobbyBus(s network.Stream) {
	decoder := json.NewDecoder(s)
	encoder := json.NewEncoder(s)

	for {
		var msg LobbyMessage
		if err := decoder.Decode(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "request-to-join":
			log.Println("Request to join room", msg.Payload)
			encoder.Encode(LobbyMessage{Type: "allowed-to-join", Payload: true})
		}
	}
}

func (mc *MCServer) _broadcast(msg LobbyMessage) {
	for id, stream := range mc.ActiveGuests {
		enc := json.NewEncoder(stream)
		if err := enc.Encode(msg); err != nil {
			log.Printf("Failed to update peer %s", id)
		}
	}
}

func (mc *MCServer) BroadcastPeerList() {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	members := make([]PeerInfo, 0, len(mc.AllowedPeers))
	for peerID, relayAddr := range mc.AllowedPeers {
		members = append(members, PeerInfo{PeerID: peerID.String(), RelayAddr: relayAddr.String()})
	}

	update := LobbyMessage{
		Type: "current-allowed-peers",
		Payload: PeerUpdate{
			Members: members,
		},
	}

	mc._broadcast(update)
}

func (mc *MCServer) Heartbeat(ctx context.Context) error {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				mc.BroadcastPeerList()
			}
		}
	}()
	return nil
}
