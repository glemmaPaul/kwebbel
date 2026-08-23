package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/conn"
	"github.com/kwebbelkorp/kwebbel/core"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/kwebbelkorp/kwebbel/logging"
	"github.com/kwebbelkorp/kwebbel/rooms"
	"github.com/kwebbelkorp/kwebbel/transport"
	"github.com/kwebbelkorp/kwebbel/tui"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func main() {
	connectTo := flag.String("connect-to", "", "address to connect to")
	relayPeerId := flag.String("relay-peer-id", "12D3KooWNCttbqRdEeF1vaSuZBKns61jmzzGRp5DZuU1wpYsWg72", "relay peer id")
	relayIp := flag.String("relay-ip", "89.167.83.252", "relay ip")
	tuiEnabled := flag.Bool("tui", false, "enable tui")
	becomeHost := flag.Bool("host-room", false, "become host")
	hostRoomName := flag.String("host-room-name", "default", "name of the room to host")

	flag.Parse()

	logOutput := io.Writer(io.Discard)
	if !*tuiEnabled {
		logOutput = os.Stderr
	}
	logger := log.New(logOutput, "", log.LstdFlags)
	log.SetOutput(logOutput)

	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := logging.WithLogger(baseCtx, logger)

	im, err := identity.NewIdentityManager()
	if err != nil {
		log.Fatal(err)
	}

	roomKey, err := im.DeriveRoomKey(*hostRoomName, *hostRoomName)
	if err != nil {
		log.Fatal(err)
	}
	room := rooms.NewRoom(*hostRoomName)
	host, err := conn.NewHost(0, roomKey, false)
	if err != nil {
		log.Fatal(err)
	}

	peerInfo := peer.AddrInfo{
		ID:    host.ID(),
		Addrs: host.Addrs(),
	}
	addrs, err := peer.AddrInfoToP2pAddrs(&peerInfo)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("libp2p node address:", addrs[0])
	mixer := audio.NewMixer()
	audioTransport := transport.NewOpusAudioTransport(mixer)

	wrn := transport.NewWebRTCNegotiator(ctx, audioTransport)
	peerTracker := transport.NewPeerTracker(ctx, host, wrn, room, transport.DefaultRetryPolicy())
	wrb := transport.NewWebRTCAudioBridge(ctx, host, peerTracker)
	relayManager := conn.NewRelayManager(host)

	kwebbelaar := core.NewKwebbelaar(host, im, room, peerTracker)

	outputErr := kwebbelaar.StartAudioOutput(mixer)
	if outputErr != nil {
		log.Fatal(outputErr)
	}

	if *becomeHost {
		if err := kwebbelaar.HostRoom(ctx); err != nil {
			log.Fatal(err)
		}
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

	relayCtx, relayCancel := context.WithTimeout(ctx, 15*time.Second)
	err = relayManager.Connect(relayCtx, relayInfo)
	relayCancel()
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

	var publishers sync.WaitGroup
	publishers.Go(func() {
		audioTransport.Publish(ctx, audioEgress.Output)
	})

	if *connectTo != "" {
		connectToAddr, err := conn.ParseConnectAddr(*connectTo)
		if err != nil {
			log.Fatal(err)
		}
		err = kwebbelaar.Attend(ctx, connectToAddr)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *tuiEnabled {
		var ui *tui.Program

		ui = tui.New(tui.Signals{
			OnConnect: func(peerID string) {
				go func() {
					peerId, err := peer.Decode(peerID)
					if err != nil {
						ui.SetStatus("Peer ID is invalid")
						return
					}
					connectToAddr, err := relayManager.TryPeer(ctx, peerId)
					if err != nil {
						ui.SetStatus(fmt.Sprintf("Failed to connect to %s: could not find relay", peerId.String()))
						return
					}
					ui.SetStatus(fmt.Sprintf("Found connection through relay %s", connectToAddr.ID.String()))
					time.Sleep(400 * time.Millisecond)
					ui.EnterCall([]tui.Caller{
						{ID: "local", Label: "You", Local: true},
						{ID: peerId.String(), Label: "Peer"},
					})

				}()
			},
			OnMute: func() {
				go func() {
					audioEgress.SetMuted(!audioEgress.Muted())
					ui.SetMuted(audioEgress.Muted())
				}()
			},
			OnDisconnect: func() {
				go func() {
					audioEgress.SetMuted(false)
					ui.LeaveCall()
					ui.SetStatus("Disconnected")
				}()
			},
		})

		if err := ui.Run(); err != nil {
			logger.Fatal(err)
		}
	} else {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
	}
	logger.Println("Shutting down...")
	cancel()
	publishers.Wait()
	wrb.Close()
	host.Close()
}
