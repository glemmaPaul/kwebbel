package rooms

import (
	"encoding/json"
	"log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type RoomListener struct {
	stream  network.Stream
	room    *Room
	signals *RoomListenerSignals
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
	}
}

func (rl *RoomListener) Start() {
	decoder := json.NewDecoder(rl.stream)
	go rl.listen(decoder)
}

func (rl *RoomListener) Stop() {
	rl.room.setPeers(nil)
}

func (rl *RoomListener) listen(decoder *json.Decoder) {
	defer rl.stream.Close()
	for {
		var msg listenerMessage
		if err := decoder.Decode(&msg); err != nil {
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

func (rl *RoomListener) onUpdateAllowedPeers(msg listenerMessage) error {
	var update PeerUpdate
	if err := json.Unmarshal(msg.Payload, &update); err != nil {
		log.Printf("room listener payload parse failed: %v", err)
		return err
	}

	peers := make([]peer.ID, 0, len(update.Members))
	for _, member := range update.Members {
		id, err := peer.Decode(member.PeerID)
		if err != nil {
			continue
		}
		peers = append(peers, id)
	}
	rl.room.setPeers(peers)

	if rl.signals != nil && rl.signals.OnUpdatedAllowedPeers != nil {
		rl.signals.OnUpdatedAllowedPeers(peers)
	}
	return nil
}

func (rl *RoomListener) AllowedPeers() []peer.ID {
	return rl.room.GetPeers()
}

func (rl *RoomListener) IsAllowed(peerID peer.ID) bool {
	return rl.room.IsAllowed(peerID)
}
