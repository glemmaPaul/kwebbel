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
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/kwebbel"
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
	relayAddr := flag.String("relay-addr", "", "relay address to use")
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

	kwebbelaar := kwebbel.NewKwebbelaar(cm, im)
	mixer := audio.NewMixer()
	outputErr := kwebbelaar.StartAudioOutput(mixer)
	if outputErr != nil {
		log.Fatal(outputErr)
	}

	host.SetStreamHandler(conn.VoiceProtocol, func(s network.Stream) {
		ingressDecoder := audio.NewAudioIngress(mixer)
		go ingressDecoder.ReadStream(s)
	})
	host.SetStreamHandler(rooms.RoomProtocol, func(s network.Stream) {
		log.Println("Incoming room stream from:", s.Conn().RemotePeer())

	})

	if *relayAddr != "" {
		peerId, err := peer.Decode("12D3KooWQgBCgKZLsZmMWyGw3K6YrrsLwGq1xU6Gv2ewdtcNMhc3")
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
		for i := 0; i < 3; i++ {
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

	if *connectTo != "" {
		peerAddrInfo, err := peer.AddrInfoFromString(*connectTo)
		if err != nil {
			log.Fatal(err)
		}
		audioEgress, err := kwebbelaar.StartAudioEgress()
		if err != nil {
			log.Fatal(err)
		}
		cm.DialPeers(context.Background(), []string{*connectTo})
		go cm.GoBroadcast(audioEgress.Output)
		log.Println("Starting audio egress", audioEgress.Output)

		host.Peerstore().AddAddrs(peerAddrInfo.ID, peerAddrInfo.Addrs, peerstore.PermanentAddrTTL)

		// stream, err := host.NewStream(context.Background(), peerAddrInfo.ID, rooms.RoomProtocol)
		// if err != nil {
		// 	log.Fatal(err)
		// }
		// log.Println("Outgoing room stream to:", stream.Conn().RemotePeer())
		// stream.Write([]byte("Hello from client"))

	}

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	fmt.Println("Shutting down...")
	host.Close()
}
