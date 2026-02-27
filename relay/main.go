package main

import (
	"log"
	"regexp"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

func main() {

	// Tune limits for my cheap hetzner
	scalingLimits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&scalingLimits)

	// Allow 4096 concurrent connections (plenty for my cheap hetzner)
	scalingLimits.SystemBaseLimit.Conns = 4096
	scalingLimits.SystemBaseLimit.Streams = 8192
	scalingLimits.SystemBaseLimit.Memory = 1 << 30 // 1GB Memory Limit for buffers (leaves 3GB for OS)

	limiter, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(scalingLimits.AutoScale()))
	if err != nil {
		panic(err)
	}

	resources := relay.DefaultResources()
	resources.Limit.Data = 1 << 30           // 1 GB per connection (basically unlimited for voice)
	resources.Limit.Duration = 1 * time.Hour // 1 hour

	host, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4242", "/ip4/0.0.0.0/udp/4242/quic-v1"),
		libp2p.ResourceManager(limiter),
		libp2p.ForceReachabilityPublic(),
	)
	if err != nil {
		panic(err)
	}

	_, err = relay.New(host, relay.WithResources(resources))
	if err != nil {
		panic(err)
	}

	host.Network().Notify(&relayNotifier{})
	log.Printf("Relay active. ID: %s", host.ID())
	// Show addresses
	log.Printf("✅ Relay active. ID: %s", host.ID())
	regex := regexp.MustCompile(`udp/\d+/quic-v1`)
	for _, addr := range host.Addrs() {
		// Does the string contain udp/<number>/quic-v1
		if regex.MatchString(addr.String()) {
			log.Printf("📢 Reachable at: %s/p2p/%s", addr.String(), host.ID())
		}
		//else log.Printf("📢 Reachable at: %s/p2p/%s", addr.String(), host.ID())
	}
	select {}
}

type relayNotifier struct{}

func (n *relayNotifier) Listen(network.Network, ma.Multiaddr)      {}
func (n *relayNotifier) ListenClose(network.Network, ma.Multiaddr) {}
func (n *relayNotifier) Connected(net network.Network, c network.Conn) {
	log.Printf("Relay: peer connected: %s", c.RemotePeer())
}
func (n *relayNotifier) Disconnected(net network.Network, c network.Conn) {
	log.Printf("Relay: peer disconnected: %s", c.RemotePeer())
}
