package identity

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	AppDir      = ".kwebbel"
	IdentityDir = "identity"
	KeyFile     = "privkey"
)

type KeyStore struct {
	basePath string
}

func NewKeyStore() (*KeyStore, error) {
	// 1. Get User Home Directory (Works on Windows/Mac/Linux)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not find home directory: %v", err)
	}

	// 2. Construct the full path: ~/.kwebbel/identity/
	basePath := filepath.Join(home, AppDir, IdentityDir)

	return &KeyStore{basePath: basePath}, nil
}

// GetKeyPath returns the full path to the privkey file
func (ks *KeyStore) GetKeyPath() string {
	return filepath.Join(ks.basePath, KeyFile)
}

// EnsureDir makes sure the directory structure exists
func (ks *KeyStore) EnsureDir() error {
	// os.MkdirAll creates parents too. 0700 = Only this user can read/write.
	return os.MkdirAll(ks.basePath, 0700)
}

// LoadOrCreate attempts to load the key, or creates a new one if missing
func (ks *KeyStore) LoadOrCreate(password string) (*IdentityManager, error) {
	path := ks.GetKeyPath()

	// 1. Try to Load
	if _, err := os.Stat(path); err == nil {
		// File exists, decrypt it
		seed, err := LoadEncrypted(path, password)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt key (wrong password?): %v", err)
		}
		return LoadFromBytes(seed)
	}

	// 2. File doesn't exist, Create New
	fmt.Println("No identity found. Generating new master key...")
	im, err := NewIdentityManager()
	if err != nil {
		return nil, err
	}

	// 3. Ensure folder exists and Save
	if err := ks.EnsureDir(); err != nil {
		return nil, err
	}

	err = SaveEncrypted(path, im.ExportMasterSeed(), password)
	if err != nil {
		return nil, err
	}

	fmt.Printf("New identity saved to: %s\n", path)
	return im, nil
}
