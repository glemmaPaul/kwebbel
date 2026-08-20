package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/turn/v4"
)

const (
	turnCredentialProtocol = "/app/kwebbel-turn-credentials/0.0.1"
	turnCredentialInfo     = "kwebbel:turn-credentials:v1"
)

type turnServerConfig struct {
	PublicIP      net.IP
	ListenPort    int
	Realm         string
	MinRelayPort  uint16
	MaxRelayPort  uint16
	CredentialTTL time.Duration
}

type turnCredentialResponse struct {
	URLs       []string  `json:"urls,omitempty"`
	Username   string    `json:"username,omitempty"`
	Credential string    `json:"credential,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// turnService runs a regular UDP TURN/STUN service next to the libp2p relay.
// The media is not tunneled through a libp2p circuit: libp2p is only used as
// an authenticated control channel for issuing short-lived TURN credentials.
type turnService struct {
	server *turn.Server
	config turnServerConfig
	secret []byte
}

func newTURNService(config turnServerConfig, identityKey libp2pcrypto.PrivKey) (*turnService, error) {
	if err := validateTURNConfig(config); err != nil {
		return nil, err
	}

	if identityKey == nil {
		return nil, errors.New("relay identity key is required for TURN")
	}

	secret, err := deriveTURNSecret(identityKey)
	if err != nil {
		return nil, err
	}

	listener, err := net.ListenPacket(
		"udp4",
		net.JoinHostPort("0.0.0.0", strconv.Itoa(config.ListenPort)),
	)
	if err != nil {
		return nil, fmt.Errorf("listen for TURN: %w", err)
	}

	service := &turnService{
		config: config,
		secret: secret,
	}
	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       config.Realm,
		AuthHandler: service.authenticate,
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: listener,
			RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{
				RelayAddress: config.PublicIP,
				Address:      "0.0.0.0",
				MinPort:      config.MinRelayPort,
				MaxPort:      config.MaxRelayPort,
			},
			// A public TURN relay must not become a route into its own private
			// network. Public WebRTC and TURN relay candidates remain allowed.
			PermissionHandler: allowTURNPeerIP,
		}},
	})
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("create TURN server: %w", err)
	}
	service.server = server
	return service, nil
}

func validateTURNConfig(config turnServerConfig) error {
	switch {
	case config.PublicIP == nil || config.PublicIP.To4() == nil:
		return errors.New("TURN public-ip must be a public IPv4 address")
	case !config.PublicIP.IsGlobalUnicast() || config.PublicIP.IsPrivate():
		return errors.New("TURN public-ip must be globally routable")
	case config.ListenPort < 1 || config.ListenPort > 65535:
		return errors.New("TURN listen port must be between 1 and 65535")
	case strings.TrimSpace(config.Realm) == "":
		return errors.New("TURN realm is required")
	case config.MinRelayPort == 0 || config.MaxRelayPort < config.MinRelayPort:
		return errors.New("TURN relay port range is invalid")
	case config.CredentialTTL <= 0:
		return errors.New("TURN credential TTL must be positive")
	default:
		return nil
	}
}

func deriveTURNSecret(identityKey libp2pcrypto.PrivKey) ([]byte, error) {
	encoded, err := libp2pcrypto.MarshalPrivateKey(identityKey)
	if err != nil {
		return nil, fmt.Errorf("marshal relay identity key: %w", err)
	}

	mac := hmac.New(sha256.New, encoded)
	_, _ = mac.Write([]byte(turnCredentialInfo))
	return mac.Sum(nil), nil
}

func (s *turnService) registerCredentialHandler(h host.Host) {
	h.SetStreamHandler(turnCredentialProtocol, s.handleCredentialStream)
}

func (s *turnService) handleCredentialStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))

	remoteID := stream.Conn().RemotePeer()
	response, err := s.credentialsForPeer(remoteID, time.Now())
	if err != nil {
		response = turnCredentialResponse{Error: err.Error()}
	}
	if err := json.NewEncoder(stream).Encode(response); err != nil {
		_ = stream.Reset()
	}
}

func (s *turnService) credentialsForPeer(id peer.ID, now time.Time) (turnCredentialResponse, error) {
	if id == "" {
		return turnCredentialResponse{}, errors.New("authenticated peer ID is required")
	}

	expiresAt := now.Add(s.config.CredentialTTL).UTC()
	username := strconv.FormatInt(expiresAt.Unix(), 10) + ":" + id.String()
	credential := s.password(username)
	address := net.JoinHostPort(s.config.PublicIP.String(), strconv.Itoa(s.config.ListenPort))

	return turnCredentialResponse{
		URLs: []string{
			fmt.Sprintf("stun:%s", address),
			fmt.Sprintf("turn:%s?transport=udp", address),
		},
		Username:   username,
		Credential: credential,
		ExpiresAt:  expiresAt,
	}, nil
}

func (s *turnService) authenticate(username, realm string, _ net.Addr) ([]byte, bool) {
	if realm != s.config.Realm || !validTURNCredentialUsername(username, time.Now()) {
		return nil, false
	}
	return turn.GenerateAuthKey(username, realm, s.password(username)), true
}

func validTURNCredentialUsername(username string, now time.Time) bool {
	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() >= expiresUnix {
		return false
	}
	_, err = peer.Decode(parts[1])
	return err == nil
}

func (s *turnService) password(username string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(username))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func allowTURNPeerIP(_ net.Addr, destination net.IP) bool {
	return destination != nil &&
		destination.IsGlobalUnicast() &&
		!destination.IsPrivate() &&
		!destination.IsLoopback() &&
		!destination.IsLinkLocalUnicast()
}

func (s *turnService) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}
