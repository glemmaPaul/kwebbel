package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/conn"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/kwebbel"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func main() {
	ch := make(chan os.Signal, 1)
	connectTo := flag.String("connect-to", "", "address to connect to")
	//relayAddr := flag.String("relay-addr", "", "relay address to use")
	relayPeerId := flag.String("relay-peer-id", "12D3KooWARhrrc15CCqqaxCJteE4CRx2Kx8ghiPqvPfvVPJpgoFc", "relay peer id to use")
	relayPort := flag.Int("relay-port", 4242, "relay port to use")
	relayIP := flag.String("relay-ip", "89.167.83.252", "relay ip to use")
	//password := flag.String("password", "", "password to use for the identity")
	flag.Parse()

	// keystore, err := identity.NewKeyStore()
	// if err != nil {
	// 	log.Fatal(err)
	// }

	im, err := identity.NewIdentityManager()
	if err != nil {
		log.Fatal(err)
	}

	// err = im.Load(*password)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	roomKey, err := im.DeriveRoomKey("default", "default")
	host, err := conn.NewHost(0, roomKey, false)
	if err != nil {
		log.Fatal(err)
	}

	cm := conn.NewConnectionManager(host)

	if *relayPeerId != "" && *relayPort != 0 && *relayIP != "" {
		peerId, err := peer.Decode(*relayPeerId)
		if err != nil {
			log.Fatal(err)
		}
		relayAddr := fmt.Sprintf("/ip4/%s/udp/%d/quic-v1/p2p/%s", *relayIP, *relayPort, *relayPeerId)
		hostErr := cm.JoinRelay(&conn.Relay{ID: peerId, Addrs: []multiaddr.Multiaddr{multiaddr.StringCast(relayAddr)}, Location: *relayIP})
		if hostErr != nil {
			log.Fatal(hostErr)
		}

		shareLink := fmt.Sprintf("%s/p2p-circuit/p2p/%s", relayAddr, host.ID().String())
		fmt.Println("Share link:", shareLink)
	}

	peerInfo := peer.AddrInfo{
		ID:    host.ID(),
		Addrs: host.Addrs(),
	}
	addrs, err := peer.AddrInfoToP2pAddrs(&peerInfo)
	fmt.Println("libp2p node address:", addrs[0])

	if err != nil {
		log.Fatal(err)
	}

	kwebbelaar := kwebbel.NewKwebbelaar(host, cm, im)

	if *connectTo != "" {
		audioEgress, err := kwebbelaar.StartAudioEgress()
		if err != nil {
			log.Fatal(err)
		}
		cm.DialPeers(context.Background(), []string{*connectTo})
		go cm.GoBroadcast(audioEgress.Output)
		serverStr := *connectTo
		server, err := peer.AddrInfoFromString(serverStr)
		if err != nil {
			log.Fatal(err)
		}
		kwebbelaar.JoinRoom(server.ID)

	} else {
		kwebbelaar.BecomeHost(rooms.Room{ID: "default", MC: host.ID()})
		mixer := audio.NewMixer()
		err := kwebbelaar.StartAudioOutput(mixer)
		if err != nil {
			log.Fatal(err)
		}
		host.SetStreamHandler(conn.VoiceProtocol, cm.CreateIncomingStreamHandler(mixer))
	}

	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	fmt.Println("Shutting down...")
	host.Close()
}
