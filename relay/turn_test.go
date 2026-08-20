package main

import (
	"bytes"
	"crypto/rand"
	"net"
	"strconv"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/turn/v4"
)

func TestTURNCredentialsAuthenticate(t *testing.T) {
	_, publicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}

	service := &turnService{
		config: turnServerConfig{
			PublicIP:      net.ParseIP("203.0.113.10"),
			ListenPort:    3478,
			Realm:         "kwebbel",
			CredentialTTL: 10 * time.Minute,
		},
		secret: []byte("test secret"),
	}
	now := time.Now()
	credentials, err := service.credentialsForPeer(peerID, now)
	if err != nil {
		t.Fatal(err)
	}

	key, ok := service.authenticate(credentials.Username, service.config.Realm, nil)
	if !ok {
		t.Fatal("expected issued credentials to authenticate")
	}
	expected := turn.GenerateAuthKey(
		credentials.Username,
		service.config.Realm,
		credentials.Credential,
	)
	if !bytes.Equal(key, expected) {
		t.Fatal("TURN authentication key did not match issued credential")
	}
	if len(credentials.URLs) != 2 {
		t.Fatalf("expected STUN and TURN URLs, got %v", credentials.URLs)
	}
}

func TestTURNCredentialUsernameExpiry(t *testing.T) {
	_, publicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0)
	valid := now.Add(time.Minute).Unix()
	expired := now.Add(-time.Second).Unix()

	if !validTURNCredentialUsername(
		formatTURNUsername(valid, peerID),
		now,
	) {
		t.Fatal("expected unexpired credential username to be valid")
	}
	if validTURNCredentialUsername(
		formatTURNUsername(expired, peerID),
		now,
	) {
		t.Fatal("expected expired credential username to be rejected")
	}
}

func TestTURNPermissionRejectsPrivateDestinations(t *testing.T) {
	if allowTURNPeerIP(nil, net.ParseIP("10.0.0.1")) {
		t.Fatal("private destination should not be allowed")
	}
	if allowTURNPeerIP(nil, net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback destination should not be allowed")
	}
	if !allowTURNPeerIP(nil, net.ParseIP("1.1.1.1")) {
		t.Fatal("public destination should be allowed")
	}
}

func formatTURNUsername(expiresUnix int64, id peer.ID) string {
	return strconv.FormatInt(expiresUnix, 10) + ":" + id.String()
}
