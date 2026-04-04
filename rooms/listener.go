package rooms

import (
	"log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

type RoomListener struct {
	stream  network.Stream
	signals *RoomListenerSignals
}

type RoomListenerSignals struct {
	OnUpdatedAllowedPeers func(peers []peer.ID)
}

func NewRoomListener(stream network.Stream, signals *RoomListenerSignals) *RoomListener {
	if signals == nil {
		signals = &RoomListenerSignals{}
	}
	return &RoomListener{
		stream:  stream,
		signals: signals,
	}
}

func (rl *RoomListener) Start() {
	go func() {
		for {
			select {
			case <-rl.stream.CloseReason():
				log.Println("Room listener stream closed", rl.stream.CloseReason())
				return
			}
		}
	}()
}
