// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("super-secret-api-token-12345")

	ct, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip failed: got %q, want %q", got, plaintext)
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	key := testKey(t)

	ct, err := Encrypt(key, []byte{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 0 {
		t.Errorf("expected empty plaintext, got %d bytes", len(got))
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := testKey(t)
	key2 := testKey(t)

	ct, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Decrypt(key2, ct)
	if err == nil {
		t.Error("expected decryption to fail with wrong key")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := testKey(t)

	ct, err := Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	ct[len(ct)-1] ^= 0xff

	_, err = Decrypt(key, ct)
	if err == nil {
		t.Error("expected decryption to fail with tampered ciphertext")
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("same-input")

	ct1, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	ct2, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of same plaintext should differ (random nonce)")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := testKey(t)

	_, err := Decrypt(key, []byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for too-short ciphertext")
	}
}
