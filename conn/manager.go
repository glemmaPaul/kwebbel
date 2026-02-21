package conn

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
)

type Group struct {
	ID    string
	Peers []peer.ID
}

type ConnectionManager struct {
	activeGroup *Group
	host        host.Host
	streams     map[peer.ID]network.Stream
	mu          sync.RWMutex
}

func NewConnectionManager(host host.Host) *ConnectionManager {
	return &ConnectionManager{
		activeGroup: nil,
		host:        host,
	}
}

func CreateHost(port int, randomness io.Reader) (host.Host, error) {
	node, err := libp2p.New(
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", port)),
		libp2p.EnableHolePunching(),
	)
	if err != nil {
		return nil, err
	}

	return node, nil
}

func (cm *ConnectionManager) createPeerInfo(destination string) (*peer.AddrInfo, error) {
	ma, err := multiaddr.NewMultiaddr(destination)
	if err != nil {
		return nil, err
	}
	peer, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return nil, err
	}
	return peer, nil
}

func (cm *ConnectionManager) DialPeers(ctx context.Context, destinations []string) error {
	streams := make(map[peer.ID]network.Stream)
	for _, destination := range destinations {
		peer, err := cm.createPeerInfo(destination)
		if err != nil {
			log.Println("Error creating peer info for", destination, err)
			continue
		}
		log.Println("Connecting to", destination)
		cm.host.Peerstore().AddAddrs(peer.ID, peer.Addrs, peerstore.PermanentAddrTTL)
		if err := cm.host.Connect(ctx, *peer); err != nil {
			return err
		}

		stream, err := cm.host.NewStream(ctx, peer.ID, VoiceProtocol)
		if err != nil {
			return err
		}
		streams[peer.ID] = stream
	}
	cm.streams = streams
	log.Println("Dialed", len(streams), "peers")
	return nil
}

/*
Broadcasts the packet stream (egress) to all connected peers.
*/
func (cm *ConnectionManager) GoBroadcast(packetStream <-chan []byte) error {
	// Loop forever reading from the channel
	for data := range packetStream {

		// Lock before reading the map
		cm.mu.RLock()
		streamCount := len(cm.streams)
		if streamCount > 0 {

			// Protocol framing: Force adding the length header!
			frame := make([]byte, 2+len(data))
			binary.BigEndian.PutUint16(frame[0:], uint16(len(data)))
			copy(frame[2:], data)

			// Fan-out: Write to everyone
			for id, stream := range cm.streams {
				// Write the FRAMED data, not the raw data
				_, err := stream.Write(frame)
				if err != nil {
					log.Printf("Error writing to %s: %v", id, err)
					// TODO: Trigger a cleanup here
				}
			}
		}

		cm.mu.RUnlock()
	}
	return nil
}

func NewHost(port int, randomness io.Reader) (host.Host, error) {
	// Creates a new RSA key pair for this host.
	prvKey, _, err := crypto.GenerateKeyPairWithReader(crypto.RSA, 2048, randomness)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// 0.0.0.0 will listen on any interface device.
	sourceMultiAddr, _ := multiaddr.NewMultiaddr(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port))

	// libp2p.New constructs a new libp2p Host.
	// Other options can be added here.
	return libp2p.New(
		libp2p.ListenAddrs(sourceMultiAddr),
		libp2p.Identity(prvKey),
	)
}

func (cm *ConnectionManager) CreateIncomingStreamHandler(mixer *audio.Mixer) func(network.Stream) {
	return func(s network.Stream) {
		cm.handleIncomingStream(s, mixer)
	}
}

func (cm *ConnectionManager) handleIncomingStream(s network.Stream, mixer *audio.Mixer) {
	log.Printf("Incoming voice stream from: %s", s.Conn().RemotePeer())

	// // Add to our broadcast list so we can talk back
	// cm.AddPeer(s)

	// // 2. SPAWN THE READ LOOP
	// // This goroutine lives as long as the connection exists
	// go cm.readStreamLoop(s)

	// TODO: Create factory method for ingress for different types of ingress
	ingressDecoder := audio.NewAudioIngress(mixer)
	go ingressDecoder.ReadStream(s)
}
