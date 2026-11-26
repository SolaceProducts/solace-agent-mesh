// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestContextCaller_FromIdentity(t *testing.T) {
	c := &Context{
		Identity: &auth.Identity{UserID: "sub-1", Claims: map[string]any{"email": "a@example.com"}},
		UserID:   "sub-1",
	}
	got := c.Caller()
	if got.Sub != "sub-1" || got.Email != "a@example.com" {
		t.Errorf("derived caller wrong: %+v", got)
	}
}

func TestContextCaller_BareUserID(t *testing.T) {
	got := (&Context{UserID: "u2"}).Caller()
	if got.Sub != "u2" || got.Email != "" {
		t.Errorf("bare-userID caller should carry only Sub: %+v", got)
	}
}

func TestContextCaller_SubTracksVerifiedIdentityNotBrokerUserID(t *testing.T) {
	got := (&Context{
		UserID:   "broker-user",
		Identity: &auth.Identity{UserID: "jwt-sub"},
	}).Caller()
	if got.Sub != "jwt-sub" {
		t.Errorf("Sub = %q, want verified subject %q", got.Sub, "jwt-sub")
	}
}

func TestFacadeCaller(t *testing.T) {
	got := (&Context{
		Identity: &auth.Identity{UserID: "sub-3", Claims: map[string]any{"email": "c@example.com"}},
	}).Facade().Caller()
	if got.Sub != "sub-3" || got.Email != "c@example.com" {
		t.Errorf("facade caller wrong: %+v", got)
	}
}
