package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hajimehoshi/go-mp3"
	"github.com/kwebbelkorp/kwebbel/audio"
	"github.com/kwebbelkorp/kwebbel/conn"
	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/multiformats/go-multiaddr"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	targetSampleRate = 48000
	frameSamples     = 960
)

func main() {
	connectTo := flag.String("connect-to", "", "remote peer multiaddr")
	mp3Path := flag.String("mp3", "", "path to mp3 file to loop")
	useRelay := flag.Bool("use-relay", true, "connect to relay and reserve circuit")
	relayPeerID := flag.String("relay-peer-id", "12D3KooWBEwfTB5mbZ3qBfxanPvuEzFHwEE6tSnaPqgqEHvyfSgB", "relay peer id")
	relayAddrs := flag.String("relay-addrs", "/ip4/89.167.83.252/udp/4242/quic-v1,/ip4/89.167.83.252/tcp/4242", "comma-separated relay multiaddrs")
	flag.Parse()

	if *connectTo == "" || *mp3Path == "" {
		log.Fatal("usage: go run ./test/fake-client.go --connect-to=<multiaddr> --mp3=<path>")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	im, err := identity.NewIdentityManager()
	if err != nil {
		log.Fatal(err)
	}
	roomKey, err := im.DeriveRoomKey("default", "default")
	if err != nil {
		log.Fatal(err)
	}

	host, err := conn.NewHost(0, roomKey, false)
	if err != nil {
		log.Fatal(err)
	}
	defer host.Close()

	selfInfo := peer.AddrInfo{ID: host.ID(), Addrs: host.Addrs()}
	selfAddrs, _ := peer.AddrInfoToP2pAddrs(&selfInfo)
	if len(selfAddrs) > 0 {
		fmt.Println("fake client node address:", selfAddrs[0])
	}

	if *useRelay {
		if err := reserveRelay(ctx, host, *relayPeerID, *relayAddrs); err != nil {
			log.Fatal(err)
		}
	}

	bridge := conn.NewWebRTCAudioBridge(host, nil)

	maddr, err := multiaddr.NewMultiaddr(*connectTo)
	if err != nil {
		log.Fatal(err)
	}
	remoteInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		log.Fatal(err)
	}

	bridge.TrackPeer(*remoteInfo)

	samples, err := loadMP3AsMono48k(*mp3Path)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d mono samples from %s", len(samples), *mp3Path)

	rawPCM := make(chan []byte, 128)
	egress := audio.NewAudioEgress(rawPCM)
	egress.StartProcessing()
	bridge.StartPublishing(ctx, egress.Output)
	go loopPCM(ctx, samples, rawPCM)

	log.Println("fake client streaming loop started")
	<-ctx.Done()
	log.Println("fake client shutting down")
	bridge.Close()
}

func reserveRelay(ctx context.Context, h host.Host, relayID, relayAddrCSV string) error {
	id, err := peer.Decode(relayID)
	if err != nil {
		return err
	}

	parts := strings.Split(relayAddrCSV, ",")
	addrs := make([]ma.Multiaddr, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		addrs = append(addrs, ma.StringCast(s))
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no relay addresses provided")
	}

	relayInfo := peer.AddrInfo{ID: id, Addrs: addrs}
	if err := h.Connect(ctx, relayInfo); err != nil {
		return err
	}
	h.Peerstore().AddAddrs(relayInfo.ID, relayInfo.Addrs, peerstore.PermanentAddrTTL)
	if _, err := client.Reserve(ctx, h, relayInfo); err != nil {
		return err
	}
	log.Println("relay reservation accepted")
	return nil
}

func loadMP3AsMono48k(path string) ([]int16, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder, err := mp3.NewDecoder(f)
	if err != nil {
		return nil, err
	}

	decoded, err := io.ReadAll(decoder)
	if err != nil {
		return nil, err
	}
	if len(decoded) < 4 {
		return nil, fmt.Errorf("mp3 decoded to too few bytes")
	}

	// go-mp3 outputs signed 16-bit little-endian stereo PCM.
	totalStereoSamples := len(decoded) / 2
	stereo := make([]int16, totalStereoSamples)
	for i := 0; i < totalStereoSamples; i++ {
		stereo[i] = int16(binary.LittleEndian.Uint16(decoded[i*2 : i*2+2]))
	}

	mono := make([]int16, 0, len(stereo)/2)
	for i := 0; i+1 < len(stereo); i += 2 {
		l := int32(stereo[i])
		r := int32(stereo[i+1])
		mono = append(mono, int16((l+r)/2))
	}

	if decoder.SampleRate() == targetSampleRate {
		return mono, nil
	}

	return resampleLinear(mono, decoder.SampleRate(), targetSampleRate), nil
}

func resampleLinear(input []int16, fromHz, toHz int) []int16 {
	if len(input) == 0 || fromHz <= 0 || toHz <= 0 {
		return nil
	}
	if fromHz == toHz {
		out := make([]int16, len(input))
		copy(out, input)
		return out
	}

	outLen := int(float64(len(input)) * float64(toHz) / float64(fromHz))
	if outLen < 1 {
		outLen = 1
	}
	out := make([]int16, outLen)

	step := float64(fromHz) / float64(toHz)
	pos := 0.0
	for i := 0; i < outLen; i++ {
		base := int(pos)
		next := base + 1
		if next >= len(input) {
			next = len(input) - 1
		}
		frac := pos - float64(base)
		a := float64(input[base])
		b := float64(input[next])
		out[i] = int16(a + (b-a)*frac)
		pos += step
	}
	return out
}

func loopPCM(ctx context.Context, samples []int16, out chan<- []byte) {
	if len(samples) < frameSamples {
		return
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	idx := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frameBytes := make([]byte, frameSamples*2)
			for i := 0; i < frameSamples; i++ {
				s := samples[(idx+i)%len(samples)]
				binary.LittleEndian.PutUint16(frameBytes[i*2:i*2+2], uint16(s))
			}
			idx = (idx + frameSamples) % len(samples)
			out <- frameBytes
		}
	}
}
