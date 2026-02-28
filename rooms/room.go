package rooms

import (
	"github.com/libp2p/go-libp2p/core/peer"
)

type Room struct {
	MC           peer.ID
	ID           string
	allowedPeers []peer.ID
}

func (r *Room) AddPeer(peer peer.ID) {
	// Check if peer already exists
	for _, p := range r.allowedPeers {
		if p == peer {
			return
		}
	}
	// Add peer
	r.allowedPeers = append(r.allowedPeers, peer)
}

func (r *Room) RemovePeer(peer peer.ID) {
	for i, p := range r.allowedPeers {
		if p == peer {
			r.allowedPeers = append(r.allowedPeers[:i], r.allowedPeers[i+1:]...)
			break
		}
	}
}

func (r *Room) GetAllowedPeers() []peer.ID {
	return r.allowedPeers
}

func (r *Room) IsPeerAllowed(peer peer.ID) bool {
	for _, p := range r.allowedPeers {
		if p == peer {
			return true
		}
	}
	return false
}
