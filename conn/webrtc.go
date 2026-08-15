package conn

import (
	"context"
	"log"

	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/kwebbelkorp/kwebbel/transport"
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
	connections transport.PeerConnectionManager
	negotiator  *WebRTCNegotiator
	logger      *log.Logger
	closed      chan struct{}
}

func NewWebRTCAudioBridge(
	h host.Host,
	room *rooms.Room,
	audioTransport *transport.OpusAudioTransport,
	connections transport.PeerConnectionManager,
) *WebRTCAudioBridge {
	if room == nil {
		panic("conn: WebRTCAudioBridge requires a room")
	}
	if connections == nil {
		panic("conn: WebRTCAudioBridge requires peer connections")
	}

	negotiator := newWebRTCNegotiator(h, room, audioTransport, connections)
	connections.SetNegotiator(negotiator)
	bridge := &WebRTCAudioBridge{
		host:        h,
		room:        room,
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

// TrackRoomPeers synchronizes retry tracking and active sessions with the room.
func (b *WebRTCAudioBridge) TrackRoomPeers() {
	b.connections.SyncMembers(b.room.GetPeers(), b.host.ID())
	b.negotiator.RemovePeersOutsideRoom()
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
				b.negotiator.Publish(packet)
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
