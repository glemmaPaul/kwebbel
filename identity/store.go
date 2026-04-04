package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// SaveEncrypted saves the 32-byte seed to a file, encrypted by password
func SaveEncrypted(filename string, seed []byte, password string) error {
	// 1. Derive an AES Key from the Password (using SHA256 for simplicity)
	// In production, use Argon2 or Scrypt for the password hashing!
	key := sha256.Sum256([]byte(password))

	// 2. Create Cipher Block
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}

	// 3. Create GCM (Galois/Counter Mode) - handles encryption + authentication
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	// 4. Create a unique Nonce (Number used once)
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	// 5. Encrypt (Seal)
	encryptedData := gcm.Seal(nonce, nonce, seed, nil) // Prepend nonce to data

	return os.WriteFile(filename, encryptedData, 0600)
}

// LoadEncrypted reads and decrypts the file
func LoadEncrypted(filename string, password string) ([]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, err
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	seed, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err // Wrong password or corrupted file
	}

	return seed, nil
}

// LoadSeed reads an identity seed from disk.
// Supported formats:
//   - raw binary 32-byte seed
//   - 64-char hex seed
//   - base64-encoded 32-byte seed
//   - encrypted seed file created by SaveEncrypted
func LoadSeed(filename string, password string) ([]byte, error) {
	if password != "" {
		return LoadEncrypted(filename, password)
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if len(data) == 32 {
		seed := make([]byte, 32)
		copy(seed, data)
		return seed, nil
	}

	trimmed := strings.TrimSpace(string(data))
	if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == 32 {
		return decoded, nil
	}

	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == 32 {
		return decoded, nil
	}

	seed, err := LoadEncrypted(filename, "")
	if err == nil {
		return seed, nil
	}

	return nil, fmt.Errorf("unsupported key file format: expected raw/hex/base64 32-byte seed or encrypted seed")
}
