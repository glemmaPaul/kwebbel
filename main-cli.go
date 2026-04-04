package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
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
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/multiformats/go-multiaddr"
	ma "github.com/multiformats/go-multiaddr"
)

func main() {
	ch := make(chan os.Signal, 1)
	connectTo := flag.String("connect-to", "", "address to connect to")
	useRelay := flag.Bool("use-relay", true, "use relay")
	relayPeerId := flag.String("relay-peer-id", "12D3KooWBEwfTB5mbZ3qBfxanPvuEzFHwEE6tSnaPqgqEHvyfSgB", "relay peer id")

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

	cm := conn.NewConnectionManager(host)

	kwebbelaar := core.NewKwebbelaar(&host, cm, im)
	mixer := audio.NewMixer()
	outputErr := kwebbelaar.StartAudioOutput(mixer)
	if outputErr != nil {
		log.Fatal(outputErr)
	}

	host.SetStreamHandler(conn.VoiceProtocol, func(s network.Stream) {
		ingressDecoder := audio.NewAudioIngress(mixer)
		go ingressDecoder.ReadStream(s)
		// We also dial back to the peer
		circuitLink := "/p2p/" + s.Conn().RemotePeer().String()
		if !cm.IsDialed(s.Conn().RemotePeer()) {
			cm.DialPeers(context.Background(), host, []string{circuitLink})
		}
	})

	if *connectTo == "" {
		room := rooms.NewRoom("default")
		mcServer := rooms.NewMCServer(room)
		mcServer.Serve()
		host.SetStreamHandler(rooms.RoomProtocol, mcServer.AttachStream)
	} else {
		host.SetStreamHandler(rooms.RoomProtocol, func(s network.Stream) {
			log.Println("Incoming room stream from:", s.Conn().RemotePeer())
		})
	}

	if *useRelay {
		peerId, err := peer.Decode(*relayPeerId)
		if err != nil {
			log.Fatal(err)
		}
		//serverStr := *relayAddr
		server := peer.AddrInfo{
			ID: peerId,
			Addrs: []ma.Multiaddr{
				ma.StringCast("/ip4/89.167.83.252/udp/4242/quic-v1"),
				ma.StringCast("/ip4/89.167.83.252/tcp/4242"),
			},
		}
		hostErr := host.Connect(context.Background(), server)
		if hostErr != nil {
			log.Fatal(hostErr)
		}

		host.Peerstore().AddAddrs(server.ID, server.Addrs, peerstore.PermanentAddrTTL)

		// 1. Request the reservation
		_, err = client.Reserve(context.Background(), host, server)
		if err != nil {
			log.Fatal("Reservation failed:", err)
		}
		log.Println("✅ Reservation request accepted by relay")

		// 5. WAIT for the address to appear
		log.Println("Waiting for relay address...")
		for i := 0; i < 1; i++ {
			time.Sleep(1 * time.Second)
			for _, addr := range host.Addrs() {
				if strings.Contains(addr.String(), "p2p-circuit") {
					fmt.Printf("🚀 SUCCESS! REACHABLE AT: %s/p2p/%s\n", addr, host.ID())
					break
				}
			}
		}

		relayaddr, _ := multiaddr.NewMultiaddr("/p2p/" + server.ID.String() + "/p2p-circuit/p2p/" + host.ID().String())
		log.Println("Relay address:", relayaddr.String())
	}

	audioEgress, err := kwebbelaar.StartAudioEgress()
	if err != nil {
		log.Fatal(err)
	}

	go cm.GoBroadcast(audioEgress.Output)

	if *connectTo != "" {

		// cm.DialPeers(context.Background(), []string{*connectTo})
		// 1. Parse the string as a Multiaddress
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

		log.Printf("Attempting to dial: %s", info.ID)

		_, err = host.NewStream(network.WithAllowLimitedConn(context.Background(), string(conn.VoiceProtocol)), info.ID, conn.VoiceProtocol)
		if err != nil {
			log.Println("Error creating stream to", info.ID, err)
			log.Fatal(err)
		}

		roomStream, err := host.NewStream(network.WithAllowLimitedConn(context.Background(), string(rooms.RoomProtocol)), info.ID, rooms.RoomProtocol)
		if err != nil {
			log.Println("Error creating message stream to", info.ID, err)
			log.Fatal(err)
		}

		signals := &rooms.RoomListenerSignals{
			OnUpdatedAllowedPeers: func(peers []peer.ID) {
				log.Println("Allowed peers updated:", peers)
			},
		}
		listener := rooms.NewRoomListener(roomStream, signals)
		listener.Start()
	}

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	fmt.Println("Shutting down...")
	host.Close()
}
