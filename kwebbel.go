package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/conn"
	"github.com/kwebbelkorp/kwebbel/core"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/multiformats/go-multiaddr"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch := make(chan os.Signal, 1)
	connectTo := flag.String("connect-to", "", "address to connect to")
	relayPeerId := flag.String("relay-peer-id", "12D3KooWNCttbqRdEeF1vaSuZBKns61jmzzGRp5DZuU1wpYsWg72", "relay peer id")
	relayIp := flag.String("relay-ip", "89.167.83.252", "relay ip")

	flag.Parse()

	im, err := identity.NewIdentityManager()
	if err != nil {
		log.Fatal(err)
	}

	roomKey, err := im.DeriveRoomKey("default", "default")
	host, err := conn.NewHost(0, roomKey, false)
	if err != nil {
		log.Fatal(err)
	}

	peerInfo := peer.AddrInfo{
		ID:    host.ID(),
		Addrs: host.Addrs(),
	}
	addrs, err := peer.AddrInfoToP2pAddrs(&peerInfo)
	fmt.Println("libp2p node address:", addrs[0])
	mixer := audio.NewMixer()
	wrb := conn.NewWebRTCAudioBridge(host, mixer)
	relayManager := conn.NewRelayManager(host)

	kwebbelaar := core.NewKwebbelaar(host, im, wrb)

	outputErr := kwebbelaar.StartAudioOutput(mixer)
	if outputErr != nil {
		log.Fatal(outputErr)
	}

	if *connectTo == "" {
		kwebbelaar.BecomeMC()
	} else {
		host.SetStreamHandler(rooms.RoomProtocol, func(s network.Stream) {
			log.Println("Incoming room stream from:", s.Conn().RemotePeer())
		})
	}

	relayId, err := peer.Decode(*relayPeerId)
	if err != nil {
		log.Fatal(err)
	}

	relayInfo := conn.RelayAddrInfo(relayId, *relayIp)

	err = relayManager.Connect(ctx, relayInfo)
	if err != nil {
		log.Fatal(err)
	}

	available := relayManager.Available()
	for _, relay := range available {
		relayaddr, _ := multiaddr.NewMultiaddr("/p2p/" + relay.ID.String() + "/p2p-circuit/p2p/" + host.ID().String())
		log.Println("Relay address:", relayaddr.String())
	}

	mic, err := audio.NewAudioInput()
	if err != nil {
		log.Fatal(err)
	}
	audioEgress, err := kwebbelaar.StartAudioInput(mic)
	if err != nil {
		log.Fatal(err)
	}

	wrb.StartPublishing(context.Background(), audioEgress.Output)

	if *connectTo != "" {
		maddr, err := multiaddr.NewMultiaddr(*connectTo)
		if err != nil {
			log.Printf("Invalid address: %v", err)
			log.Fatal(err)
		}
		log.Println("Connecting to", *connectTo)
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			log.Printf("AddrInfo error: %v", err)
			log.Fatal(err)
		}

		// Relay connections are "limited" - must opt-in to use them for streams
		host.Peerstore().AddAddrs(info.ID, info.Addrs, peerstore.PermanentAddrTTL)
		// Bootstrap allowlist so initial negotiation to room host is possible.
		wrb.AllowPeer(info.ID)
		wrb.TrackPeer(*info)
		log.Printf("Tracking peer for WebRTC retries: %s", info.ID)

		log.Printf("Opening room stream to: %s", info.ID)
		roomStream, err := host.NewStream(network.WithAllowLimitedConn(context.Background(), string(rooms.RoomProtocol)), info.ID, rooms.RoomProtocol)
		if err != nil {
			log.Println("Error creating message stream to", info.ID, err)
			log.Fatal(err)
		}

		signals := &rooms.RoomListenerSignals{
			OnUpdatedAllowedPeers: func(peers []peer.ID) {
				log.Println("Allowed peers updated:", peers)
				wrb.SetAllowedPeers(peers)
				wrb.TrackAllowedPeers()
			},
		}
		listener := rooms.NewRoomListener(roomStream, signals)
		listener.Start()
	}

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	fmt.Println("Shutting down...")
	wrb.Close()
	host.Close()
}
