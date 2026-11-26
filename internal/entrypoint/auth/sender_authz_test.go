// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type recordingAuthzClient struct {
	res        AuthorizationResolution
	err        error
	lastUserID string
	lastCtx    map[string]any
	calls      int
}

func (c *recordingAuthzClient) Lookup(_ context.Context, userID string, gatewayContext map[string]any) (AuthorizationResolution, error) {
	c.calls++
	c.lastUserID = userID
	c.lastCtx = gatewayContext
	return c.res, c.err
}

func TestAuthorizeSenderPacksTheLookupInsideGatewayContext(t *testing.T) {
	client := &recordingAuthzClient{res: AuthorizationResolution{Resolution: identity.LookupMatched}}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{
		UserID:          "whatsapp:15551234567",
		ClaimKey:        "phone_number",
		ClaimValue:      "15551234567",
		ContinuityToken: "US.1234",
	}); err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}

	for key, want := range map[string]any{
		"gateway_id":           "wa-gw",
		"identity_claim_key":   "phone_number",
		"identity_claim_value": "15551234567",
		"continuity_token":     "US.1234",
	} {
		if got := client.lastCtx[key]; got != want {
			t.Errorf("gateway_context[%q] = %v, want %v — a field outside this map is invisible to both caches", key, got, want)
		}
	}
	if client.lastUserID != "whatsapp:15551234567" {
		t.Errorf("userID = %q, want the presented principal", client.lastUserID)
	}
}

func TestAuthorizeSenderOmitsAbsentLookupFieldsSoCacheKeysStayStable(t *testing.T) {
	client := &recordingAuthzClient{}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{UserID: "u"}); err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}
	for _, key := range []string{"identity_claim_key", "identity_claim_value", "continuity_token", "continuity_predecessor", "continuity_discard"} {
		if _, present := client.lastCtx[key]; present {
			t.Errorf("gateway_context carries %q for a request that named none", key)
		}
	}
}

func TestAuthorizeSenderCarriesTheRotationRemedy(t *testing.T) {
	t.Run("rebind names the predecessor", func(t *testing.T) {
		client := &recordingAuthzClient{}
		a := NewBrokerSenderAuthorizer(client, "wa-gw")
		if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{
			UserID: "u", ClaimKey: "phone_number", ClaimValue: "15551234567",
			ContinuityToken: "US.new", PredecessorToken: "US.old",
		}); err != nil {
			t.Fatalf("AuthorizeSender: %v", err)
		}
		if client.lastCtx["continuity_predecessor"] != "US.old" {
			t.Errorf("continuity_predecessor = %v, want US.old", client.lastCtx["continuity_predecessor"])
		}
		if _, present := client.lastCtx["continuity_discard"]; present {
			t.Error("a rebind also asked for a discard")
		}
	})

	t.Run("discard sets the flag", func(t *testing.T) {
		client := &recordingAuthzClient{}
		a := NewBrokerSenderAuthorizer(client, "wa-gw")
		if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{
			UserID: "u", ClaimKey: "phone_number", ClaimValue: "15551234567",
			ContinuityToken: "15551234567", DiscardBinding: true,
		}); err != nil {
			t.Fatalf("AuthorizeSender: %v", err)
		}
		if client.lastCtx["continuity_discard"] != true {
			t.Errorf("continuity_discard = %v, want true", client.lastCtx["continuity_discard"])
		}
	})
}

func TestAuthorizeSenderPassesTheResolutionThrough(t *testing.T) {
	client := &recordingAuthzClient{res: AuthorizationResolution{
		Resolution:     identity.LookupAmbiguous,
		ResolvedUserID: "u-1",
		Roles:          []string{"r"},
		Scopes:         []string{"agent:*:invoke"},
	}}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	got, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{UserID: "u"})
	if err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}
	if got.Resolution != identity.LookupAmbiguous || got.ResolvedUserID != "u-1" ||
		len(got.Roles) != 1 || len(got.Scopes) != 1 {
		t.Errorf("resolution = %+v, want the platform's answer verbatim", got)
	}
}

func TestAuthorizeSenderPropagatesTheError(t *testing.T) {
	client := &recordingAuthzClient{err: errors.New("broker timeout")}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{UserID: "u"}); err == nil {
		t.Fatal("a broker failure was swallowed; the caller would read it as a clean miss and refuse the sender")
	}
}

func TestNewBrokerSenderAuthorizerIsNilWithoutAClient(t *testing.T) {
	if a := NewBrokerSenderAuthorizer(nil, "wa-gw"); a != nil {
		t.Error("a nil client produced a live authorizer")
	}
}

func TestSenderAuthorizerEmitsTheWireKeysAsLiteralStrings(t *testing.T) {
	client := &recordingAuthzClient{res: AuthorizationResolution{Resolution: identity.LookupMatched}}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	if _, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{
		UserID:           "whatsapp:15551234567",
		ClaimKey:         "phone_number",
		ClaimValue:       "15551234567",
		ContinuityToken:  "US.NEW",
		PredecessorToken: "US.OLD",
		DiscardBinding:   true,
	}); err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}

	want := map[string]any{
		"gateway_id":             "wa-gw",
		"identity_claim_key":     "phone_number",
		"identity_claim_value":   "15551234567",
		"continuity_token":       "US.NEW",
		"continuity_predecessor": "US.OLD",
		"continuity_discard":     true,
	}
	for key, value := range want {
		got, present := client.lastCtx[key]
		if !present {
			t.Errorf("gateway_context is missing %q; the platform reads that key and admits the sender without it", key)
			continue
		}
		if got != value {
			t.Errorf("gateway_context[%q] = %v, want %v", key, got, value)
		}
	}
}

func TestSenderAuthorizationCarriesTheRotationSignal(t *testing.T) {
	client := &recordingAuthzClient{res: AuthorizationResolution{
		Resolution:      identity.LookupRefused,
		RefusalReason:   "sender_changed",
		RotationApplied: true,
	}}
	a := NewBrokerSenderAuthorizer(client, "wa-gw")

	res, err := a.AuthorizeSender(context.Background(), SenderAuthorizationRequest{UserID: "whatsapp:1"})
	if err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}
	if !res.RotationApplied {
		t.Error("RotationApplied was dropped in translation; a nil error is not a confirmation")
	}
	if res.RefusalReason != "sender_changed" {
		t.Errorf("RefusalReason = %q, want it carried so a withheld sender gets the sign-in offer", res.RefusalReason)
	}
}
