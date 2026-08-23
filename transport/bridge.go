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
	host    host.Host
	tracker *PeerTracker
	logger  *log.Logger
	closed  chan struct{}
}

func NewWebRTCAudioBridge(
	ctx context.Context,
	h host.Host,
	tracker *PeerTracker) *WebRTCAudioBridge {
	logger := logging.FromContext(ctx)
	bridge := &WebRTCAudioBridge{
		host:    h,
		tracker: tracker,
		logger:  logger,
		closed:  make(chan struct{}),
	}

	h.SetStreamHandler(WebRTCSignalProtocol, tracker.StreamHandler())
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
	b.tracker.Close()
}

func (b *WebRTCAudioBridge) logf(format string, args ...any) {
	if b.logger == nil {
		log.Printf("[webrtc] "+format, args...)
		return
	}
	b.logger.Printf("[webrtc] "+format, args...)
}
