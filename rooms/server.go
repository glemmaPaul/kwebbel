package rooms

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type PeerStream struct {
	stream network.Stream
	peerID peer.ID
}

type MCServer struct {
	mu           sync.RWMutex
	streams      map[peer.ID]network.Stream
	allowedPeers []peer.ID
	activeRoom   *Room
}

type LobbyMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

func NewMCServer(room *Room) *MCServer {
	return &MCServer{
		streams:      make(map[peer.ID]network.Stream),
		allowedPeers: make([]peer.ID, 0),
		activeRoom:   room,
	}
}

func (mc *MCServer) Serve() error {
	// Not yet a lot here..
	log.Println("Serving MC server for room", mc.activeRoom.Name)
	go mc.Heartbeat(context.Background())
	return nil
}

func (mc *MCServer) AttachStream(s network.Stream) {
	log.Println("Received room message stream from", s.Conn().RemotePeer())
	// Check if the peer stream is allowed in peers
	peerID := s.Conn().RemotePeer()
	if mc.activeRoom != nil {
		mc.activeRoom.AddPeer(peerID)
	}
	mc.mu.Lock()
	if _, ok := mc.streams[peerID]; ok {
		mc.mu.Unlock()
		log.Println("Peer stream already exists", peerID)
		return
	}
	mc.streams[peerID] = s
	mc.mu.Unlock()

	go func() {
		defer func() {
			mc.mu.Lock()
			delete(mc.streams, peerID)
			mc.mu.Unlock()
			if mc.activeRoom != nil {
				mc.activeRoom.RemovePeer(peerID)
			}
		}()

		if err := mc.HandleLobbyBus(context.Background(), s); err != nil {
			log.Printf("room stream ended for %s: %v", peerID, err)
		}
	}()
}

func (mc *MCServer) HandleLobbyBus(context context.Context, s network.Stream) error {
	decoder := json.NewDecoder(s)
	encoder := json.NewEncoder(s)

	for {
		var msg LobbyMessage
		if err := decoder.Decode(&msg); err != nil {
			return err
		}

		switch msg.Type {
		case "request-to-join":
			log.Println("Request to join room", msg.Payload)
			encoder.Encode(LobbyMessage{Type: "allowed-to-join", Payload: true})
		}
	}
}

func (mc *MCServer) allowedPeerStreams() map[peer.ID]network.Stream {
	if mc.activeRoom == nil {
		return make(map[peer.ID]network.Stream)
	}

	allowedPeers := mc.activeRoom.GetPeers()
	streams := make(map[peer.ID]network.Stream)

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, peerID := range allowedPeers {
		stream, ok := mc.streams[peerID]
		if !ok || stream == nil {
			continue
		}
		streams[peerID] = stream
	}
	return streams
}

func (mc *MCServer) _broadcast(msg LobbyMessage, streams map[peer.ID]network.Stream) {
	for id, stream := range streams {
		if stream == nil {
			continue
		}
		enc := json.NewEncoder(stream)
		if err := enc.Encode(msg); err != nil {
			log.Printf("Failed to update peer %s: %v", id.String(), err)
		}
	}
}

func (mc *MCServer) BroadcastPeerList() {
	if mc.activeRoom == nil {
		return
	}

	allowedPeers := mc.activeRoom.GetPeers()
	members := make([]PeerInfo, 0, len(allowedPeers))
	for _, peerID := range allowedPeers {
		members = append(members, PeerInfo{PeerID: peerID.String()})
	}

	update := LobbyMessage{
		Type: "current-allowed-peers",
		Payload: PeerUpdate{
			Members: members,
		},
	}

	mc._broadcast(update, mc.allowedPeerStreams())
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
