package secureconfig

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const (
	ciphertextVersion           = "v1"
	settingsPassphraseDomain    = "algoforge:llm-provider-settings:v1\x00"
	localDevelopmentKeyMaterial = "algoforge:local-development-settings:v1"
)

// Cipher encrypts small configuration secrets using AES-256-GCM. The encoded
// value is versioned so a future key/cipher migration can fail closed.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher accepts either a base64-encoded 32-byte key or a passphrase of at
// least 32 bytes. Passphrases are domain-separated before use as an AES key.
func NewCipher(secret string) (*Cipher, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, fmt.Errorf("settings encryption key is required")
	}

	key := decodeKey(secret)
	if len(key) == 0 {
		if len(secret) < 32 {
			return nil, fmt.Errorf("settings encryption key must contain at least 32 bytes or encode 32 random bytes")
		}
		digest := sha256.Sum256([]byte(settingsPassphraseDomain + secret))
		key = digest[:]
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create settings cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create settings AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// ResolveSettingsSecret preserves the historical long-JWT fallback key while
// allowing legacy short JWT deployments to boot without weakening the
// explicit ALGOFORGE_SETTINGS_ENCRYPTION_KEY contract. An empty deterministic
// fallback exists only for explicit local development mode.
func ResolveSettingsSecret(explicitSecret, jwtSecret string, devMode bool) (string, string, error) {
	if explicitSecret = strings.TrimSpace(explicitSecret); explicitSecret != "" {
		return explicitSecret, "explicit", nil
	}
	if jwtSecret = strings.TrimSpace(jwtSecret); jwtSecret != "" {
		if len(jwtSecret) >= 32 {
			return jwtSecret, "jwt-derived", nil
		}
		digest := sha256.Sum256([]byte(settingsPassphraseDomain + jwtSecret))
		return base64.RawStdEncoding.EncodeToString(digest[:]), "jwt-derived", nil
	}
	if !devMode {
		return "", "", fmt.Errorf("settings encryption key is required when JWT secret is empty")
	}
	digest := sha256.Sum256([]byte(localDevelopmentKeyMaterial))
	return base64.RawStdEncoding.EncodeToString(digest[:]), "local-development", nil
}

func decodeKey(value string) []byte {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(value)
		if err == nil && len(decoded) == 32 {
			return decoded
		}
	}
	return nil
}

// Seal encrypts a non-empty secret and returns a self-describing string.
func (c *Cipher) Seal(plaintext string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("settings cipher is unavailable")
	}
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", fmt.Errorf("configuration secret must not be empty")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate settings nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), []byte(ciphertextVersion))
	payload := append(nonce, sealed...)
	return ciphertextVersion + ":" + base64.RawStdEncoding.EncodeToString(payload), nil
}

// Open decrypts a value produced by Seal.
func (c *Cipher) Open(encoded string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("settings cipher is unavailable")
	}
	version, payloadText, ok := strings.Cut(strings.TrimSpace(encoded), ":")
	if !ok || version != ciphertextVersion {
		return "", fmt.Errorf("unsupported settings ciphertext version")
	}
	payload, err := base64.RawStdEncoding.DecodeString(payloadText)
	if err != nil {
		return "", fmt.Errorf("decode settings ciphertext: %w", err)
	}
	if len(payload) <= c.aead.NonceSize() {
		return "", fmt.Errorf("settings ciphertext is truncated")
	}
	nonce := payload[:c.aead.NonceSize()]
	sealed := payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, sealed, []byte(ciphertextVersion))
	if err != nil {
		return "", fmt.Errorf("decrypt settings ciphertext: %w", err)
	}
	return string(plaintext), nil
}
