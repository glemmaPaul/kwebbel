package conn

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/rooms"
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
	streams     map[peer.ID]network.Stream
	mu          sync.RWMutex
}

func NewConnectionManager(host host.Host) *ConnectionManager {
	return &ConnectionManager{
		activeGroup: nil,
		streams:     make(map[peer.ID]network.Stream),
	}
}

func (cm *ConnectionManager) DialPeers(ctx context.Context, host host.Host, destinations []string) error {
	streams := make(map[peer.ID]network.Stream)
	for _, destination := range destinations {
		// 1. Parse the string as a Multiaddress
		maddr, err := multiaddr.NewMultiaddr(destination)
		if err != nil {
			log.Printf("Invalid address: %v", err)
			continue
		}
		log.Println("Connecting to", destination)
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Printf("AddrInfo error: %v", err)
			continue
		}

		// Relay connections are "limited" - must opt-in to use them for streams
		host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

		log.Printf("Attempting to dial: %s", info.ID)

		stream, err := host.NewStream(network.WithAllowLimitedConn(ctx, string(VoiceProtocol)), info.ID, VoiceProtocol)
		if err != nil {
			log.Println("Error creating stream to", info.ID, err)
			return err
		}
		streams[info.ID] = stream

		roomStream, err := host.NewStream(network.WithAllowLimitedConn(ctx, string(rooms.RoomProtocol)), info.ID, rooms.RoomProtocol)
		if err != nil {
			log.Println("Error creating message stream to", info.ID, err)
			return err
		}

		// test out
		roomStream.Write([]byte("Hello from client"))

	}
	cm.streams = streams
	log.Println("Dialed", len(streams), "peers")
	return nil
}

func (cm *ConnectionManager) IsDialed(peerID peer.ID) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.streams[peerID] != nil
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

func (cm *ConnectionManager) CreateIncomingStreamHandler(mixer *audio.Mixer) func(network.Stream) {
	return func(s network.Stream) {
		cm.handleIncomingStream(s, mixer)
	}
}

func (cm *ConnectionManager) handleIncomingStream(s network.Stream, mixer *audio.Mixer) {
	log.Printf("Incoming voice stream from: %s", s.Conn().RemotePeer())

	// TODO: Create factory method for ingress for different types of ingress
	ingressDecoder := audio.NewAudioIngress(mixer)
	go ingressDecoder.ReadStream(s)
}
