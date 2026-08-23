package transport

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kwebbelkorp/kwebbel/logging"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
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
	Negotiate(ctx context.Context, host host.Host, remote peer.AddrInfo) error
	Disconnect(id peer.ID) error
	HandleSignalStream(stream network.Stream)
	Events() <-chan ConnectionEvent
}

type PeerList interface {
	GetPeers() []peer.ID
	IsAllowed(id peer.ID) bool
}

type PeerStatus struct {
	PeerID          peer.ID
	Connected       bool
	Connecting      bool
	Dialing         bool
	RetryCount      int
	NextAttempt     time.Time
	LastError       string
	LastConnectedAt time.Time
}

type trackedPeer struct {
	info            peer.AddrInfo
	connected       bool
	connecting      bool
	dialing         bool
	retryCount      int
	nextAttempt     time.Time
	lastError       string
	lastConnectedAt time.Time
}

// PeerTracker is the desired-state loop for the voice mesh.
// It tracks which peers should stay connected, reconciles that set with the
// room allowlist, drives negotiation attempts, and keeps retry state.
type PeerTracker struct {
	mu           sync.RWMutex
	host         host.Host
	peers        map[peer.ID]*trackedPeer
	policy       RetryPolicy
	negotiator   Negotiator
	allowedPeers PeerList
	logger       *log.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	closed       chan struct{}
	closeOnce    sync.Once
}

func NewPeerTracker(
	ctx context.Context,
	host host.Host,
	negotiator Negotiator,
	allowedPeers PeerList,
	policy RetryPolicy,
) *PeerTracker {
	policy = normalizeRetryPolicy(policy)
	logger := logging.FromContext(ctx)
	cancelCtx, cancel := context.WithCancel(ctx)
	p := &PeerTracker{
		host:         host,
		peers:        make(map[peer.ID]*trackedPeer),
		policy:       policy,
		negotiator:   negotiator,
		allowedPeers: allowedPeers,
		logger:       logger,
		ctx:          cancelCtx,
		cancel:       cancel,
		closed:       make(chan struct{}),
	}
	go p.schedule()
	return p
}

func (p *PeerTracker) StreamHandler() func(stream network.Stream) {
	return func(stream network.Stream) {
		remoteID := stream.Conn().RemotePeer()
		if !p.isAllowed(remoteID) {
			_ = stream.Reset()
			return
		}
		p.negotiator.HandleSignalStream(stream)
	}
}

func (p *PeerTracker) Dial(ctx context.Context, remote peer.AddrInfo) error {
	select {
	case <-p.closed:
		return errors.New("peer tracker is closed")
	default:
	}
	if !p.isAllowed(remote.ID) {
		return fmt.Errorf("peer %s is not allowed", remote.ID)
	}
	p.Track(remote)
	info, shouldDial := p.beginDial(remote.ID)
	if !shouldDial {
		return nil
	}
	return p.dial(ctx, info)
}

func (p *PeerTracker) Track(remote peer.AddrInfo) {
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

	p.peers[remote.ID] = &trackedPeer{
		info:        remote,
		nextAttempt: time.Now(),
	}
}

// SyncPeers immediately reconciles tracked peers with the current allowlist.
func (p *PeerTracker) SyncPeers() error {
	return p.syncPeers()
}

func (p *PeerTracker) syncPeers() error {
	now := time.Now()
	peers := p.allowedPeers.GetPeers()
	allowed := make(map[peer.ID]struct{}, len(peers))
	for _, id := range peers {
		if p.isAllowed(id) {
			allowed[id] = struct{}{}
		}
	}

	p.mu.Lock()
	for id := range allowed {
		if _, tracked := p.peers[id]; !tracked {
			p.peers[id] = &trackedPeer{
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

func (p *PeerTracker) isAllowed(id peer.ID) bool {
	return id != p.host.ID() && p.allowedPeers.IsAllowed(id)
}

func (p *PeerTracker) Untrack(id peer.ID) error {
	p.mu.Lock()
	delete(p.peers, id)
	negotiator := p.negotiator
	p.mu.Unlock()
	return disconnectPeers(negotiator, []peer.ID{id})
}

func (p *PeerTracker) IsTracked(id peer.ID) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.peers[id]
	return ok
}

func (p *PeerTracker) TrackedPeerIDs() []peer.ID {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ids := make([]peer.ID, 0, len(p.peers))
	for id := range p.peers {
		ids = append(ids, id)
	}
	return ids
}

func (p *PeerTracker) Status(id peer.ID) (PeerStatus, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	state, ok := p.peers[id]
	if !ok {
		return PeerStatus{}, false
	}
	return PeerStatus{
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

func (p *PeerTracker) beginDial(id peer.ID) (peer.AddrInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.peers[id]
	if !ok || state.connected || state.connecting || state.dialing {
		return peer.AddrInfo{}, false
	}
	state.dialing = true
	return state.info, true
}

func (p *PeerTracker) dial(ctx context.Context, remote peer.AddrInfo) error {
	p.mu.RLock()
	negotiator := p.negotiator
	timeout := p.policy.DialTimeout
	p.mu.RUnlock()

	if negotiator == nil {
		err := errors.New("peer tracker requires a negotiator")
		p.completeDial(remote.ID, err)
		return err
	}

	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stopCancel := context.AfterFunc(p.ctx, cancel)
	defer stopCancel()
	err := negotiator.Negotiate(dialCtx, p.host, remote)
	p.completeDial(remote.ID, err)
	return err
}

func (p *PeerTracker) completeDial(id peer.ID, err error) {
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

func (p *PeerTracker) updateState(event ConnectionEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.peers[event.PeerID]
	if !ok {
		return
	}

	switch event.State {
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

func (p *PeerTracker) Close() {
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

func (p *PeerTracker) schedule() {
	ticker := time.NewTicker(p.policy.RetryInterval)
	defer ticker.Stop()
	events := p.negotiator.Events()

	for {
		select {
		case <-p.closed:
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch event.Kind {
			case ConnectionEventPeerDiscovered:
				if p.isAllowed(event.PeerID) {
					p.Track(peer.AddrInfo{ID: event.PeerID})
				}
			case ConnectionEventStateChanged:
				p.updateState(event)
			}
		case now := <-ticker.C:
			_ = p.syncPeers()
			for _, remote := range p.pendingDials(now) {
				go func() {
					p.logger.Printf("Dialing peer %s", remote.ID)
					_ = p.dial(p.ctx, remote)
				}()
			}
		}
	}
}

func (p *PeerTracker) pendingDials(now time.Time) []peer.AddrInfo {
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

func (p *PeerTracker) retryBackoff(retries int) time.Duration {
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
