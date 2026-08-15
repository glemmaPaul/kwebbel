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
	audio       *OpusAudioTransport
	connections PeerConnectionManager
	logger      *log.Logger
	closed      chan struct{}
}

func NewWebRTCAudioBridge(
	ctx context.Context,
	h host.Host,
	room *rooms.Room,
	audioTransport *OpusAudioTransport,
	connections PeerConnectionManager) *WebRTCAudioBridge {
	logger := logging.FromContext(ctx)
	bridge := &WebRTCAudioBridge{
		host:        h,
		room:        room,
		audio:       audioTransport,
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

// StartPublishing forwards 20ms Opus packets to all active WebRTC peers.
func (b *WebRTCAudioBridge) StartPublishing(ctx context.Context, opusPackets <-chan []byte) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.closed:
				return
			case packet, ok := <-opusPackets:
				if !ok {
					return
				}
				if b.audio == nil {
					continue
				}
				if err := b.audio.Publish(packet); err != nil {
					b.logf("failed publishing Opus packet: %v", err)
				}
			}
		}
	}()
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
