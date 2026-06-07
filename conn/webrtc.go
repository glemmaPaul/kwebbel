package conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"gopkg.in/hraban/opus.v2"
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

	allowMu         sync.RWMutex
	allowedPeerSet  map[peer.ID]struct{}
	allowListActive bool

	managedMu   sync.RWMutex
	managedPeer map[peer.ID]*managedPeerState
	retryPolicy RetryPolicy
}
type webrtcPeerState struct {
	pc               *webrtc.PeerConnection
	opusTrack        *webrtc.TrackLocalStaticSample
	encoder          *json.Encoder
	encoderMu        sync.Mutex
	trackMu          sync.Mutex
	publishedPackets uint64
}

type RetryPolicy struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RetryInterval  time.Duration
	DialTimeout    time.Duration
	ConnectTimeout time.Duration
}

type managedPeerState struct {
	info            peer.AddrInfo
	connected       bool
	dialing         bool
	retryCount      int
	nextAttempt     time.Time
	lastError       string
	lastConnectedAt time.Time
}

type PeerConnectionStatus struct {
	PeerID          peer.ID
	Connected       bool
	Dialing         bool
	RetryCount      int
	NextAttempt     time.Time
	LastError       string
	LastConnectedAt time.Time
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

		allowedPeerSet: make(map[peer.ID]struct{}),
		managedPeer:    make(map[peer.ID]*managedPeerState),
		retryPolicy: RetryPolicy{
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     30 * time.Second,
			RetryInterval:  1 * time.Second,
			DialTimeout:    8 * time.Second,
			ConnectTimeout: 6 * time.Second,
		},
	}
	// Accept incoming signaling requests from peers.
	h.SetStreamHandler(WebRTCSignalProtocol, b.handleIncomingSignalStream)
	b.logf("bridge initialized, signaling protocol=%s", WebRTCSignalProtocol)
	go b.reconcileManagedPeers()
	return b
}

func (b *WebRTCAudioBridge) SetLogger(logger *log.Logger) {
	if logger == nil {
		return
	}
	b.logger = logger
}

func (b *WebRTCAudioBridge) TrackAllowedPeers() {
	b.allowMu.RLock()
	ids := make([]peer.ID, 0, len(b.allowedPeerSet))
	for id := range b.allowedPeerSet {
		ids = append(ids, id)
	}
	b.allowMu.RUnlock()

	for _, id := range ids {
		if id == b.host.ID() {
			continue
		}
		b.TrackPeer(peer.AddrInfo{ID: id})
	}
}

// SetAllowedPeers replaces the active allowlist.
// When called, incoming/outgoing signaling is restricted to this set.
func (b *WebRTCAudioBridge) SetAllowedPeers(peers []peer.ID) {
	next := make(map[peer.ID]struct{}, len(peers))
	for _, id := range peers {
		next[id] = struct{}{}
	}

	b.allowMu.Lock()
	b.allowedPeerSet = next
	b.allowListActive = true
	b.allowMu.Unlock()

	b.logf("updated allowlist peer_count=%d", len(peers))
}

// AllowPeer adds one peer to the allowlist and activates filtering.
func (b *WebRTCAudioBridge) AllowPeer(id peer.ID) {
	b.allowMu.Lock()
	b.allowedPeerSet[id] = struct{}{}
	b.allowListActive = true
	b.allowMu.Unlock()
}

// TrackPeer ensures the bridge keeps this peer connected, retrying with backoff.
func (b *WebRTCAudioBridge) TrackPeer(remote peer.AddrInfo) {
	if remote.ID == b.host.ID() {
		b.logf("ignoring self peer in TrackPeer peer=%s", remote.ID)
		return
	}

	b.managedMu.Lock()
	defer b.managedMu.Unlock()

	if existing, ok := b.managedPeer[remote.ID]; ok {
		existing.info = remote
		if existing.nextAttempt.IsZero() {
			existing.nextAttempt = time.Now()
		}
		return
	}

	b.managedPeer[remote.ID] = &managedPeerState{
		info:        remote,
		nextAttempt: time.Now(),
	}
	b.logf("tracking peer=%s for connection retries", remote.ID)
}

func (b *WebRTCAudioBridge) UntrackPeer(id peer.ID) {
	b.managedMu.Lock()
	delete(b.managedPeer, id)
	b.managedMu.Unlock()
}

func (b *WebRTCAudioBridge) PeerStatus(id peer.ID) (PeerConnectionStatus, bool) {
	b.managedMu.RLock()
	defer b.managedMu.RUnlock()

	mp, ok := b.managedPeer[id]
	if !ok {
		return PeerConnectionStatus{}, false
	}
	return PeerConnectionStatus{
		PeerID:          id,
		Connected:       mp.connected,
		Dialing:         mp.dialing,
		RetryCount:      mp.retryCount,
		NextAttempt:     mp.nextAttempt,
		LastError:       mp.lastError,
		LastConnectedAt: mp.lastConnectedAt,
	}, true
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
	if remote.ID == b.host.ID() {
		return errors.New("refusing to dial self")
	}
	if !b.isPeerAllowed(remote.ID) {
		return fmt.Errorf("peer %s not in allowlist", remote.ID)
	}
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
	if !b.isPeerAllowed(remoteID) {
		b.logf("rejected signaling stream from non-allowed peer=%s", remoteID)
		_ = stream.Reset()
		return
	}
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
		b.updateManagedPeerState(remoteID, cs)
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

func (b *WebRTCAudioBridge) reconcileManagedPeers() {
	ticker := time.NewTicker(b.retryPolicy.RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.closed:
			return
		case <-ticker.C:
			now := time.Now()
			toDial := make([]peer.AddrInfo, 0)

			b.managedMu.Lock()
			for id, mp := range b.managedPeer {
				if id == b.host.ID() {
					continue
				}
				if !b.isPeerAllowed(id) {
					continue
				}
				if mp.connected || mp.dialing {
					continue
				}
				if !mp.nextAttempt.IsZero() && now.Before(mp.nextAttempt) {
					continue
				}
				if b.peerConnectionIsAlive(id) {
					continue
				}

				mp.dialing = true
				toDial = append(toDial, mp.info)
			}
			b.managedMu.Unlock()

			for _, info := range toDial {
				go b.dialManagedPeer(info)
			}
		}
	}
}

func (b *WebRTCAudioBridge) dialManagedPeer(info peer.AddrInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), b.retryPolicy.DialTimeout)
	defer cancel()

	err := b.DialAndNegotiate(ctx, info)
	if err != nil {
		b.markManagedDialFailure(info.ID, err)
		return
	}
	b.markManagedDialStarted(info.ID)
}

func (b *WebRTCAudioBridge) markManagedDialStarted(id peer.ID) {
	b.managedMu.Lock()
	defer b.managedMu.Unlock()

	mp, ok := b.managedPeer[id]
	if !ok {
		return
	}
	mp.dialing = false
	// Give handshake some time; if we never reach Connected, try again.
	mp.nextAttempt = time.Now().Add(b.retryPolicy.ConnectTimeout)
	mp.lastError = ""
}

func (b *WebRTCAudioBridge) markManagedDialFailure(id peer.ID, err error) {
	b.managedMu.Lock()
	defer b.managedMu.Unlock()

	mp, ok := b.managedPeer[id]
	if !ok {
		return
	}
	mp.dialing = false
	mp.connected = false
	mp.retryCount++
	mp.lastError = err.Error()
	backoff := b.retryBackoff(mp.retryCount)
	mp.nextAttempt = time.Now().Add(backoff)
	b.logf("dial failed peer=%s retries=%d next_attempt_in=%s err=%v", id, mp.retryCount, backoff, err)
}

func (b *WebRTCAudioBridge) updateManagedPeerState(id peer.ID, state webrtc.PeerConnectionState) {
	b.managedMu.Lock()
	defer b.managedMu.Unlock()

	mp, ok := b.managedPeer[id]
	if !ok {
		return
	}

	switch state {
	case webrtc.PeerConnectionStateConnected:
		mp.connected = true
		mp.dialing = false
		mp.retryCount = 0
		mp.lastError = ""
		mp.lastConnectedAt = time.Now()
		mp.nextAttempt = time.Time{}
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected:
		mp.connected = false
		mp.dialing = false
		if mp.nextAttempt.IsZero() || mp.nextAttempt.After(time.Now().Add(b.retryPolicy.InitialBackoff)) {
			mp.nextAttempt = time.Now().Add(b.retryPolicy.InitialBackoff)
		}
	case webrtc.PeerConnectionStateClosed:
		mp.connected = false
		mp.dialing = false
		mp.nextAttempt = time.Now().Add(b.retryPolicy.InitialBackoff)
	}
}

func (b *WebRTCAudioBridge) retryBackoff(retries int) time.Duration {
	if retries <= 0 {
		return b.retryPolicy.InitialBackoff
	}
	backoff := b.retryPolicy.InitialBackoff
	for i := 1; i < retries; i++ {
		backoff *= 2
		if backoff >= b.retryPolicy.MaxBackoff {
			return b.retryPolicy.MaxBackoff
		}
	}
	return backoff
}

func (b *WebRTCAudioBridge) peerConnectionIsAlive(id peer.ID) bool {
	state, ok := b.getPeerState(id)
	if !ok || state == nil || state.pc == nil {
		return false
	}
	cs := state.pc.ConnectionState()
	return cs == webrtc.PeerConnectionStateConnecting || cs == webrtc.PeerConnectionStateConnected
}

func (b *WebRTCAudioBridge) isPeerAllowed(id peer.ID) bool {
	b.allowMu.RLock()
	defer b.allowMu.RUnlock()

	if !b.allowListActive {
		return true
	}
	_, ok := b.allowedPeerSet[id]
	return ok
}
