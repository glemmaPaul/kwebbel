package transport

import (
	"context"
	"log"

	"github.com/kwebbelkorp/kwebbel/logging"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	// WebRTCSignalProtocol carries SDP/ICE messages over libp2p streams.
	WebRTCSignalProtocol = "/app/kwebbel-webrtc-signal/0.0.1"
)

// WebRTCAudioBridge is the room-scoped façade for peer negotiation and audio.
type WebRTCAudioBridge struct {
	host        host.Host
	connections PeerConnectionManager
	logger      *log.Logger
	closed      chan struct{}
}

func NewWebRTCAudioBridge(
	ctx context.Context,
	h host.Host,
	connections PeerConnectionManager) *WebRTCAudioBridge {
	logger := logging.FromContext(ctx)
	bridge := &WebRTCAudioBridge{
		host:        h,
		connections: connections,
		logger:      logger,
		closed:      make(chan struct{}),
	}

	h.SetStreamHandler(WebRTCSignalProtocol, connections.StreamHandler())
	logger.Printf("bridge initialized, signaling protocol=%s", WebRTCSignalProtocol)
	return bridge
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
