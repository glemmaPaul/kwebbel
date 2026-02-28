package conn

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

type Relay struct {
	ID       peer.ID
	Addrs    []multiaddr.Multiaddr
	Location string
}
