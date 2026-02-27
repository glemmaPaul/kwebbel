package kwebbel

import (
	"github.com/libp2p/go-libp2p/core/peer"
)

type Room struct {
	mc          peer.ID
	id          string
	joinedPeers []peer.ID
}

func (r *Room) AddPeer(peer peer.ID) {
	// Check if peer already exists
	for _, p := range r.joinedPeers {
		if p == peer {
			return
		}
	}
	// Add peer
	r.joinedPeers = append(r.joinedPeers, peer)
}

func (r *Room) RemovePeer(peer peer.ID) {
	for i, p := range r.joinedPeers {
		if p == peer {
			r.joinedPeers = append(r.joinedPeers[:i], r.joinedPeers[i+1:]...)
			break
		}
	}
}
