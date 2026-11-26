// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package atrest

import (
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	a, err := New("secret", "p1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := a.Seal([]byte("token"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := New("secret", "p1")
	got, err := b.Open(sealed)
	if err != nil || string(got) != "token" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestOtherPurposeOrSecretCannotOpen(t *testing.T) {
	a, _ := New("secret", "p1")
	sealed, _ := a.Seal([]byte("token"))
	for _, c := range []struct{ secret, purpose string }{{"secret", "p2"}, {"other", "p1"}, {"", "p1"}} {
		b, err := New(c.secret, c.purpose)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Open(sealed); err == nil {
			t.Errorf("Open with %+v succeeded", c)
		}
	}
}

func TestEmptySecretGivesWorkingProcessKey(t *testing.T) {
	a, err := New("", "p1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := a.Seal([]byte("x"))
	if got, err := a.Open(sealed); err != nil || string(got) != "x" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestErrors(t *testing.T) {
	if _, err := New("s", ""); err == nil {
		t.Error("empty purpose accepted")
	}
	a, _ := New("s", "p")
	if _, err := a.Open("!!!"); err == nil {
		t.Error("bad base64 accepted")
	}
}

func TestSessionSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET_KEY", "env")
	if SessionSecret("cfg") != "cfg" || SessionSecret("") != "env" {
		t.Error("SessionSecret precedence wrong")
	}
	if Hash("a") == Hash("b") || len(Hash("a")) != 64 {
		t.Error("Hash wrong")
	}
}
