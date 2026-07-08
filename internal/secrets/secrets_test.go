package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateKey_GeneratesNewKeyWithCorrectPermissions(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "secret.key")

	key, err := LoadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(key) != keySize {
		t.Fatalf("expected a %d-byte key, got %d", keySize, len(key))
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected key file to exist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0600, got %o", perm)
	}
}

func TestLoadOrCreateKey_LoadsExistingKeyUnchanged(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "secret.key")

	first, err := LoadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := LoadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("unexpected error on second load: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("expected the same key to be loaded on a second call, got a different one")
	}
}

func TestLoadOrCreateKey_RejectsCorruptKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(keyPath, []byte("too short"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := LoadOrCreateKey(keyPath); err == nil {
		t.Fatal("expected an error for a key file of the wrong size")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	store, err := New(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	plaintext := "sk-super-secret-api-key-12345"
	encrypted, err := store.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("unexpected error encrypting: %v", err)
	}
	if encrypted == plaintext {
		t.Fatal("encrypted value should not equal the plaintext")
	}

	decrypted, err := store.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("unexpected error decrypting: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestEncrypt_DifferentEachTime(t *testing.T) {
	store, _ := New(make([]byte, keySize))
	a, _ := store.Encrypt("same plaintext")
	b, _ := store.Encrypt("same plaintext")
	if a == b {
		t.Fatal("expected two encryptions of the same plaintext to differ (random nonce), got identical ciphertext")
	}
}

func TestDecrypt_FailsWithWrongKey(t *testing.T) {
	storeA, _ := New(make([]byte, keySize))
	keyB := make([]byte, keySize)
	keyB[0] = 1 // different from storeA's all-zero key
	storeB, _ := New(keyB)

	encrypted, err := storeA.Encrypt("secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := storeB.Decrypt(encrypted); err == nil {
		t.Fatal("expected decryption with the wrong key to fail")
	}
}

func TestDecrypt_FailsOnTamperedCiphertext(t *testing.T) {
	store, _ := New(make([]byte, keySize))
	encrypted, err := store.Encrypt("secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tampered := []byte(encrypted)
	tampered[len(tampered)-1] ^= 0xFF // flip the last base64 char's underlying bits
	if _, err := store.Decrypt(string(tampered)); err == nil {
		t.Fatal("expected decryption of tampered ciphertext to fail")
	}
}

func TestDecrypt_FailsOnMalformedInput(t *testing.T) {
	store, _ := New(make([]byte, keySize))
	if _, err := store.Decrypt("not valid base64!!!"); err == nil {
		t.Fatal("expected an error for malformed input")
	}
}

func TestNew_RejectsWrongKeySize(t *testing.T) {
	if _, err := New([]byte("too short")); err == nil {
		t.Fatal("expected an error for a key that isn't 32 bytes")
	}
}
