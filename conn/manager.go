package conn

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
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
	relays      map[string]*Relay
	muRelay     sync.RWMutex
}

func NewConnectionManager(host host.Host) *ConnectionManager {
	return &ConnectionManager{
		activeGroup: nil,
		host:        host,
		relays:      make(map[string]*Relay),
	}
}

func (cm *ConnectionManager) DialPeers(ctx context.Context, destinations []string) error {
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
		cm.host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)

		log.Printf("Attempting to dial: %s", info.ID)

		stream, err := cm.host.NewStream(context.Background(), info.ID, VoiceProtocol)
		if err != nil {
			log.Println("Error creating stream to", info.ID, err)
			return err
		}
		streams[info.ID] = stream
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
		libp2p.EnableNATService(),
		//libp2p.ForceReachabilityPrivate(),
	)
}

func (cm *ConnectionManager) JoinRelay(relay *Relay) error {
	log.Println("Joining relay", relay.ID, relay.Addrs, relay.Location)
	addrInfo := peer.AddrInfo{
		ID:    relay.ID,
		Addrs: relay.Addrs,
	}
	hostErr := cm.host.Connect(context.Background(), addrInfo)
	if hostErr != nil {
		return fmt.Errorf("failed to connect to relay: %w", hostErr)
	}

	cm.host.Peerstore().AddAddrs(relay.ID, relay.Addrs, peerstore.PermanentAddrTTL)

	_, err := client.Reserve(context.Background(), cm.host, addrInfo)
	if err != nil {
		return fmt.Errorf("reservation failed: %w", err)
	}
	log.Println("✅ Reservation request accepted by relay")

	cm.muRelay.Lock()
	cm.relays[relay.Location] = relay
	cm.muRelay.Unlock()
	return nil
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
