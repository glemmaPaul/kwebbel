package transport

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/webrtc/v4"
)

type RetryPolicy struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RetryInterval  time.Duration
	DialTimeout    time.Duration
	ConnectTimeout time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		RetryInterval:  time.Second,
		DialTimeout:    8 * time.Second,
		ConnectTimeout: 6 * time.Second,
	}
}

type Negotiator interface {
	Dial(ctx context.Context, remote peer.AddrInfo) error
	Disconnect(id peer.ID) error
}

type PeerConnectionStatus struct {
	PeerID          peer.ID
	Connected       bool
	Connecting      bool
	Dialing         bool
	RetryCount      int
	NextAttempt     time.Time
	LastError       string
	LastConnectedAt time.Time
}

type PeerConnectionManager interface {
	SetNegotiator(negotiator Negotiator)
	Dial(ctx context.Context, remote peer.AddrInfo) error
	Track(remote peer.AddrInfo)
	SyncPeers(peers []peer.ID, self peer.ID) error
	Untrack(id peer.ID) error
	IsTracked(id peer.ID) bool
	TrackedPeerIDs() []peer.ID
	Status(id peer.ID) (PeerConnectionStatus, bool)
	UpdateState(id peer.ID, state webrtc.PeerConnectionState)
	Close()
}

type managedPeerState struct {
	info            peer.AddrInfo
	connected       bool
	connecting      bool
	dialing         bool
	retryCount      int
	nextAttempt     time.Time
	lastError       string
	lastConnectedAt time.Time
}

// PeerConnections owns peer tracking, negotiation attempts, and retry state.
type PeerConnections struct {
	mu         sync.RWMutex
	peers      map[peer.ID]*managedPeerState
	policy     RetryPolicy
	negotiator Negotiator
	ctx        context.Context
	cancel     context.CancelFunc
	closed     chan struct{}
	closeOnce  sync.Once
}

func NewPeerConnections(policy RetryPolicy) *PeerConnections {
	policy = normalizeRetryPolicy(policy)
	ctx, cancel := context.WithCancel(context.Background())
	p := &PeerConnections{
		peers:  make(map[peer.ID]*managedPeerState),
		policy: policy,
		ctx:    ctx,
		cancel: cancel,
		closed: make(chan struct{}),
	}
	go p.schedule()
	return p
}

func normalizeRetryPolicy(policy RetryPolicy) RetryPolicy {
	defaults := DefaultRetryPolicy()
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaults.InitialBackoff
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = defaults.MaxBackoff
		if policy.MaxBackoff < policy.InitialBackoff {
			policy.MaxBackoff = policy.InitialBackoff
		}
	}
	if policy.RetryInterval <= 0 {
		policy.RetryInterval = defaults.RetryInterval
	}
	if policy.DialTimeout <= 0 {
		policy.DialTimeout = defaults.DialTimeout
	}
	if policy.ConnectTimeout <= 0 {
		policy.ConnectTimeout = defaults.ConnectTimeout
	}
	return policy
}

func (p *PeerConnections) SetNegotiator(negotiator Negotiator) {
	p.mu.Lock()
	p.negotiator = negotiator
	p.mu.Unlock()
}

func (p *PeerConnections) Dial(ctx context.Context, remote peer.AddrInfo) error {
	select {
	case <-p.closed:
		return errors.New("peer connections is closed")
	default:
	}
	p.Track(remote)
	info, shouldDial := p.beginDial(remote.ID)
	if !shouldDial {
		return nil
	}
	return p.dial(ctx, info)
}

func (p *PeerConnections) Track(remote peer.AddrInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.peers[remote.ID]; ok {
		if len(remote.Addrs) > 0 {
			existing.info = remote
		}
		if existing.nextAttempt.IsZero() && !existing.connected {
			existing.nextAttempt = time.Now()
		}
		return
	}

	p.peers[remote.ID] = &managedPeerState{
		info:        remote,
		nextAttempt: time.Now(),
	}
}

func (p *PeerConnections) SyncPeers(peers []peer.ID, self peer.ID) error {
	now := time.Now()
	allowed := make(map[peer.ID]struct{}, len(peers))
	for _, id := range peers {
		if id != self {
			allowed[id] = struct{}{}
		}
	}

	p.mu.Lock()
	for id := range allowed {
		if _, tracked := p.peers[id]; !tracked {
			p.peers[id] = &managedPeerState{
				info:        peer.AddrInfo{ID: id},
				nextAttempt: now,
			}
		}
	}
	removed := make([]peer.ID, 0)
	for id := range p.peers {
		if _, keep := allowed[id]; !keep {
			delete(p.peers, id)
			removed = append(removed, id)
		}
	}
	negotiator := p.negotiator
	p.mu.Unlock()

	return disconnectPeers(negotiator, removed)
}

func (p *PeerConnections) Untrack(id peer.ID) error {
	p.mu.Lock()
	delete(p.peers, id)
	negotiator := p.negotiator
	p.mu.Unlock()
	return disconnectPeers(negotiator, []peer.ID{id})
}

func (p *PeerConnections) IsTracked(id peer.ID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.peers[id]
	return ok
}

func (p *PeerConnections) TrackedPeerIDs() []peer.ID {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ids := make([]peer.ID, 0, len(p.peers))
	for id := range p.peers {
		ids = append(ids, id)
	}
	return ids
}

func (p *PeerConnections) Status(id peer.ID) (PeerConnectionStatus, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	state, ok := p.peers[id]
	if !ok {
		return PeerConnectionStatus{}, false
	}
	return PeerConnectionStatus{
		PeerID:          id,
		Connected:       state.connected,
		Connecting:      state.connecting,
		Dialing:         state.dialing,
		RetryCount:      state.retryCount,
		NextAttempt:     state.nextAttempt,
		LastError:       state.lastError,
		LastConnectedAt: state.lastConnectedAt,
	}, true
}

func (p *PeerConnections) beginDial(id peer.ID) (peer.AddrInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.peers[id]
	if !ok || state.connected || state.connecting || state.dialing {
		return peer.AddrInfo{}, false
	}
	state.dialing = true
	return state.info, true
}

func (p *PeerConnections) dial(ctx context.Context, remote peer.AddrInfo) error {
	p.mu.RLock()
	negotiator := p.negotiator
	timeout := p.policy.DialTimeout
	p.mu.RUnlock()

	if negotiator == nil {
		err := errors.New("peer connections requires a negotiator")
		p.completeDial(remote.ID, err)
		return err
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stopCancel := context.AfterFunc(p.ctx, cancel)
	defer stopCancel()
	err := negotiator.Dial(dialCtx, remote)
	p.completeDial(remote.ID, err)
	return err
}

func (p *PeerConnections) completeDial(id peer.ID, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.peers[id]
	if !ok {
		return
	}
	state.dialing = false
	if err == nil {
		state.lastError = ""
		state.nextAttempt = time.Now().Add(p.policy.ConnectTimeout)
		return
	}

	state.connected = false
	state.connecting = false
	state.retryCount++
	state.lastError = err.Error()
	state.nextAttempt = time.Now().Add(p.retryBackoff(state.retryCount))
}

func (p *PeerConnections) UpdateState(id peer.ID, connectionState webrtc.PeerConnectionState) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.peers[id]
	if !ok {
		return
	}

	switch connectionState {
	case webrtc.PeerConnectionStateConnecting:
		state.connecting = true
		state.dialing = false
		state.nextAttempt = time.Now().Add(p.policy.ConnectTimeout)
	case webrtc.PeerConnectionStateConnected:
		state.connected = true
		state.connecting = false
		state.dialing = false
		state.retryCount = 0
		state.lastError = ""
		state.lastConnectedAt = time.Now()
		state.nextAttempt = time.Time{}
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected:
		state.connected = false
		state.connecting = false
		state.dialing = false
		retryAt := time.Now().Add(p.policy.InitialBackoff)
		if state.nextAttempt.IsZero() || state.nextAttempt.After(retryAt) {
			state.nextAttempt = retryAt
		}
	case webrtc.PeerConnectionStateClosed:
		state.connected = false
		state.connecting = false
		state.dialing = false
		state.nextAttempt = time.Now().Add(p.policy.InitialBackoff)
	}
}

func (p *PeerConnections) Close() {
	p.closeOnce.Do(func() {
		p.cancel()
		close(p.closed)
		p.mu.RLock()
		negotiator := p.negotiator
		ids := make([]peer.ID, 0, len(p.peers))
		for id := range p.peers {
			ids = append(ids, id)
		}
		p.mu.RUnlock()
		_ = disconnectPeers(negotiator, ids)
	})
}

func disconnectPeers(negotiator Negotiator, ids []peer.ID) error {
	if negotiator == nil || len(ids) == 0 {
		return nil
	}

	errs := make([]error, 0)
	for _, id := range ids {
		if err := negotiator.Disconnect(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *PeerConnections) schedule() {
	ticker := time.NewTicker(p.policy.RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.closed:
			return
		case now := <-ticker.C:
			for _, remote := range p.pendingDials(now) {
				go func() {
					_ = p.dial(p.ctx, remote)
				}()
			}
		}
	}
}

func (p *PeerConnections) pendingDials(now time.Time) []peer.AddrInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	remotes := make([]peer.AddrInfo, 0)
	for _, state := range p.peers {
		if state.connected || state.connecting || state.dialing {
			continue
		}
		if !state.nextAttempt.IsZero() && now.Before(state.nextAttempt) {
			continue
		}

		state.dialing = true
		remotes = append(remotes, state.info)
	}
	return remotes
}

func (p *PeerConnections) retryBackoff(retries int) time.Duration {
	if retries <= 0 {
		return p.policy.InitialBackoff
	}
	backoff := p.policy.InitialBackoff
	for i := 1; i < retries; i++ {
		backoff *= 2
		if backoff >= p.policy.MaxBackoff {
			return p.policy.MaxBackoff
		}
	}
	return backoff
}
