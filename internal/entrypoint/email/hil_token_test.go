// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"
	"time"
)

func TestHILToken_RoundTrip(t *testing.T) {
	secret := []byte("super-secret-key")
	tok := hilToken{
		RequestID: "req-1", TaskID: "task-1", AgentName: "Agent", UserID: "u1",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	s, err := signHILToken(tok, secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, err := verifyHILToken(s, secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != tok {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, tok)
	}
}

func TestHILToken_WrongSecret(t *testing.T) {
	s, _ := signHILToken(hilToken{RequestID: "r", ExpiresAt: time.Now().Add(time.Hour).Unix()}, []byte("a"))
	if _, err := verifyHILToken(s, []byte("b")); err != errTokenInvalid {
		t.Errorf("err = %v, want errTokenInvalid", err)
	}
}

func TestHILToken_Tampered(t *testing.T) {
	s, _ := signHILToken(hilToken{RequestID: "r", ExpiresAt: time.Now().Add(time.Hour).Unix()}, []byte("a"))
	b := []byte(s)
	b[0] ^= 0x01
	if _, err := verifyHILToken(string(b), []byte("a")); err != errTokenInvalid {
		t.Errorf("err = %v, want errTokenInvalid", err)
	}
}

func TestHILToken_Expired(t *testing.T) {
	s, _ := signHILToken(hilToken{RequestID: "r", ExpiresAt: time.Now().Add(-time.Minute).Unix()}, []byte("a"))
	if _, err := verifyHILToken(s, []byte("a")); err != errTokenExpired {
		t.Errorf("err = %v, want errTokenExpired", err)
	}
}

func TestHILToken_Malformed(t *testing.T) {
	for _, s := range []string{"", "nodot", "a.b.c", "."} {
		if _, err := verifyHILToken(s, []byte("a")); err != errTokenInvalid {
			t.Errorf("verify(%q) err = %v, want errTokenInvalid", s, err)
		}
	}
}
