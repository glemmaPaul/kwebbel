package transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/pion/webrtc/v4"
)

type fakeNegotiator struct {
	mu      sync.Mutex
	err     error
	wait    bool
	calls   int
	started chan peer.ID
	closed  chan peer.ID
	events  chan ConnectionEvent
}

type fakePeerList struct {
	mu      sync.RWMutex
	peers   []peer.ID
	allowed map[peer.ID]struct{}
}

func newFakePeerList(peers ...peer.ID) *fakePeerList {
	allowed := make(map[peer.ID]struct{}, len(peers))
	for _, id := range peers {
		allowed[id] = struct{}{}
	}
	return &fakePeerList{
		peers:   append([]peer.ID(nil), peers...),
		allowed: allowed,
	}
}

func (l *fakePeerList) GetPeers() []peer.ID {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]peer.ID(nil), l.peers...)
}

func (l *fakePeerList) IsAllowed(id peer.ID) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.allowed[id]
	return ok
}

func (l *fakePeerList) disallow(id peer.ID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.allowed, id)
}

func (l *fakePeerList) remove(id peer.ID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.allowed, id)
	peers := l.peers[:0]
	for _, candidate := range l.peers {
		if candidate != id {
			peers = append(peers, candidate)
		}
	}
	l.peers = peers
}

func newFakeNegotiator() *fakeNegotiator {
	return &fakeNegotiator{
		started: make(chan peer.ID, 10),
		closed:  make(chan peer.ID, 10),
		events:  make(chan ConnectionEvent, 10),
	}
}

func (n *fakeNegotiator) Negotiate(ctx context.Context, host host.Host, remote peer.AddrInfo) error {
	n.mu.Lock()
	n.calls++
	wait := n.wait
	err := n.err
	n.mu.Unlock()
	n.started <- remote.ID

	if wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (n *fakeNegotiator) Disconnect(id peer.ID) error {
	n.closed <- id
	return nil
}

func (n *fakeNegotiator) HandleSignalStream(network.Stream) {}

func (n *fakeNegotiator) Events() <-chan ConnectionEvent {
	return n.events
}

func (n *fakeNegotiator) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func TestPeerConnectionsSchedulesAndTracksDialFailure(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	negotiator.err = errors.New("dial failed")
	peerID := peer.ID("peer-a")
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		newFakePeerList(peerID),
		testRetryPolicy(),
	)
	defer connections.Close()

	connections.Track(peer.AddrInfo{ID: peerID})
	awaitDial(t, negotiator, peerID)
	awaitStatus(t, connections, peerID, func(status PeerConnectionStatus) bool {
		return !status.Dialing && status.RetryCount == 1 && status.LastError == "dial failed"
	})

	negotiator.events <- ConnectionEvent{
		Kind:   ConnectionEventStateChanged,
		PeerID: peerID,
		State:  webrtc.PeerConnectionStateConnected,
	}
	awaitStatus(t, connections, peerID, func(status PeerConnectionStatus) bool {
		return status.Connected && status.RetryCount == 0 && status.LastError == ""
	})
}

func TestPeerConnectionsDoesNotRedialConnectingPeer(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	peerID := peer.ID("peer-a")
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		newFakePeerList(peerID),
		testRetryPolicy(),
	)
	defer connections.Close()

	connections.Track(peer.AddrInfo{ID: peerID})
	awaitDial(t, negotiator, peerID)
	negotiator.events <- ConnectionEvent{
		Kind:   ConnectionEventStateChanged,
		PeerID: peerID,
		State:  webrtc.PeerConnectionStateConnecting,
	}
	awaitStatus(t, connections, peerID, func(status PeerConnectionStatus) bool {
		return status.Connecting
	})

	time.Sleep(20 * time.Millisecond)
	if got := negotiator.callCount(); got != 1 {
		t.Fatalf("dial count = %d, want 1 while connecting", got)
	}
}

func TestPeerConnectionsDialPreventsDuplicatesAndTimesOut(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	negotiator.wait = true
	remote := peer.AddrInfo{ID: peer.ID("peer-a")}
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		newFakePeerList(remote.ID),
		testRetryPolicy(),
	)
	defer connections.Close()

	result := make(chan error, 1)
	go func() {
		result <- connections.Dial(context.Background(), remote)
	}()
	awaitDial(t, negotiator, remote.ID)

	if err := connections.Dial(context.Background(), remote); err != nil {
		t.Fatalf("duplicate dial returned error: %v", err)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial error = %v, want deadline exceeded", err)
	}
	if got := negotiator.callCount(); got != 1 {
		t.Fatalf("dial count = %d, want 1", got)
	}
}

func TestPeerConnectionsRejectsDisallowedDial(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	remote := peer.AddrInfo{ID: peer.ID("peer-a")}
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		newFakePeerList(),
		testRetryPolicy(),
	)
	defer connections.Close()

	if err := connections.Dial(context.Background(), remote); err == nil {
		t.Fatal("disallowed dial should return an error")
	}
	if connections.IsTracked(remote.ID) {
		t.Fatal("disallowed peer should not be tracked")
	}
	if got := negotiator.callCount(); got != 0 {
		t.Fatalf("negotiator dial count = %d, want 0", got)
	}
}

func TestPeerConnectionsReconcilesPeerList(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	self := peer.ID("self")
	kept := peer.ID("kept")
	removed := peer.ID("removed")
	denied := peer.ID("denied")
	peerList := newFakePeerList(self, kept, removed, denied)
	peerList.disallow(denied)
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		peerList,
		testRetryPolicy(),
	)
	defer connections.Close()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if connections.IsTracked(kept) && connections.IsTracked(removed) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !connections.IsTracked(removed) {
		t.Fatal("initial room member should be tracked")
	}

	peerList.remove(removed)
	deadline = time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !connections.IsTracked(removed) {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if connections.IsTracked(self) {
		t.Fatal("self peer should not be tracked")
	}
	if !connections.IsTracked(kept) {
		t.Fatal("room member should be tracked")
	}
	if connections.IsTracked(removed) {
		t.Fatal("peer outside room should be untracked")
	}
	if connections.IsTracked(denied) {
		t.Fatal("peer rejected by IsAllowed should not be tracked")
	}
	select {
	case got := <-negotiator.closed:
		if got != removed {
			t.Fatalf("disconnected peer = %s, want %s", got, removed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for removed peer disconnect")
	}
}

func TestPeerConnectionsTracksDiscoveredPeerEvent(t *testing.T) {
	fakeMock := mocknet.New()
	negotiator := newFakeNegotiator()
	peerID := peer.ID("incoming")
	connections := NewPeerConnections(
		context.Background(),
		fakeMock.Host(peer.ID("self")),
		negotiator,
		newFakePeerList(peerID),
		DefaultRetryPolicy(),
	)
	defer connections.Close()

	negotiator.events <- ConnectionEvent{
		Kind:   ConnectionEventPeerDiscovered,
		PeerID: peerID,
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if connections.IsTracked(peerID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("discovered peer was not tracked")
}

func testRetryPolicy() RetryPolicy {
	return RetryPolicy{
		InitialBackoff: 2 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		RetryInterval:  2 * time.Millisecond,
		DialTimeout:    10 * time.Millisecond,
		ConnectTimeout: 5 * time.Millisecond,
	}
}

func awaitDial(t *testing.T, negotiator *fakeNegotiator, want peer.ID) {
	t.Helper()
	select {
	case got := <-negotiator.started:
		if got != want {
			t.Fatalf("dial peer = %s, want %s", got, want)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for dial")
	}
}

func awaitStatus(
	t *testing.T,
	connections *PeerConnections,
	peerID peer.ID,
	ready func(PeerConnectionStatus) bool,
) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if status, ok := connections.Status(peerID); ok && ready(status) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	status, _ := connections.Status(peerID)
	t.Fatalf("timed out waiting for status, last value: %+v", status)
}
