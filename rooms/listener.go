package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Roomlistener handles the lobby bus message for a room.
// It is used to join a room and receive updates on the allowed peers.
// Which reflects back in room.GetPeers()
type RoomListener struct {
	stream  network.Stream
	room    *Room
	signals *RoomListenerSignals
	decoder *json.Decoder
	encoder *json.Encoder
}

type RoomListenerSignals struct {
	OnUpdatedAllowedPeers func(peers []peer.ID)
}

type listenerMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func NewRoomListener(stream network.Stream, room *Room, signals *RoomListenerSignals) *RoomListener {
	if room == nil {
		panic("rooms: RoomListener requires a room")
	}
	if signals == nil {
		signals = &RoomListenerSignals{}
	}
	return &RoomListener{
		stream:  stream,
		room:    room,
		signals: signals,
		decoder: json.NewDecoder(stream),
		encoder: json.NewEncoder(stream),
	}
}

// Join requests room admission and waits until the host has committed the
// membership update. Start must only be called after Join succeeds.
func (rl *RoomListener) Join(ctx context.Context, request JoinRequest) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := rl.stream.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set room join deadline: %w", err)
		}
		defer func() {
			_ = rl.stream.SetDeadline(time.Time{})
		}()
	}

	if err := rl.encoder.Encode(LobbyMessage{
		Type:    "request-to-join",
		Payload: request,
	}); err != nil {
		return fmt.Errorf("send room join request: %w", err)
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		var msg listenerMessage
		if err := rl.decoder.Decode(&msg); err != nil {
			return fmt.Errorf("receive room join response: %w", err)
		}

		switch msg.Type {
		case "allowed-to-join":
			return rl.onJoinAllowed(msg)
		case "current-allowed-peers":
			if err := rl.onUpdateAllowedPeers(msg); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

func (rl *RoomListener) Start() {
	go rl.listen()
}

func (rl *RoomListener) Stop() {
	rl.room.setPeers(nil)
}

func (rl *RoomListener) listen() {
	defer rl.stream.Close()
	for {
		var msg listenerMessage
		if err := rl.decoder.Decode(&msg); err != nil {
			log.Printf("room listener stream ended: %v", err)
			return
		}

		switch msg.Type {
		case "current-allowed-peers":
			if err := rl.onUpdateAllowedPeers(msg); err != nil {
				log.Printf("room listener update allowed peers failed: %v", err)
			}
		default:
			continue
		}

	}
}

func (rl *RoomListener) onJoinAllowed(msg listenerMessage) error {
	var response JoinResponse
	if err := json.Unmarshal(msg.Payload, &response); err != nil {
		return fmt.Errorf("parse room join response: %w", err)
	}
	if !response.Allowed {
		return errors.New("room join request was rejected")
	}

	peers, err := decodePeers(response.Members)
	if err != nil {
		return err
	}
	rl.room.setPeers(peers)
	return nil
}

func (rl *RoomListener) onUpdateAllowedPeers(msg listenerMessage) error {
	var update PeerUpdate
	if err := json.Unmarshal(msg.Payload, &update); err != nil {
		return fmt.Errorf("parse room peer update: %w", err)
	}

	peers, err := decodePeers(update.Members)
	if err != nil {
		return err
	}
	rl.room.setPeers(peers)

	if rl.signals != nil && rl.signals.OnUpdatedAllowedPeers != nil {
		rl.signals.OnUpdatedAllowedPeers(peers)
	}
	return nil
}

func decodePeers(members []PeerInfo) ([]peer.ID, error) {
	peers := make([]peer.ID, 0, len(members))
	for _, member := range members {
		id, err := peer.Decode(member.PeerID)
		if err != nil {
			return nil, fmt.Errorf("decode room peer %q: %w", member.PeerID, err)
		}
		peers = append(peers, id)
	}
	return peers, nil
}

func (rl *RoomListener) AllowedPeers() []peer.ID {
	return rl.room.GetPeers()
}

func (rl *RoomListener) IsAllowed(peerID peer.ID) bool {
	return rl.room.IsAllowed(peerID)
}
