package transport

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/webrtc/v4"
)

type fakeNegotiator struct {
	mu      sync.Mutex
	err     error
	wait    bool
	calls   int
	started chan peer.ID
	closed  chan peer.ID
}

func newFakeNegotiator() *fakeNegotiator {
	return &fakeNegotiator{
		started: make(chan peer.ID, 10),
		closed:  make(chan peer.ID, 10),
	}
}

func (n *fakeNegotiator) Dial(ctx context.Context, remote peer.AddrInfo) error {
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

func (n *fakeNegotiator) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func TestPeerConnectionsSchedulesAndTracksDialFailure(t *testing.T) {
	connections := NewPeerConnections(testRetryPolicy())
	defer connections.Close()
	negotiator := newFakeNegotiator()
	negotiator.err = errors.New("dial failed")
	connections.SetNegotiator(negotiator)

	peerID := peer.ID("peer-a")
	connections.Track(peer.AddrInfo{ID: peerID})
	awaitDial(t, negotiator, peerID)
	awaitStatus(t, connections, peerID, func(status PeerConnectionStatus) bool {
		return !status.Dialing && status.RetryCount == 1 && status.LastError == "dial failed"
	})

	connections.UpdateState(peerID, webrtc.PeerConnectionStateConnected)
	status, _ := connections.Status(peerID)
	if !status.Connected || status.RetryCount != 0 || status.LastError != "" {
		t.Fatalf("unexpected connected status: %+v", status)
	}
}

func TestPeerConnectionsDoesNotRedialConnectingPeer(t *testing.T) {
	connections := NewPeerConnections(testRetryPolicy())
	defer connections.Close()
	negotiator := newFakeNegotiator()
	connections.SetNegotiator(negotiator)

	peerID := peer.ID("peer-a")
	connections.Track(peer.AddrInfo{ID: peerID})
	awaitDial(t, negotiator, peerID)
	connections.UpdateState(peerID, webrtc.PeerConnectionStateConnecting)

	time.Sleep(20 * time.Millisecond)
	if got := negotiator.callCount(); got != 1 {
		t.Fatalf("dial count = %d, want 1 while connecting", got)
	}
}

func TestPeerConnectionsDialPreventsDuplicatesAndTimesOut(t *testing.T) {
	connections := NewPeerConnections(testRetryPolicy())
	defer connections.Close()
	negotiator := newFakeNegotiator()
	negotiator.wait = true
	connections.SetNegotiator(negotiator)

	remote := peer.AddrInfo{ID: peer.ID("peer-a")}
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

func TestPeerConnectionsSyncPeers(t *testing.T) {
	connections := NewPeerConnections(DefaultRetryPolicy())
	defer connections.Close()
	negotiator := newFakeNegotiator()
	connections.SetNegotiator(negotiator)

	self := peer.ID("self")
	kept := peer.ID("kept")
	removed := peer.ID("removed")
	connections.Track(peer.AddrInfo{ID: removed})
	if err := connections.SyncPeers([]peer.ID{self, kept}, self); err != nil {
		t.Fatalf("sync peers: %v", err)
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
	select {
	case got := <-negotiator.closed:
		if got != removed {
			t.Fatalf("disconnected peer = %s, want %s", got, removed)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for removed peer disconnect")
	}
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

var _ Negotiator = (*fakeNegotiator)(nil)
var _ PeerConnectionManager = (*PeerConnections)(nil)
