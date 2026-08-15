package transport

import (
	"context"
	"log"

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
	negotiator  *WebRTCNegotiator
	logger      *log.Logger
	closed      chan struct{}
}

func NewWebRTCAudioBridge(
	h host.Host,
	room *rooms.Room,
	audioTransport *OpusAudioTransport,
	connections PeerConnectionManager,
) *WebRTCAudioBridge {
	if room == nil {
		panic("transport: WebRTCAudioBridge requires a room")
	}
	if connections == nil {
		panic("transport: WebRTCAudioBridge requires peer connections")
	}

	negotiator := newWebRTCNegotiator(h, room, audioTransport, connections)
	connections.SetNegotiator(negotiator)
	bridge := &WebRTCAudioBridge{
		host:        h,
		room:        room,
		audio:       audioTransport,
		connections: connections,
		negotiator:  negotiator,
		logger:      log.Default(),
		closed:      make(chan struct{}),
	}

	h.SetStreamHandler(WebRTCSignalProtocol, negotiator.handleIncomingSignalStream)
	bridge.logf("bridge initialized, room=%s signaling protocol=%s", room.ID, WebRTCSignalProtocol)
	return bridge
}

func (b *WebRTCAudioBridge) SetLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	b.logger = logger
	b.negotiator.SetLogger(logger)
}

// Dial delegates one guarded negotiation attempt to PeerConnections.
func (b *WebRTCAudioBridge) Dial(ctx context.Context, remote peer.AddrInfo) error {
	return b.connections.Dial(ctx, remote)
}

// SyncRoomPeers synchronizes retry tracking and active sessions with the room.
func (b *WebRTCAudioBridge) SyncRoomPeers() {
	if err := b.connections.SyncPeers(b.room.GetPeers(), b.host.ID()); err != nil {
		b.logf("failed syncing room peers: %v", err)
	}
	b.logf("synced room peers room=%s peer_count=%d", b.room.ID, b.room.GetPeerCount())
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
	b.negotiator.Close()
}

func (b *WebRTCAudioBridge) logf(format string, args ...any) {
	if b.logger == nil {
		log.Printf("[webrtc] "+format, args...)
		return
	}
	b.logger.Printf("[webrtc] "+format, args...)
}
