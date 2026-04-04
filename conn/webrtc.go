package conn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	// WebRTCSignalProtocol carries SDP/ICE messages over libp2p streams.
	WebRTCSignalProtocol = "/app/kwebbel-webrtc-signal/0.0.1"
)

// WebRTCSignalMessage is exchanged over libp2p to establish a WebRTC session.
type WebRTCSignalMessage struct {
	Type      string                     `json:"type"` // offer, answer, candidate
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

// WebRTCAudioBridge demonstrates how to:
// 1) use libp2p for peer discovery/signaling,
// 2) use Pion/WebRTC for audio transport,
// 3) feed Opus frames from audio egress into WebRTC tracks.
type WebRTCAudioBridge struct {
	host   host.Host
	api    *webrtc.API
	logger *log.Logger
	mixer  *audio.Mixer
	mu     sync.RWMutex
	peers  map[peer.ID]*webrtcPeerState
	closed chan struct{}
}
type webrtcPeerState struct {
	pc               *webrtc.PeerConnection
	opusTrack        *webrtc.TrackLocalStaticSample
	encoder          *json.Encoder
	encoderMu        sync.Mutex
	trackMu          sync.Mutex
	publishedPackets uint64
}

func NewWebRTCAudioBridge(h host.Host, mixer *audio.Mixer) *WebRTCAudioBridge {
	api := newDefaultWebRTCAPI()
	b := &WebRTCAudioBridge{
		host:   h,
		api:    api,
		logger: log.Default(),
		mixer:  mixer,
		peers:  make(map[peer.ID]*webrtcPeerState),
		closed: make(chan struct{}),
	}
	// Accept incoming signaling requests from peers.
	h.SetStreamHandler(WebRTCSignalProtocol, b.handleIncomingSignalStream)
	b.logf("bridge initialized, signaling protocol=%s", WebRTCSignalProtocol)
	return b
}

func (b *WebRTCAudioBridge) SetLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	b.logger = logger
}

// StartPublishing forwards 20ms Opus packets from AudioEgress.Output to all
// active WebRTC peers. This expects 48kHz mono Opus frames (960-sample frames).
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
				b.publishFrame(packet)
			}
		}
	}()
}

// DialAndNegotiate opens a libp2p stream to exchange SDP/ICE and starts
// the offer/answer flow for one peer.
func (b *WebRTCAudioBridge) DialAndNegotiate(ctx context.Context, remote peer.AddrInfo) error {
	b.logf("dial and negotiate start with peer=%s", remote.ID)
	b.host.Peerstore().AddAddrs(remote.ID, remote.Addrs, peerstore.PermanentAddrTTL)
	if err := b.host.Connect(ctx, remote); err != nil {
		return err
	}
	stream, err := b.host.NewStream(
		network.WithAllowLimitedConn(ctx, string(WebRTCSignalProtocol)),
		remote.ID,
		WebRTCSignalProtocol,
	)
	if err != nil {
		return err
	}
	state, err := b.createPeerState(remote.ID, stream)
	if err != nil {
		_ = stream.Close()
		return err
	}
	if err := state.ensureAudioSender(); err != nil {
		return err
	}
	offer, err := state.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := state.pc.SetLocalDescription(offer); err != nil {
		return err
	}
	if err := state.send(WebRTCSignalMessage{
		Type: "offer",
		SDP:  state.pc.LocalDescription(),
	}); err != nil {
		return err
	}
	b.logf("sent offer to peer=%s", remote.ID)
	go b.consumeSignalMessages(remote.ID, stream)
	return nil
}
func (b *WebRTCAudioBridge) handleIncomingSignalStream(stream network.Stream) {
	remoteID := stream.Conn().RemotePeer()
	b.logf("incoming signaling stream from peer=%s", remoteID)
	if _, err := b.createPeerState(remoteID, stream); err != nil {
		b.logf("webrtc peer setup failed for %s: %v", remoteID, err)
		_ = stream.Close()
		return
	}
	go b.consumeSignalMessages(remoteID, stream)
}
func (b *WebRTCAudioBridge) consumeSignalMessages(remoteID peer.ID, stream network.Stream) {
	defer stream.Close()
	decoder := json.NewDecoder(stream)
	for {
		var msg WebRTCSignalMessage
		if err := decoder.Decode(&msg); err != nil {
			b.logf("signaling stream closed for peer=%s: %v", remoteID, err)
			return
		}
		b.logf("received signaling message type=%s peer=%s", msg.Type, remoteID)
		if err := b.applySignalMessage(remoteID, msg); err != nil {
			b.logf("webrtc signaling error (%s): %v", remoteID, err)
		}
	}
}
func (b *WebRTCAudioBridge) applySignalMessage(remoteID peer.ID, msg WebRTCSignalMessage) error {
	state, ok := b.getPeerState(remoteID)
	if !ok {
		return errors.New("peer state not found")
	}
	switch msg.Type {
	case "offer":
		if msg.SDP == nil {
			return errors.New("missing offer sdp")
		}
		if err := state.pc.SetRemoteDescription(*msg.SDP); err != nil {
			return err
		}
		b.logf("applied remote offer peer=%s", remoteID)
		// Attach local audio after applying the remote offer so Pion can bind
		// it to the offered transceiver and avoid codec mismatch errors.
		if err := state.ensureAudioSender(); err != nil {
			return err
		}
		answer, err := state.pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		if err := state.pc.SetLocalDescription(answer); err != nil {
			return err
		}
		return state.send(WebRTCSignalMessage{
			Type: "answer",
			SDP:  state.pc.LocalDescription(),
		})
	case "answer":
		if msg.SDP == nil {
			return errors.New("missing answer sdp")
		}
		if err := state.pc.SetRemoteDescription(*msg.SDP); err != nil {
			return err
		}
		b.logf("applied remote answer peer=%s", remoteID)
		return nil
	case "candidate":
		if msg.Candidate == nil {
			return errors.New("missing candidate")
		}
		b.logf("adding remote ICE candidate peer=%s", remoteID)
		return state.pc.AddICECandidate(*msg.Candidate)
	case "error":
		return errors.New(msg.Error)
	default:
		return nil
	}
}
func (b *WebRTCAudioBridge) createPeerState(remoteID peer.ID, stream network.Stream) (*webrtcPeerState, error) {
	if existing, ok := b.getPeerState(remoteID); ok {
		return existing, nil
	}
	pc, err := b.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	state := &webrtcPeerState{
		pc:      pc,
		encoder: json.NewEncoder(stream),
	}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			b.logf("finished gathering local ICE candidates peer=%s", remoteID)
			return
		}
		if err := state.send(WebRTCSignalMessage{
			Type:      "candidate",
			Candidate: ptr(c.ToJSON()),
		}); err != nil {
			b.logf("failed to send candidate (%s): %v", remoteID, err)
		}
	})
	pc.OnICEConnectionStateChange(func(cs webrtc.ICEConnectionState) {
		b.logf("ice connection state peer=%s state=%s", remoteID, cs.String())
	})
	pc.OnSignalingStateChange(func(ss webrtc.SignalingState) {
		b.logf("signaling state peer=%s state=%s", remoteID, ss.String())
	})
	pc.OnConnectionStateChange(func(cs webrtc.PeerConnectionState) {
		b.logf("peer connection state peer=%s state=%s", remoteID, cs.String())
		if cs == webrtc.PeerConnectionStateFailed ||
			cs == webrtc.PeerConnectionStateDisconnected ||
			cs == webrtc.PeerConnectionStateClosed {
			b.removePeer(remoteID)
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		_ = receiver
		b.logf("remote track received peer=%s kind=%s codec=%s", remoteID, track.Kind().String(), track.Codec().MimeType)
		go b.readRemoteTrack(remoteID, track)
	})
	b.mu.Lock()
	b.peers[remoteID] = state
	b.mu.Unlock()
	b.logf("peer state created for peer=%s", remoteID)
	return state, nil
}
func (b *WebRTCAudioBridge) publishFrame(opusPacket []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for id, p := range b.peers {
		if p.opusTrack == nil {
			continue
		}
		err := p.opusTrack.WriteSample(media.Sample{
			Data:     opusPacket,
			Duration: 20 * time.Millisecond,
		})
		if err != nil {
			b.logf("failed writing opus frame to %s: %v", id, err)
			continue
		}

		p.publishedPackets++
		if p.publishedPackets == 1 || p.publishedPackets%200 == 0 {
			b.logf("published opus packets peer=%s count=%d", id, p.publishedPackets)
		}
	}
}
func (b *WebRTCAudioBridge) Close() {
	select {
	case <-b.closed:
		return
	default:
		close(b.closed)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, p := range b.peers {
		if err := p.pc.Close(); err != nil {
			b.logf("failed closing peer connection (%s): %v", id, err)
		}
	}
	b.peers = map[peer.ID]*webrtcPeerState{}
}
func (b *WebRTCAudioBridge) getPeerState(remoteID peer.ID) (*webrtcPeerState, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	state, ok := b.peers[remoteID]
	return state, ok
}
func (b *WebRTCAudioBridge) removePeer(remoteID peer.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, ok := b.peers[remoteID]
	if !ok {
		return
	}
	_ = state.pc.Close()
	delete(b.peers, remoteID)
	b.logf("peer state removed peer=%s", remoteID)
}
func (p *webrtcPeerState) send(msg WebRTCSignalMessage) error {
	p.encoderMu.Lock()
	defer p.encoderMu.Unlock()
	return p.encoder.Encode(msg)
}

func (p *webrtcPeerState) ensureAudioSender() error {
	p.trackMu.Lock()
	defer p.trackMu.Unlock()

	if p.opusTrack != nil {
		return nil
	}

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			// WebRTC Opus is negotiated as 2 channels in SDP by default.
			// The actual encoded frames can still contain mono audio.
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio",
		"kwebbel",
	)
	if err != nil {
		return err
	}

	if _, err := p.pc.AddTrack(track); err != nil {
		return err
	}

	p.opusTrack = track
	return nil
}
func ptr[T any](v T) *T {
	return &v
}

func newDefaultWebRTCAPI() *webrtc.API {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		log.Printf("failed registering default codecs, falling back to package API: %v", err)
		return webrtc.NewAPI()
	}

	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		log.Printf("failed registering default interceptors, falling back to package API: %v", err)
		return webrtc.NewAPI()
	}

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)
}

func (b *WebRTCAudioBridge) logf(format string, args ...any) {
	if b.logger == nil {
		log.Printf("[webrtc] "+format, args...)
		return
	}
	b.logger.Printf("[webrtc] "+format, args...)
}

func (b *WebRTCAudioBridge) readRemoteTrack(remoteID peer.ID, track *webrtc.TrackRemote) {
	if b.mixer == nil {
		b.logf("no mixer configured; dropping incoming track peer=%s", remoteID)
		return
	}

	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	decoder, err := opus.NewDecoder(48000, 1)
	if err != nil {
		b.logf("failed to create opus decoder peer=%s: %v", remoteID, err)
		return
	}

	pcmBuffer := make([]int16, 1920)
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				b.logf("track read ended peer=%s: %v", remoteID, err)
			}
			return
		}
		if len(packet.Payload) == 0 {
			continue
		}

		n, err := decoder.Decode(packet.Payload, pcmBuffer)
		if err != nil {
			b.logf("opus decode error peer=%s: %v", remoteID, err)
			continue
		}
		if n <= 0 {
			continue
		}

		samples := make([]int16, n)
		copy(samples, pcmBuffer[:n])
		b.mixer.PushAudio(remoteID.String(), samples)
	}
}
