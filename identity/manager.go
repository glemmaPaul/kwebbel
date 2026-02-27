package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/crypto/hkdf"
)

type IdentityManager struct {
	// The Master Secret (32 bytes).
	// NEVER share this. This allows you to regenerate any room key.
	masterSeed []byte
}

// NewIdentityManager generates a brand new identity (for first run)
func NewIdentityManager() (*IdentityManager, error) {
	seed := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, err
	}
	return &IdentityManager{masterSeed: seed}, nil
}

// LoadFromBytes loads an existing raw 32-byte seed (e.g. from disk/db)
func LoadFromBytes(seed []byte) (*IdentityManager, error) {
	if len(seed) != 32 {
		return nil, fmt.Errorf("invalid seed length: expected 32, got %d", len(seed))
	}
	return &IdentityManager{masterSeed: seed}, nil
}

// DeriveRoomKey generates a deterministic Libp2p Key for a specific room.
// Logic: NewKey = HKDF(MasterKey, Salt=nil, Info="room-name")
func (im *IdentityManager) DeriveRoomKey(id string, roomName string) (libp2pcrypto.PrivKey, error) {
	// 1. Setup HKDF (Standard Key Derivation)
	// Hash: SHA256
	// Secret: Your Master Seed
	// Salt: nil (optional, but nil is fine here)
	// Info: The context string (prevents key reuse across different parts of your app)
	info := []byte(fmt.Sprintf("voice-app-room:%s:%s", id, roomName))

	kdf := hkdf.New(sha256.New, im.masterSeed, nil, info)

	// 2. Read 32 bytes from the KDF to get the Room-Specific Seed
	roomSeed := make([]byte, 32)
	if _, err := io.ReadFull(kdf, roomSeed); err != nil {
		return nil, err
	}

	// 3. Create Ed25519 Key from that seed
	// This is the standard Go library key
	stdKey := ed25519.NewKeyFromSeed(roomSeed)

	// 4. Convert to Libp2p Key format
	// Libp2p has a helper to wrap standard Ed25519 keys
	privKey, _, err := libp2pcrypto.KeyPairFromStdKey(&stdKey)
	if err != nil {
		return nil, err
	}

	return privKey, nil
}

// Returns a representation of the master public key
func (im *IdentityManager) GetMasterPublicID() string {
	// Re-generate the standard Go key from the seed
	stdKey := ed25519.NewKeyFromSeed(im.masterSeed)

	// Convert to Libp2p Key
	// KeyPairFromStdKey returns (PrivKey, PubKey, error)
	// We only care about the Public Key (second return value) here
	_, pubKey, err := libp2pcrypto.KeyPairFromStdKey(&stdKey)
	if err != nil {
		return "error-generating-key"
	}

	// Convert Public Key -> Peer ID
	id, err := peer.IDFromPublicKey(pubKey)
	if err != nil {
		return "error-calculating-id"
	}

	return id.String()
}

// ExportMasterSeed returns the raw bytes to save to disk (encrypt this!)
func (im *IdentityManager) ExportMasterSeed() []byte {
	// Return a copy so external code can't modify internal state
	clone := make([]byte, 32)
	copy(clone, im.masterSeed)
	return clone
}
