package transport

import (
	"context"
	"log"

	"github.com/kwebbelkorp/kwebbel/logging"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// WebRTCSignalProtocol carries SDP/ICE messages over libp2p streams.
	WebRTCSignalProtocol = "/app/kwebbel-webrtc-signal/0.0.1"
)

// WebRTCAudioBridge is the room-scoped façade for peer negotiation and audio.
type WebRTCAudioBridge struct {
	host        host.Host
	room        *rooms.Room
	connections PeerConnectionManager
	logger      *log.Logger
	closed      chan struct{}
}

func NewWebRTCAudioBridge(
	ctx context.Context,
	h host.Host,
	room *rooms.Room,
	connections PeerConnectionManager) *WebRTCAudioBridge {
	logger := logging.FromContext(ctx)
	bridge := &WebRTCAudioBridge{
		host:        h,
		room:        room,
		connections: connections,
		logger:      logger,
		closed:      make(chan struct{}),
	}

	h.SetStreamHandler(WebRTCSignalProtocol, connections.StreamHandler())
	logger.Printf("bridge initialized, room=%s signaling p	rotocol=%s", room.ID, WebRTCSignalProtocol)
	return bridge
}

// Dial delegates one guarded negotiation attempt to PeerConnections.
func (b *WebRTCAudioBridge) Dial(ctx context.Context, remote peer.AddrInfo) error {
	return b.connections.Dial(ctx, remote)
}

// SyncRoomPeers synchronizes retry tracking and active sessions with the room.
func (b *WebRTCAudioBridge) SyncRoomPeers() {
	if err := b.connections.SyncPeers(b.room.GetPeers(), b.host.ID()); err != nil {
		b.logger.Printf("failed syncing room peers: %v", err)
	}
	b.logger.Printf("synced room peers room=%s peer_count=%d", b.room.ID, b.room.GetPeerCount())
}

func (b *WebRTCAudioBridge) Close() {
	select {
	case <-b.closed:
		return
	default:
		close(b.closed)
	}
	b.connections.Close()
}

func (b *WebRTCAudioBridge) logf(format string, args ...any) {
	if b.logger == nil {
		log.Printf("[webrtc] "+format, args...)
		return
	}
	b.logger.Printf("[webrtc] "+format, args...)
}
