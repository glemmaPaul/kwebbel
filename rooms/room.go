package rooms

import (
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/libp2p/go-libp2p/core/peer"
)

type Room struct {
	ID    string
	Name  string
	Peers []peer.ID
	mu    sync.RWMutex
}

func NewRoom(name string) *Room {
	// simple uuid4 for now
	id := uuid.New().String()
	return &Room{
		ID:    id,
		Name:  name,
		mu:    sync.RWMutex{},
		Peers: make([]peer.ID, 0),
	}
}

func (r *Room) AddPeer(peerID peer.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Peers = append(r.Peers, peerID)
}

func (r *Room) RemovePeer(peerID peer.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Peers = slices.DeleteFunc(r.Peers, func(p peer.ID) bool {
		return p == peerID
	})
}

func (r *Room) GetPeers() []peer.ID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Peers
}

func (r *Room) GetPeerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.Peers)
}

func (r *Room) IsAllowed(peerID peer.ID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Contains(r.Peers, peerID)
}
