// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"strings"
	"testing"
)

func TestXOAuth2Client_StartProducesSpecBytes(t *testing.T) {
	t.Parallel()

	c := newXoauth2Client("alice@example.com", "fake-bearer-token")

	mech, ir, err := c.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("mech = %q, want %q", mech, "XOAUTH2")
	}
	want := "user=alice@example.com\x01auth=Bearer fake-bearer-token\x01\x01"
	if string(ir) != want {
		t.Errorf("initial response = %q, want %q", ir, want)
	}
}

func TestXOAuth2Client_NextRejectsServerChallenge(t *testing.T) {
	t.Parallel()

	c := newXoauth2Client("alice@example.com", "fake-bearer-token")

	if _, _, err := c.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	resp, err := c.Next([]byte(`{"status":"401","schemes":"bearer","scope":"https://outlook.office365.com/.default"}`))
	if err == nil {
		t.Fatal("Next() returned nil error for server failure challenge; want non-nil")
	}
	if len(resp) != 0 {
		t.Errorf("Next() response = %q, want empty", resp)
	}
	if !strings.Contains(err.Error(), "xoauth2") {
		t.Errorf("error %q does not mention xoauth2", err.Error())
	}
}

func TestParseXoauth2Blob(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		blob := []byte("user=alice@example.com\x01auth=Bearer tok-123\x01\x01")
		user, token, err := parseXoauth2Blob(blob)
		if err != nil {
			t.Fatalf("parseXoauth2Blob() error = %v", err)
		}
		if user != "alice@example.com" {
			t.Errorf("user = %q, want %q", user, "alice@example.com")
		}
		if token != "tok-123" {
			t.Errorf("token = %q, want %q", token, "tok-123")
		}
	})

	cases := []struct {
		name string
		blob []byte
	}{
		{"empty", []byte{}},
		{"only user", []byte("user=alice@example.com\x01\x01")},
		{"only token", []byte("auth=Bearer tok\x01\x01")},
		{"wrong scheme", []byte("user=alice@example.com\x01auth=Basic tok\x01\x01")},
	}
	for _, tc := range cases {
		t.Run("malformed/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := parseXoauth2Blob(tc.blob); err == nil {
				t.Fatalf("parseXoauth2Blob(%q) = nil error; want error", tc.blob)
			}
		})
	}
}
