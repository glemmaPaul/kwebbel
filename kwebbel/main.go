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
	"github.com/libp2p/go-libp2p/core/peer"
)

func main() {
	fmt.Println("Hello, World!")
	ch := make(chan os.Signal, 1)
	port := flag.Int("port", 8080, "port to listen on")
	connectTo := flag.String("connect-to", "", "address to connect to")
	flag.Parse()

	fmt.Println("Listening on port", *port)
	host, err := conn.CreateHost(*port, nil)
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
	cm := conn.NewConnectionManager(host)

	if *connectTo != "" {
		// 2. Start Audio Input (Mic)
		mic, err := audio.NewAudioInput()
		if err != nil {
			log.Fatal(err)
		}

		encodedChan := make(chan []byte, 100)
		egress := audio.AudioEgress{
			Input: mic.OutputChan,
			Output: func(data []byte) {
				encodedChan <- data
			},
		}
		egress.StartProcessing()
		err = mic.Start()
		if err != nil {
			log.Fatal(err)
		}
		cm.DialPeers(context.Background(), []string{*connectTo})
		go cm.GoBroadcast(encodedChan)

		// Create a group
		// group := conn.NewGroup("default", []peer.ID{peerID})
		// err = cm.JoinGroup(context.Background(), group)
		// if err != nil {
		// 	log.Fatal(err)
		// }
	} else {
		audioOutput, err := audio.NewAudioOutput(mixer)
		if err != nil {
			log.Fatal(err)
		}
		err = audioOutput.Start()
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
