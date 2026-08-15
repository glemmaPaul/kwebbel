package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

// WebRTCSignalMessage is exchanged over libp2p to establish a WebRTC session.
type WebRTCSignalMessage struct {
	Type      string                     `json:"type"` // offer, answer, candidate
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

type WebRTCNegotiator struct {
	host        host.Host
	api         *webrtc.API
	logger      *log.Logger
	transport   *OpusAudioTransport
	connections PeerConnectionManager
	room        *rooms.Room

	mu    sync.RWMutex
	peers map[peer.ID]*webrtcPeerState
}

type webrtcPeerState struct {
	pc        *webrtc.PeerConnection
	opusTrack *webrtc.TrackLocalStaticSample
	encoder   *json.Encoder
	encoderMu sync.Mutex
	trackMu   sync.Mutex
}

func newWebRTCNegotiator(
	h host.Host,
	room *rooms.Room,
	audioTransport *OpusAudioTransport,
	connections PeerConnectionManager,
) *WebRTCNegotiator {
	return &WebRTCNegotiator{
		host:        h,
		api:         newWebRTCAPI(),
		logger:      log.Default(),
		transport:   audioTransport,
		connections: connections,
		room:        room,
		peers:       make(map[peer.ID]*webrtcPeerState),
	}
}

// Dial opens a libp2p stream and starts the WebRTC offer/answer flow.
func (n *WebRTCNegotiator) Dial(ctx context.Context, remote peer.AddrInfo) error {
	n.logf("dial and negotiate start with peer=%s", remote.ID)
	if remote.ID == n.host.ID() {
		return errors.New("refusing to dial self")
	}
	if !n.isPeerAllowed(remote.ID) {
		return fmt.Errorf("peer %s is not a member of room %s", remote.ID, n.room.ID)
	}
	n.host.Peerstore().AddAddrs(remote.ID, remote.Addrs, peerstore.PermanentAddrTTL)
	if err := n.host.Connect(ctx, remote); err != nil {
		return err
	}
	stream, err := n.host.NewStream(
		network.WithAllowLimitedConn(ctx, string(WebRTCSignalProtocol)),
		remote.ID,
		WebRTCSignalProtocol,
	)
	if err != nil {
		return err
	}
	state, err := n.createPeerState(remote.ID, stream)
	if err != nil {
		_ = stream.Close()
		return err
	}
	if err := n.ensureAudioSender(remote.ID, state); err != nil {
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
	n.logf("sent offer to peer=%s", remote.ID)
	go n.consumeSignalMessages(remote.ID, stream)
	return nil
}

func (n *WebRTCNegotiator) handleIncomingSignalStream(stream network.Stream) {
	remoteID := stream.Conn().RemotePeer()
	if !n.isPeerAllowed(remoteID) {
		n.logf("rejected signaling stream from non-allowed peer=%s", remoteID)
		_ = stream.Reset()
		return
	}
	n.logf("incoming signaling stream from peer=%s", remoteID)
	if _, err := n.createPeerState(remoteID, stream); err != nil {
		n.logf("webrtc peer setup failed for %s: %v", remoteID, err)
		_ = stream.Close()
		return
	}
	go n.consumeSignalMessages(remoteID, stream)
}

func (n *WebRTCNegotiator) consumeSignalMessages(remoteID peer.ID, stream network.Stream) {
	defer stream.Close()
	decoder := json.NewDecoder(stream)
	for {
		var msg WebRTCSignalMessage
		if err := decoder.Decode(&msg); err != nil {
			n.logf("signaling stream closed for peer=%s: %v", remoteID, err)
			return
		}
		n.logf("received signaling message type=%s peer=%s", msg.Type, remoteID)
		if err := n.applySignalMessage(remoteID, msg); err != nil {
			n.logf("webrtc signaling error (%s): %v", remoteID, err)
		}
	}
}

func (n *WebRTCNegotiator) applySignalMessage(remoteID peer.ID, msg WebRTCSignalMessage) error {
	state, ok := n.getPeerState(remoteID)
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
		n.logf("applied remote offer peer=%s", remoteID)
		if err := n.ensureAudioSender(remoteID, state); err != nil {
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
		n.logf("applied remote answer peer=%s", remoteID)
		return nil
	case "candidate":
		if msg.Candidate == nil {
			return errors.New("missing candidate")
		}
		n.logf("adding remote ICE candidate peer=%s", remoteID)
		return state.pc.AddICECandidate(*msg.Candidate)
	case "error":
		return errors.New(msg.Error)
	default:
		return nil
	}
}

func (n *WebRTCNegotiator) createPeerState(remoteID peer.ID, stream network.Stream) (*webrtcPeerState, error) {
	if existing, ok := n.getPeerState(remoteID); ok {
		return existing, nil
	}
	n.connections.Track(peer.AddrInfo{ID: remoteID})
	pc, err := n.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	state := &webrtcPeerState{
		pc:      pc,
		encoder: json.NewEncoder(stream),
	}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			n.logf("finished gathering local ICE candidates peer=%s", remoteID)
			return
		}
		if err := state.send(WebRTCSignalMessage{
			Type:      "candidate",
			Candidate: ptr(c.ToJSON()),
		}); err != nil {
			n.logf("failed to send candidate (%s): %v", remoteID, err)
		}
	})
	pc.OnICEConnectionStateChange(func(cs webrtc.ICEConnectionState) {
		n.logf("ice connection state peer=%s state=%s", remoteID, cs.String())
	})
	pc.OnSignalingStateChange(func(ss webrtc.SignalingState) {
		n.logf("signaling state peer=%s state=%s", remoteID, ss.String())
	})
	pc.OnConnectionStateChange(func(cs webrtc.PeerConnectionState) {
		n.logf("peer connection state peer=%s state=%s", remoteID, cs.String())
		n.connections.UpdateState(remoteID, cs)
		if cs == webrtc.PeerConnectionStateFailed ||
			cs == webrtc.PeerConnectionStateDisconnected ||
			cs == webrtc.PeerConnectionStateClosed {
			_ = n.Disconnect(remoteID)
		}
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		_ = receiver
		n.logf("remote track received peer=%s kind=%s codec=%s", remoteID, track.Kind().String(), track.Codec().MimeType)
		if n.transport == nil {
			n.logf("no Opus transport configured; dropping incoming track peer=%s", remoteID)
			return
		}
		go func() {
			if err := n.transport.ReadTrack(remoteID.String(), track); err != nil {
				n.logf("incoming track ended peer=%s: %v", remoteID, err)
			}
		}()
	})
	n.mu.Lock()
	n.peers[remoteID] = state
	n.mu.Unlock()
	n.logf("peer state created for peer=%s", remoteID)
	return state, nil
}

func (n *WebRTCNegotiator) ensureAudioSender(remoteID peer.ID, state *webrtcPeerState) error {
	track, err := state.ensureAudioSender()
	if err != nil {
		return err
	}
	if n.transport != nil {
		n.transport.AddTrack(remoteID.String(), track)
	}
	return nil
}

func (n *WebRTCNegotiator) Close() {
	n.mu.Lock()
	peers := n.peers
	n.peers = map[peer.ID]*webrtcPeerState{}
	n.mu.Unlock()

	for id, state := range peers {
		if n.transport != nil {
			n.transport.RemoveTrack(id.String())
		}
		if err := state.pc.Close(); err != nil {
			n.logf("failed closing peer connection (%s): %v", id, err)
		}
	}
}

func (n *WebRTCNegotiator) SetLogger(logger *log.Logger) {
	if logger != nil {
		n.logger = logger
	}
}

func (n *WebRTCNegotiator) getPeerState(remoteID peer.ID) (*webrtcPeerState, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	state, ok := n.peers[remoteID]
	return state, ok
}

func (n *WebRTCNegotiator) Disconnect(remoteID peer.ID) error {
	n.mu.Lock()
	state, ok := n.peers[remoteID]
	if !ok {
		n.mu.Unlock()
		return nil
	}
	delete(n.peers, remoteID)
	n.mu.Unlock()

	if n.transport != nil {
		n.transport.RemoveTrack(remoteID.String())
	}
	err := state.pc.Close()
	n.logf("peer state removed peer=%s", remoteID)
	return err
}

func (n *WebRTCNegotiator) isPeerAllowed(id peer.ID) bool {
	return n.room.IsAllowed(id)
}

func (n *WebRTCNegotiator) logf(format string, args ...any) {
	if n.logger == nil {
		log.Printf("[webrtc] "+format, args...)
		return
	}
	n.logger.Printf("[webrtc] "+format, args...)
}

func (p *webrtcPeerState) send(msg WebRTCSignalMessage) error {
	p.encoderMu.Lock()
	defer p.encoderMu.Unlock()
	return p.encoder.Encode(msg)
}

func (p *webrtcPeerState) ensureAudioSender() (*webrtc.TrackLocalStaticSample, error) {
	p.trackMu.Lock()
	defer p.trackMu.Unlock()

	if p.opusTrack != nil {
		return p.opusTrack, nil
	}

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		"audio",
		"kwebbel",
	)
	if err != nil {
		return nil, err
	}
	if _, err := p.pc.AddTrack(track); err != nil {
		return nil, err
	}
	p.opusTrack = track
	return track, nil
}

func ptr[T any](v T) *T {
	return &v
}

func newWebRTCAPI() *webrtc.API {
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
