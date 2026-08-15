package conn

import (
	"context"
	"fmt"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

type RelayInfo struct {
	peerID    peer.ID
	peerInfo  peer.AddrInfo
	connected bool
}

/*
Handles the reservation and connection to the relay.
*/
type RelayManager struct {
	relays map[peer.ID]RelayInfo
	host   host.Host
	mu     sync.Mutex
}

func NewRelayManager(host host.Host) *RelayManager {
	return &RelayManager{
		host:   host,
		relays: make(map[peer.ID]RelayInfo),
	}
}

func (r *RelayManager) Connect(ctx context.Context, relayInfo peer.AddrInfo) error {
	if r.isConnected(relayInfo.ID) {
		return nil
	}
	err := r.host.Connect(ctx, relayInfo)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}

	r.host.Peerstore().AddAddrs(relayInfo.ID, relayInfo.Addrs, peerstore.PermanentAddrTTL)

	// 1. Request the reservation
	_, err = client.Reserve(context.Background(), r.host, relayInfo)
	if err != nil {
		return fmt.Errorf("reservation failed: %w", err)
	}

	r.addRelay(relayInfo)
	return nil
}

func (r *RelayManager) Disconnect(ctx context.Context, relayID peer.ID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.relays[relayID]
	if !ok {
		return fmt.Errorf("relay not found")
	}
	err := r.host.Network().ClosePeer(relayID)
	if err != nil {
		return fmt.Errorf("failed to disconnect from relay: %w", err)
	}
	r.removeRelay(relayID)
	return nil
}

func (r *RelayManager) Available() []peer.AddrInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	available := make([]peer.AddrInfo, 0, len(r.relays))
	for _, relay := range r.relays {
		if relay.connected {
			available = append(available, relay.peerInfo)
		}
	}
	return available
}

func (r *RelayManager) removeRelay(relayID peer.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.relays, relayID)
}

func (r *RelayManager) addRelay(relayInfo peer.AddrInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.relays[relayInfo.ID] = RelayInfo{
		peerID:    relayInfo.ID,
		peerInfo:  relayInfo,
		connected: true,
	}
}

func (r *RelayManager) isConnected(relayID peer.ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.relays[relayID].connected
}

// RelayAddrInfo is a helper function to create addrinfo of transports used
func RelayAddrInfo(relayID peer.ID, relayIp string) peer.AddrInfo {
	return peer.AddrInfo{
		ID: relayID,
		Addrs: []ma.Multiaddr{
			ma.StringCast(fmt.Sprintf("/ip4/%s/udp/4242/quic-v1", relayIp)),
			ma.StringCast(fmt.Sprintf("/ip4/%s/tcp/4242", relayIp)),
		},
	}
}
