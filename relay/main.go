package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kwebbelkorp/kwebbel/identity"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

func main() {
	privateKeyFile := flag.String("private-key-file", "", "path to identity seed file (encrypted or plain)")
	privateKeyPassphrase := flag.String("private-key-passphrase", "", "passphrase for encrypted identity seed file")
	turnPublicIP := flag.String("turn-public-ip", "", `public IPv4 address for TURN, "auto" to discover it, empty to disable TURN`)
	turnPort := flag.Int("turn-port", 3478, "TURN/STUN UDP listening port")
	turnRealm := flag.String("turn-realm", "kwebbel", "TURN authentication realm")
	turnMinRelayPort := flag.Int("turn-min-relay-port", 50000, "first UDP port available for TURN allocations")
	turnMaxRelayPort := flag.Int("turn-max-relay-port", 50100, "last UDP port available for TURN allocations")
	turnCredentialTTL := flag.Duration("turn-credential-ttl", 10*time.Minute, "lifetime of credentials issued over libp2p")
	flag.Parse()

	// Tune limits for my cheap hetzner
	scalingLimits := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&scalingLimits)

	// Allow 4096 concurrent connections (plenty for my cheap hetzner)
	scalingLimits.SystemBaseLimit.Conns = 4096
	scalingLimits.SystemBaseLimit.Streams = 8192
	scalingLimits.SystemBaseLimit.Memory = 1 << 30 // 1GB Memory Limit for buffers (leaves 3GB for OS)

	limiter, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(scalingLimits.AutoScale()))
	if err != nil {
		panic(err)
	}

	resources := relay.DefaultResources()
	resources.Limit.Data = 256 << 20         // 256MB per connection
	resources.Limit.Duration = 1 * time.Hour // 1 hour

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/4242", "/ip4/0.0.0.0/udp/4242/quic-v1"),
		libp2p.ResourceManager(limiter),
		libp2p.ForceReachabilityPublic(),
	}

	if *privateKeyFile != "" {
		passphrase := strings.TrimSpace(*privateKeyPassphrase)

		keyFileExists, err := fileExists(*privateKeyFile)
		if err != nil {
			panic(err)
		}

		if !keyFileExists {
			if passphrase == "" {
				fmt.Print("New key passphrase (press Enter for none): ")
				reader := bufio.NewReader(os.Stdin)
				input, err := reader.ReadString('\n')
				if err == nil {
					passphrase = strings.TrimSpace(input)
				}
			}
			if err := createRelaySeedFile(*privateKeyFile, passphrase); err != nil {
				panic(err)
			}
		} else if passphrase == "" {
			fmt.Print("Passphrase (press Enter for none): ")
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err == nil {
				passphrase = strings.TrimSpace(input)
			}
		}

		privKey, err := loadRelayPrivateKey(*privateKeyFile, passphrase)
		if err != nil {
			panic(err)
		}
		opts = append(opts, libp2p.Identity(privKey))
	}

	host, err := libp2p.New(opts...)
	if err != nil {
		panic(err)
	}

	if *turnPublicIP != "" {
		if *turnMinRelayPort < 1 || *turnMinRelayPort > 65535 ||
			*turnMaxRelayPort < 1 || *turnMaxRelayPort > 65535 {
			log.Fatal("TURN relay ports must be between 1 and 65535")
		}
		publicIP := net.ParseIP(*turnPublicIP)
		if strings.EqualFold(strings.TrimSpace(*turnPublicIP), "auto") {
			publicIP, err = autodiscoverIP()
			if err != nil {
				log.Fatalf("TURN public IP discovery failed: %v", err)
			}
			log.Printf("TURN public IP discovered as %s", publicIP)
		}
		turnRelay, err := newTURNService(turnServerConfig{
			PublicIP:      publicIP,
			ListenPort:    *turnPort,
			Realm:         *turnRealm,
			MinRelayPort:  uint16(*turnMinRelayPort),
			MaxRelayPort:  uint16(*turnMaxRelayPort),
			CredentialTTL: *turnCredentialTTL,
		}, host.Peerstore().PrivKey(host.ID()))
		if err != nil {
			log.Fatalf("TURN startup failed: %v", err)
		}
		defer turnRelay.Close()
		turnRelay.registerCredentialHandler(host)
		log.Printf(
			"TURN/STUN active at %s:%d (relay UDP ports %d-%d, credential protocol %s)",
			publicIP,
			*turnPort,
			*turnMinRelayPort,
			*turnMaxRelayPort,
			turnCredentialProtocol,
		)
	}

	_, err = relay.New(host, relay.WithResources(resources))
	if err != nil {
		panic(err)
	}

	host.Network().Notify(&relayNotifier{})
	log.Printf("Relay active. ID: %s", host.ID())
	// Show addresses
	log.Printf("✅ Relay active. ID: %s", host.ID())
	regex := regexp.MustCompile(`udp/\d+/quic-v1`)
	for _, addr := range host.Addrs() {
		// Does the string contain udp/<number>/quic-v1
		if regex.MatchString(addr.String()) {
			log.Printf("📢 Reachable at: %s/p2p/%s", addr.String(), host.ID())
		}
		//else log.Printf("📢 Reachable at: %s/p2p/%s", addr.String(), host.ID())
	}
	select {}
}

type relayNotifier struct{}

func (n *relayNotifier) Listen(network.Network, ma.Multiaddr)      {}
func (n *relayNotifier) ListenClose(network.Network, ma.Multiaddr) {}
func (n *relayNotifier) Connected(net network.Network, c network.Conn) {
	log.Printf("Relay: peer connected: %s", c.RemotePeer())
}
func (n *relayNotifier) Disconnected(net network.Network, c network.Conn) {
	log.Printf("Relay: peer disconnected: %s", c.RemotePeer())
}

func loadRelayPrivateKey(privateKeyFile string, passphrase string) (crypto.PrivKey, error) {
	seed, err := identity.LoadSeed(privateKeyFile, passphrase)
	if err != nil {
		if passphrase == "" {
			return nil, fmt.Errorf("failed to load relay key file %q (if encrypted, pass --private-key-passphrase): %w", privateKeyFile, err)
		}
		return nil, err
	}

	im, err := identity.LoadFromBytes(seed)
	if err != nil {
		return nil, err
	}

	return im.MasterPrivateKey()
}

func fileExists(filename string) (bool, error) {
	_, err := os.Stat(filename)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func createRelaySeedFile(privateKeyFile string, passphrase string) error {
	im, err := identity.NewIdentityManager()
	if err != nil {
		return err
	}

	parentDir := filepath.Dir(privateKeyFile)
	if parentDir != "." {
		if err := os.MkdirAll(parentDir, 0700); err != nil {
			return err
		}
	}

	seed := im.ExportMasterSeed()
	if passphrase == "" {
		if err := os.WriteFile(privateKeyFile, seed, 0600); err != nil {
			return err
		}
	} else {
		if err := identity.SaveEncrypted(privateKeyFile, seed, passphrase); err != nil {
			return err
		}
	}

	masterPublicKey, err := im.GetMasterPublicID()
	if err != nil {
		return err
	}

	log.Printf("Created new relay identity at %s (peer id: %s)", privateKeyFile, masterPublicKey)
	return nil
}
