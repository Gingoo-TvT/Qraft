package secureconfig

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCipherRoundTripAndTamperDetection(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	box, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}

	sealed, err := box.Seal("  sk-permanent-secret  ")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if strings.Contains(sealed, "sk-permanent-secret") {
		t.Fatal("ciphertext contains plaintext secret")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened != "sk-permanent-secret" {
		t.Fatalf("Open() = %q", opened)
	}

	version, payloadText, ok := strings.Cut(sealed, ":")
	if !ok {
		t.Fatalf("sealed value has no version separator: %q", sealed)
	}
	payload, err := base64.RawStdEncoding.DecodeString(payloadText)
	if err != nil {
		t.Fatalf("decode sealed payload: %v", err)
	}
	payload[len(payload)-1] ^= 0x01
	tampered := version + ":" + base64.RawStdEncoding.EncodeToString(payload)
	if _, err := box.Open(tampered); err == nil {
		t.Fatal("tampered ciphertext unexpectedly decrypted")
	}
}

func TestCipherRejectsWeakKeyAndEmptySecret(t *testing.T) {
	if _, err := NewCipher("short"); err == nil {
		t.Fatal("weak key unexpectedly accepted")
	}
	box, err := NewCipher(strings.Repeat("k", 32))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	if _, err := box.Seal(" "); err == nil {
		t.Fatal("empty plaintext unexpectedly accepted")
	}
}

func TestResolveSettingsSecretPreservesExplicitAndJWTFallbackCompatibility(t *testing.T) {
	explicit := strings.Repeat("e", 32)
	resolved, source, err := ResolveSettingsSecret(explicit, "ignored", false)
	if err != nil || resolved != explicit || source != "explicit" {
		t.Fatalf("explicit resolve = (%q,%q,%v)", resolved, source, err)
	}

	longJWT := strings.Repeat("j", 40)
	legacyCipher, err := NewCipher(longJWT)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := legacyCipher.Seal("stored-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	resolved, source, err = ResolveSettingsSecret("", longJWT, false)
	if err != nil || source != "jwt-derived" {
		t.Fatalf("long JWT resolve = (%q,%v)", source, err)
	}
	compatibleCipher, err := NewCipher(resolved)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := compatibleCipher.Open(ciphertext)
	if err != nil || plaintext != "stored-provider-key" {
		t.Fatalf("fallback compatibility = (%q,%v)", plaintext, err)
	}
}

func TestResolveSettingsSecretSupportsLegacyShortJWTAndExplicitLocalDev(t *testing.T) {
	shortResolved, source, err := ResolveSettingsSecret("", "short-jwt", false)
	if err != nil || source != "jwt-derived" {
		t.Fatalf("short JWT resolve = (%q,%v)", source, err)
	}
	if _, err := NewCipher(shortResolved); err != nil {
		t.Fatalf("short JWT fallback rejected: %v", err)
	}

	first, firstSource, err := ResolveSettingsSecret("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	second, secondSource, err := ResolveSettingsSecret("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if firstSource != "local-development" || secondSource != firstSource || first != second {
		t.Fatalf("local dev fallback drifted: (%q,%q) (%q,%q)", first, firstSource, second, secondSource)
	}
	if _, err := NewCipher(first); err != nil {
		t.Fatalf("local dev fallback rejected: %v", err)
	}
}

func TestResolveSettingsSecretRejectsEmptyProductionFallback(t *testing.T) {
	if _, _, err := ResolveSettingsSecret("", "", false); err == nil {
		t.Fatal("empty production settings secret was accepted")
	}
}

func TestResolveSettingsSecretPreservesEncodedJWTKeyCompatibility(t *testing.T) {
	rawKey := []byte(strings.Repeat("\xff", 32))
	variants := map[string]string{
		"std":     base64.StdEncoding.EncodeToString(rawKey),
		"raw-std": base64.RawStdEncoding.EncodeToString(rawKey),
		"raw-url": base64.RawURLEncoding.EncodeToString(rawKey),
	}
	for name, jwtSecret := range variants {
		t.Run(name, func(t *testing.T) {
			legacyCipher, err := NewCipher(jwtSecret)
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := legacyCipher.Seal("stored-provider-key")
			if err != nil {
				t.Fatal(err)
			}
			resolved, source, err := ResolveSettingsSecret("", jwtSecret, false)
			if err != nil || source != "jwt-derived" {
				t.Fatalf("encoded JWT resolve = (%q,%v)", source, err)
			}
			compatibleCipher, err := NewCipher(resolved)
			if err != nil {
				t.Fatal(err)
			}
			plaintext, err := compatibleCipher.Open(ciphertext)
			if err != nil || plaintext != "stored-provider-key" {
				t.Fatalf("encoded JWT fallback compatibility = (%q,%v)", plaintext, err)
			}
		})
	}
}
