package conn

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
)

func TestTryPeerNoRelays(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })

	rm := NewRelayManager(h)
	target := peer.ID("12D3KooWHnFuap6bMdBCvFNcqjFuvqvDRkGWaoQh69A8Ca1aP4cB")

	_, err = rm.TryPeer(context.Background(), target)
	if err == nil {
		t.Fatal("expected error when no relays are available")
	}
}

func TestTryPeerOwnID(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })

	_, err = NewRelayManager(h).TryPeer(context.Background(), h.ID())
	if err == nil {
		t.Fatal("expected error when trying own peer id")
	}
}

func TestTryPeerViaRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	relayHost, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.ForceReachabilityPublic(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { relayHost.Close() })

	if _, err := relay.New(relayHost); err != nil {
		t.Fatal(err)
	}

	newPrivateHost := func() *RelayManager {
		h, err := libp2p.New(
			libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0", "/p2p-circuit"),
			libp2p.EnableRelay(),
			libp2p.ForceReachabilityPrivate(),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { h.Close() })
		return NewRelayManager(h)
	}

	target := newPrivateHost()
	dialer := newPrivateHost()
	relayInfo := peer.AddrInfo{ID: relayHost.ID(), Addrs: relayHost.Addrs()}

	if err := target.Connect(ctx, relayInfo); err != nil {
		t.Fatalf("target failed to reserve relay: %v", err)
	}
	if err := dialer.Connect(ctx, relayInfo); err != nil {
		t.Fatalf("dialer failed to connect to relay: %v", err)
	}

	info, err := dialer.TryPeer(ctx, target.host.ID())
	if err != nil {
		t.Fatalf("TryPeer: %v", err)
	}
	if info.ID != target.host.ID() {
		t.Fatalf("got peer %s, want %s", info.ID, target.host.ID())
	}

	connectedness := dialer.host.Network().Connectedness(target.host.ID())
	if connectedness != network.Limited && connectedness != network.Connected {
		t.Fatalf("expected a circuit (or upgraded) connection, got %s", connectedness)
	}
}
