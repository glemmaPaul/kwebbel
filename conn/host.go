package conn

import (
	"fmt"
	"log"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
)

func NewHost(port int, prvKey crypto.PrivKey, isLocal bool) (host.Host, error) {
	ipAddress := "0.0.0.0"
	if isLocal {
		ipAddress = "127.0.0.1"
	}

	tcpAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%d", ipAddress, port))
	quicAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/%d/quic-v1", ipAddress, port))

	// For discovery
	relayAddr, _ := multiaddr.NewMultiaddr("/p2p-circuit")

	log.Println("Initializing host on", ipAddress, port)

	return libp2p.New(
		libp2p.ListenAddrs(tcpAddr, quicAddr, relayAddr),
		libp2p.Identity(prvKey),
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(),
		libp2p.ForceReachabilityPrivate(),
	)
}
