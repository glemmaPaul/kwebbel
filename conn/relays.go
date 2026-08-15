package conn

import (
	"context"
	"errors"
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

	_, err = client.Reserve(context.Background(), r.host, relayInfo)
	if err != nil {
		return fmt.Errorf("reservation failed: %w", err)
	}

	r.addRelay(relayInfo)
	return nil
}

func (r *RelayManager) Disconnect(ctx context.Context, relayID peer.ID) error {
	r.mu.Lock()
	_, ok := r.relays[relayID]
	r.mu.Unlock()
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

// TryPeer races circuit dials through every available relay and returns as soon
// as one path to target succeeds. Remaining relays keep being tried in the
// background until they finish or ctx is cancelled, so extra working paths can
// still land in the peerstore.
func (r *RelayManager) TryPeer(ctx context.Context, target peer.ID) (peer.AddrInfo, error) {
	if target == r.host.ID() {
		return peer.AddrInfo{}, fmt.Errorf("cannot dial own peer id")
	}

	relays := r.Available()
	type attempt struct {
		relay peer.ID
		info  peer.AddrInfo
	}
	attempts := make([]attempt, 0, len(relays))
	for _, relay := range relays {
		if relay.ID == target {
			continue
		}
		info, err := circuitAddrInfo(relay.ID, target)
		if err != nil {
			continue
		}
		attempts = append(attempts, attempt{relay: relay.ID, info: info})
	}
	if len(attempts) == 0 {
		return peer.AddrInfo{}, fmt.Errorf("no relays available")
	}

	type result struct {
		info peer.AddrInfo
		err  error
	}
	results := make(chan result, len(attempts))
	for _, a := range attempts {
		go func(a attempt) {
			results <- result{info: a.info, err: r.connectViaRelay(ctx, a.relay, a.info)}
		}(a)
	}

	var errs []error
	for i := 0; i < len(attempts); i++ {
		select {
		case <-ctx.Done():
			return peer.AddrInfo{}, ctx.Err()
		case res := <-results:
			if res.err != nil {
				errs = append(errs, res.err)
				continue
			}
			return res.info, nil
		}
	}
	return peer.AddrInfo{}, fmt.Errorf("peer %s unreachable via relays: %w", target, errors.Join(errs...))
}

func (r *RelayManager) connectViaRelay(ctx context.Context, relayID peer.ID, info peer.AddrInfo) error {
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.TempAddrTTL)
	if err := r.host.Connect(ctx, info); err != nil {
		return fmt.Errorf("relay %s: %w", relayID, err)
	}
	r.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
	return nil
}

func circuitAddrInfo(relayID, target peer.ID) (peer.AddrInfo, error) {
	circuit, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s/p2p-circuit", relayID))
	if err != nil {
		return peer.AddrInfo{}, err
	}
	return peer.AddrInfo{
		ID:    target,
		Addrs: []ma.Multiaddr{circuit},
	}, nil
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
