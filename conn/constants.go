package conn

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/webrtc/v4"
)

const (
	// WebRTCSignalProtocol carries SDP/ICE messages over libp2p streams.
	WebRTCSignalProtocol = "/app/kwebbel-webrtc-signal/0.0.1"
)

type WebRTCSignalMessage struct {
	Type      string                     `json:"type"` // offer, answer, candidate
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
	Error     string                     `json:"error,omitempty"`
}

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
