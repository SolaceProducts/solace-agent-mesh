// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hmacutil

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestSumMatchesRFC4231Vector(t *testing.T) {
	secret := []byte("Jefe")
	data := []byte("what do ya want for nothing?")
	want, err := hex.DecodeString("5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843")
	if err != nil {
		t.Fatalf("decode expected tag: %v", err)
	}
	if got := Sum(secret, data); !bytes.Equal(got, want) {
		t.Errorf("Sum = %x, want %x", got, want)
	}
}

func TestValid(t *testing.T) {
	secret := []byte("app-secret")
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	tag := Sum(secret, body)

	tests := []struct {
		name   string
		secret []byte
		data   []byte
		tag    []byte
		want   bool
	}{
		{"accepts genuine tag", secret, body, tag, true},
		{"rejects wrong secret", []byte("other-secret"), body, tag, false},
		{"rejects tampered data", secret, []byte(`{"object":"tampered"}`), tag, false},
		{"rejects truncated tag", secret, body, tag[:len(tag)-1], false},
		{"rejects empty tag", secret, body, nil, false},
		{"accepts empty data", secret, nil, Sum(secret, nil), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Valid(tt.secret, tt.data, tt.tag); got != tt.want {
				t.Errorf("Valid = %v, want %v", got, tt.want)
			}
		})
	}
}
