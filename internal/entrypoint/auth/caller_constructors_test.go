// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type fakeExpander struct {
	scopes    []string
	calls     int
	gotUserID string
	gotRoles  []string
}

func (f *fakeExpander) ResolveScopes(userID string, roles []string) []string {
	f.calls++
	f.gotUserID, f.gotRoles = userID, roles
	return f.scopes
}

type fakeLookup struct {
	ent          Entitlement
	err          error
	gotUserID    string
	gotGatewayCx map[string]any
}

func (f *fakeLookup) Lookup(_ context.Context, userID string, gatewayContext map[string]any) (Entitlement, error) {
	f.gotUserID, f.gotGatewayCx = userID, gatewayContext
	return f.ent, f.err
}

type fakeTokens struct {
	claims TokenClaims
	err    error
}

func (f *fakeTokens) Verify(string) (TokenClaims, error) { return f.claims, f.err }

func TestForSAMAccessToken_NoVerifierRefuses(t *testing.T) {
	if _, err := ForSAMAccessToken("tok", nil, &fakeExpander{}); !errors.Is(err, ErrNoTokenVerifier) {
		t.Fatalf("err = %v, want ErrNoTokenVerifier — an uncheckable credential must be refused, not trusted", err)
	}
}

func TestForSAMAccessToken_VerificationFailurePropagates(t *testing.T) {
	v := &fakeTokens{err: errors.New("bad signature")}
	_, err := ForSAMAccessToken("tok", v, &fakeExpander{})
	if !errors.Is(err, ErrCredentialRejected) {
		t.Fatalf("err = %v, want ErrCredentialRejected — callers tell a rejected credential from an unwired verifier by it", err)
	}
	if !strings.Contains(err.Error(), "bad signature") {
		t.Errorf("err = %v, want the verifier's reason kept", err)
	}
}

func TestForSAMAccessToken_PrefersBakedScopesAndSkipsTheExpander(t *testing.T) {
	exp := &fakeExpander{scopes: []string{"resolved:scope"}}
	v := &fakeTokens{claims: TokenClaims{UserID: "u1", Roles: []string{"r"}, Scopes: []string{"baked:scope"}}}

	id, err := ForSAMAccessToken("tok", v, exp)
	if err != nil {
		t.Fatalf("ForSAMAccessToken: %v", err)
	}
	if !slices.Equal(id.scopes, []string{"baked:scope"}) {
		t.Errorf("Scopes = %v, want the baked-in set", id.scopes)
	}
	if exp.calls != 0 {
		t.Errorf("expander called %d times, want 0 — the fast path must stay claim-read only", exp.calls)
	}
	if id.authMethod != AuthMethodSAMAccessToken {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodSAMAccessToken)
	}
}

func TestForSAMAccessToken_FallsBackToExpanderWithTheTokensRoles(t *testing.T) {
	exp := &fakeExpander{scopes: []string{"resolved:scope"}}
	v := &fakeTokens{claims: TokenClaims{UserID: "u1", Roles: []string{"admin"}}}

	id, err := ForSAMAccessToken("tok", v, exp)
	if err != nil {
		t.Fatalf("ForSAMAccessToken: %v", err)
	}
	if !slices.Equal(id.scopes, []string{"resolved:scope"}) {
		t.Errorf("Scopes = %v, want the expander's set", id.scopes)
	}
	if exp.gotUserID != "u1" || !slices.Equal(exp.gotRoles, []string{"admin"}) {
		t.Errorf("expander got (%q, %v), want (\"u1\", [admin])", exp.gotUserID, exp.gotRoles)
	}
}

func TestForSAMAccessToken_NoExpanderLeavesScopesEmptyWithoutFailing(t *testing.T) {
	v := &fakeTokens{claims: TokenClaims{UserID: "u1"}}

	id, err := ForSAMAccessToken("tok", v, nil)
	if err != nil {
		t.Fatalf("ForSAMAccessToken: %v", err)
	}
	if len(id.scopes) != 0 {
		t.Errorf("Scopes = %v, want empty", id.scopes)
	}
	if !id.authenticated {
		t.Error("Authenticated = false, want true — a verified token is still a verified token")
	}
}

func TestForIDPAccessToken_RejectsInvalidAndReservedIdentifiers(t *testing.T) {
	cases := map[string]struct {
		ext  ExternalID
		want error
	}{
		"empty value":     {ExternalID{Type: IDSub, Value: ""}, ErrInvalidExternalID},
		"unknown type":    {ExternalID{Type: IDType("ldap"), Value: "u1"}, ErrInvalidExternalID},
		"system claimant": {ExternalID{Type: IDSub, Value: "system:default"}, ErrReservedSystemPrincipal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ForIDPAccessToken(t.Context(), tc.ext, "", "", nil, &fakeLookup{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestForIDPAccessToken_LookupErrorPropagates(t *testing.T) {
	lk := &fakeLookup{err: errors.New("refusing to mint")}
	_, err := ForIDPAccessToken(t.Context(), ExternalID{Type: IDSub, Value: "u1"}, "", "", nil, lk)
	if err == nil {
		t.Fatal("err = nil, want the lookup failure to propagate rather than become an empty entitlement")
	}
}

func TestForIDPAccessToken_NoLookupRefuses(t *testing.T) {
	_, err := ForIDPAccessToken(t.Context(), ExternalID{Type: IDSub, Value: "u1"}, "", "", nil, nil)
	if !errors.Is(err, ErrNoEntitlementLookup) {
		t.Fatalf("err = %v, want ErrNoEntitlementLookup", err)
	}
}

func TestForIDPAccessToken_PassesGatewayContextAndCarriesEnrichment(t *testing.T) {
	gwCtx := map[string]any{"idp_claims": map[string]any{"groups": []string{"g1"}}}
	lk := &fakeLookup{ent: Entitlement{
		Roles:            []string{"viewer"},
		Scopes:           []string{"agent:*:invoke"},
		Profile:          &identity.Profile{ID: "u1"},
		EnrichmentStatus: identity.EnrichmentOK,
		EnrichmentReason: "",
	}}

	id, err := ForIDPAccessToken(t.Context(), ExternalID{Type: IDEmail, Value: "a@b.com"}, "a@b.com", "Alice", gwCtx, lk)
	if err != nil {
		t.Fatalf("ForIDPAccessToken: %v", err)
	}
	if lk.gotGatewayCx == nil {
		t.Fatal("lookup received a nil gatewayContext — idp_claims-derived roles would be lost")
	}
	if lk.gotUserID != "a@b.com" {
		t.Errorf("lookup userID = %q, want %q", lk.gotUserID, "a@b.com")
	}
	if !slices.Equal(id.roles, []string{"viewer"}) || !slices.Equal(id.scopes, []string{"agent:*:invoke"}) {
		t.Errorf("Roles/Scopes = %v/%v, want the lookup's entitlement", id.roles, id.scopes)
	}
	if id.name != "Alice" || id.email != "a@b.com" {
		t.Errorf("Name/Email = %q/%q, want Alice/a@b.com — both are supplied by the flow, not derived from the identifier", id.name, id.email)
	}
	if id.profile == nil || id.enrichmentStatus != identity.EnrichmentOK {
		t.Error("enrichment from the lookup did not reach the identity")
	}
	if id.authMethod != AuthMethodOIDC {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodOIDC)
	}
}

func TestForSyntheticMonitor_PrincipalIsUnprefixedAndRolesComeFromConfig(t *testing.T) {
	exp := &fakeExpander{scopes: []string{"agent:*:invoke"}}

	id := ForSyntheticMonitor([]string{"monitor"}, map[string]any{"appid": "abc", "is_synthetic": true}, exp)

	if id.userID != SyntheticUserID {
		t.Fatalf("UserID = %q, want %q", id.userID, SyntheticUserID)
	}
	if id.userID != "synthetic-monitor" {
		t.Errorf("UserID = %q — the principal must stay UNPREFIXED; prefixing changes the scope key every role assignment is written against", id.userID)
	}
	if exp.gotUserID != SyntheticUserID || !slices.Equal(exp.gotRoles, []string{"monitor"}) {
		t.Errorf("expander got (%q, %v), want (%q, [monitor]) — resolving with no roles would fall back to the deployment's default roles", exp.gotUserID, exp.gotRoles, SyntheticUserID)
	}
	if !slices.Equal(id.scopes, []string{"agent:*:invoke"}) {
		t.Errorf("Scopes = %v, want the expander's result", id.scopes)
	}
	if id.metadata["appid"] != "abc" || id.metadata["is_synthetic"] != true {
		t.Errorf("Metadata = %v, want the flow's own appid/is_synthetic pair", id.metadata)
	}
	if id.email != SyntheticUserEmail || id.name != SyntheticUserName {
		t.Errorf("Email/Name = %q/%q, want %q/%q", id.email, id.name, SyntheticUserEmail, SyntheticUserName)
	}
}

func TestForSyntheticMonitor_NoExpanderKeepsNilScopesStillAuthenticated(t *testing.T) {
	id := ForSyntheticMonitor([]string{"monitor"}, nil, nil)
	if id.scopes != nil {
		t.Errorf("Scopes = %v, want nil", id.scopes)
	}
	if !id.authenticated {
		t.Error("Authenticated = false, want true")
	}
}

func TestForSyntheticMonitor_ClonesItsInputs(t *testing.T) {
	roles := []string{"monitor"}
	meta := map[string]any{"appid": "abc"}

	id := ForSyntheticMonitor(roles, meta, nil)
	roles[0] = "mutated"
	meta["appid"] = "mutated"

	if id.roles[0] != "monitor" || id.metadata["appid"] != "abc" {
		t.Error("the identity aliases its caller's slice/map — config must not be mutable through a resolved caller")
	}
}

func TestForMcpDefaultCaller_PrincipalPrecedence(t *testing.T) {
	raw := ForMcpDefaultCaller(&McpDefaults{DefaultUserIdentity: "svc-account"}, nil, false)
	if raw.userID != "svc-account" {
		t.Errorf("UserID = %q, want the configured identity used RAW", raw.userID)
	}

	fallback := ForMcpDefaultCaller(&McpDefaults{RunAs: "mcp"}, nil, false)
	if fallback.userID != "system:mcp" {
		t.Errorf("UserID = %q, want run_as prefixed as a system user", fallback.userID)
	}

	noCfg := ForMcpDefaultCaller(nil, nil, false)
	if noCfg.userID != DevIdentity().userID {
		t.Errorf("UserID = %q, want the dev principal when there is no config at all", noCfg.userID)
	}
}

func TestForMcpDefaultCaller_ScopeLadder(t *testing.T) {
	withExpander := ForMcpDefaultCaller(&McpDefaults{RunAs: "mcp"}, &fakeExpander{scopes: []string{"agent:a:invoke"}}, true)
	if !slices.Equal(withExpander.scopes, []string{"agent:a:invoke"}) {
		t.Errorf("Scopes = %v, want the expander's answer", withExpander.scopes)
	}

	enforced := ForMcpDefaultCaller(&McpDefaults{RunAs: "mcp"}, nil, true)
	if enforced.scopes == nil || len(enforced.scopes) != 0 {
		t.Errorf("Scopes = %v, want an explicit empty set — enforced with no resolver must fail closed, not hand out the dev wildcard", enforced.scopes)
	}

	unenforced := ForMcpDefaultCaller(&McpDefaults{RunAs: "mcp"}, nil, false)
	if !slices.Equal(unenforced.scopes, []string{"*"}) {
		t.Errorf("Scopes = %v, want DevIdentity's \"*\" — this is the community/dev allow-all lever", unenforced.scopes)
	}
}

func TestForNoopDevCaller_IdentityPrecedence(t *testing.T) {
	base := ForNoopDevCaller("", DevHeaders{}, nil)
	if base.userID != DevIdentity().userID {
		t.Errorf("UserID = %q, want the DevIdentity baseline when nothing overrides it", base.userID)
	}

	effective := ForNoopDevCaller("configured", DevHeaders{}, nil)
	if effective.userID != "configured" || effective.email != "configured@dev.local" {
		t.Errorf("got %q/%q, want configured/configured@dev.local", effective.userID, effective.email)
	}

	header := ForNoopDevCaller("configured", DevHeaders{UserID: "hdr"}, nil)
	if header.userID != "hdr" || header.email != "hdr@dev.local" {
		t.Errorf("got %q/%q, want hdr/hdr@dev.local", header.userID, header.email)
	}

	both := ForNoopDevCaller("", DevHeaders{UserID: "hdr", Email: "real@corp.com"}, nil)
	if both.email != "real@corp.com" {
		t.Errorf("Email = %q, want the explicit header", both.email)
	}

	emailOnly := ForNoopDevCaller("", DevHeaders{Email: "real@corp.com"}, nil)
	if emailOnly.email == "real@corp.com" {
		t.Error("an email header without a matching id must be dropped — honouring it desyncs UserID and Email")
	}
}

func TestForNoopDevCaller_ScopeLadder(t *testing.T) {
	wildcard := ForNoopDevCaller("", DevHeaders{}, nil)
	if !slices.Equal(wildcard.scopes, []string{"*"}) {
		t.Errorf("Scopes = %v, want DevIdentity's \"*\" when no hook is wired", wildcard.scopes)
	}

	declined := ForNoopDevCaller("", DevHeaders{}, func(string, []string) ([]string, bool) { return nil, false })
	if !slices.Equal(declined.scopes, []string{"*"}) {
		t.Errorf("Scopes = %v, want \"*\" to stand when the hook declines", declined.scopes)
	}

	resolved := ForNoopDevCaller("", DevHeaders{}, func(string, []string) ([]string, bool) { return []string{"agent:a:invoke"}, true })
	if !slices.Equal(resolved.scopes, []string{"agent:a:invoke"}) {
		t.Errorf("Scopes = %v, want the hook's answer", resolved.scopes)
	}

	failClosed := func(string, []string) ([]string, bool) { return []string{}, true }
	empty := ForNoopDevCaller("", DevHeaders{}, failClosed)
	if len(empty.scopes) != 0 {
		t.Errorf("Scopes = %v, want the hook's explicit empty set", empty.scopes)
	}

	overridden := ForNoopDevCaller("", DevHeaders{Scopes: []string{"persona:scope"}}, failClosed)
	if !slices.Equal(overridden.scopes, []string{"persona:scope"}) {
		t.Errorf("Scopes = %v, want the header to win even over the fail-closed empty set", overridden.scopes)
	}
}

func TestWebhookAndEventMeshCallersAreNotInterchangeable(t *testing.T) {
	webhook := ForWebhookCaller("ops")
	em, err := ForEventMeshCaller(nil, "ops")
	if err != nil {
		t.Fatalf("ForEventMeshCaller: %v", err)
	}

	if webhook.userID != em.userID {
		t.Fatalf("UserID differs (%q vs %q) — both derive the same run_as principal", webhook.userID, em.userID)
	}
	if webhook.name != webhook.userID {
		t.Errorf("webhook Name = %q, want it to equal UserID", webhook.name)
	}
	if em.name != "" {
		t.Errorf("event-mesh Name = %q, want empty — it has never carried one, and Name reaches user properties", em.name)
	}
	if webhook.authMethod != AuthMethodWebhook || em.authMethod != AuthMethodEventMesh {
		t.Errorf("AuthMethod = %q/%q, want %q/%q", webhook.authMethod, em.authMethod, AuthMethodWebhook, AuthMethodEventMesh)
	}
}

func TestForWebhookCaller_AlwaysAPrefixedSystemPrincipal(t *testing.T) {
	named := ForWebhookCaller("ops")
	if named.userID != "system:ops" {
		t.Errorf("UserID = %q, want the run_as name prefixed", named.userID)
	}
	unset := ForWebhookCaller("")
	if !strings.HasPrefix(unset.userID, "system:") {
		t.Errorf("UserID = %q, want an unset run_as to resolve to the baked-in default system user, not a blank or fabricated principal", unset.userID)
	}
	if !named.authenticated {
		t.Error("Authenticated = false, want true")
	}
}

func TestForEventMeshCaller_ExternalIdentityIsUsedRaw(t *testing.T) {
	id, err := ForEventMeshCaller(&ExternalID{Type: IDSub, Value: "alice"}, "ops")
	if err != nil {
		t.Fatalf("ForEventMeshCaller: %v", err)
	}
	if id.userID != "alice" {
		t.Errorf("UserID = %q, want the message-derived identity, not the run_as principal", id.userID)
	}
}

func TestForEventMeshCaller_RejectsReservedPrincipalFromTheWire(t *testing.T) {
	_, err := ForEventMeshCaller(&ExternalID{Type: IDSub, Value: "system:default"}, "ops")
	if !errors.Is(err, ErrReservedSystemPrincipal) {
		t.Fatalf("err = %v, want ErrReservedSystemPrincipal", err)
	}
}

type recordingEnricher struct {
	gotUserID string
	calls     int
}

func (e *recordingEnricher) EnrichIdentity(_ context.Context, id *Subject) {
	e.calls++
	e.gotUserID = id.userID
	id.enrichmentStatus = identity.EnrichmentOK
	id.profile = &identity.Profile{ID: id.userID}
}

func TestForChatUser_EnrichesTheRealCallerBeforeTheSwap(t *testing.T) {
	enr := &recordingEnricher{}
	real := &Subject{userID: "alice@corp.com", email: "alice@corp.com", authenticated: true, authMethod: "slack"}

	id, swapped := ForChatUser(t.Context(), real, enr, ChannelPolicy{IsMultiParty: true, RunAs: "ops"})

	if !swapped {
		t.Fatal("swapped = false, want true on a shared surface")
	}
	if enr.gotUserID != "alice@corp.com" {
		t.Errorf("enricher saw %q, want the real caller — enrichment must not run on the swapped principal", enr.gotUserID)
	}
	if id.userID != "system:ops" {
		t.Errorf("authorizing UserID = %q, want the run-as principal", id.userID)
	}
	if real.profile == nil || real.enrichmentStatus != identity.EnrichmentOK {
		t.Error("the real caller was not enriched; presence and requesting_user read it")
	}
	if id.profile != nil {
		t.Errorf("swapped identity carries profile %+v, want none on a shared-surface task", id.profile)
	}
}

func TestForChatUser_ChannelPolicyThreeStates(t *testing.T) {
	cases := map[string]struct {
		policy      ChannelPolicy
		wantUserID  string
		wantSwapped bool
	}{
		"1:1 keeps the real user": {
			ChannelPolicy{IsMultiParty: false, RunAs: "ops"}, "alice", false,
		},
		"shared surface swaps": {
			ChannelPolicy{IsMultiParty: true, RunAs: "ops"}, "system:ops", true,
		},
		"flag opts a shared surface back to the real user": {
			ChannelPolicy{IsMultiParty: true, UseUserIdentityInChannels: true, RunAs: "ops"}, "alice", false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			real := &Subject{userID: "alice", authenticated: true}
			id, swapped := ForChatUser(t.Context(), real, nil, tc.policy)
			if id.userID != tc.wantUserID {
				t.Errorf("UserID = %q, want %q", id.userID, tc.wantUserID)
			}
			if swapped != tc.wantSwapped {
				t.Errorf("swapped = %v, want %v — callers key the shared-surface system note on this", swapped, tc.wantSwapped)
			}
		})
	}
}

func TestForChatUser_NoEnricherIsNotAFailure(t *testing.T) {
	real := &Subject{userID: "alice", authenticated: true}
	if id, _ := ForChatUser(t.Context(), real, nil, ChannelPolicy{}); id.userID != "alice" {
		t.Errorf("UserID = %q, want the real caller", id.userID)
	}
}

func TestForWhatsAppSender_EmptyRunAsKeepsTheRealSender(t *testing.T) {
	sender := &Subject{userID: "whatsapp:123", authenticated: true, authMethod: AuthMethodWhatsApp}

	id, swapped := ForWhatsAppSender(sender, "")

	if swapped {
		t.Error("swapped = true, want false")
	}
	if id.userID != "whatsapp:123" {
		t.Errorf("UserID = %q, want the real sender — an empty run_as must NOT resolve to the default system user on this flow", id.userID)
	}
}

func TestForWhatsAppSender_ConfiguredRunAsSwapsUnconditionally(t *testing.T) {
	sender := &Subject{userID: "whatsapp:123", authenticated: true, authMethod: AuthMethodWhatsApp}

	id, swapped := ForWhatsAppSender(sender, "ops")

	if !swapped {
		t.Fatal("swapped = false, want true")
	}
	if id.userID != "system:ops" {
		t.Errorf("UserID = %q, want the prefixed run-as principal", id.userID)
	}
	if id.authMethod != AuthMethodWhatsApp {
		t.Errorf("AuthMethod = %q, want the swap to carry the sender's method through", id.authMethod)
	}
	if id.email != "" {
		t.Error("the swapped principal must carry no email — it is not the sender")
	}
}

func TestForAuthorizedWhatsAppSender_CarriesTheAuthorizersAnswer(t *testing.T) {
	sender := &Subject{userID: "whatsapp:123", authenticated: true, authMethod: AuthMethodWhatsApp,
		metadata: map[string]any{"whatsapp_wa_id": "123"}}

	id := ForAuthorizedWhatsAppSender(sender, "alice@example.com", SenderAuthorization{
		ResolvedUserID: "u-1", Roles: []string{"user"}, Scopes: []string{"agent:a:invoke"},
		Profile: &identity.Profile{ID: "u-1"}, EnrichmentStatus: identity.EnrichmentOK,
	})

	if id.userID != "alice@example.com" || sender.userID != "whatsapp:123" {
		t.Errorf("UserID = %q (sender %q), want the matched principal on the caller and the sender untouched", id.userID, sender.userID)
	}
	if !slices.Equal(id.scopes, []string{"agent:a:invoke"}) || !slices.Equal(id.roles, []string{"user"}) {
		t.Errorf("Roles/Scopes = %v/%v, want the authorizer's", id.roles, id.scopes)
	}
	if !id.scopesResolved || id.profile == nil || id.enrichmentStatus != identity.EnrichmentOK {
		t.Errorf("scopesResolved=%v profile=%v status=%q, want the lookup recorded", id.scopesResolved, id.profile, id.enrichmentStatus)
	}
	if id.metadata[MetadataPlatformUserID] != "u-1" {
		t.Errorf("metadata[%s] = %v, want the resolved platform user", MetadataPlatformUserID, id.metadata[MetadataPlatformUserID])
	}
	if _, leaked := sender.metadata[MetadataPlatformUserID]; leaked {
		t.Error("the sender's metadata map was written through — the authorized caller must not alias it")
	}
}

func TestForAuthorizedWhatsAppSender_EmptyGrantIsStillResolved(t *testing.T) {
	sender := &Subject{userID: "whatsapp:123", authenticated: true}

	id := ForAuthorizedWhatsAppSender(sender, sender.userID, SenderAuthorization{})

	if !id.scopesResolved || len(id.scopes) != 0 {
		t.Errorf("scopesResolved=%v scopes=%v, want a resolved empty set", id.scopesResolved, id.scopes)
	}
	if _, ok := id.metadata[MetadataPlatformUserID]; ok {
		t.Error("no ResolvedUserID was reported, so none must be recorded")
	}
}

func TestForEmailSender_AddressIsBothUserIDAndEmail(t *testing.T) {
	id, err := ForEmailSender(ExternalID{Type: IDEmail, Value: "alice@corp.com"}, "Alice", false)
	if err != nil {
		t.Fatalf("ForEmailSender: %v", err)
	}
	if id.userID != "alice@corp.com" || id.email != "alice@corp.com" {
		t.Errorf("UserID/Email = %q/%q, want both to be the canonical address", id.userID, id.email)
	}
	if id.name != "Alice" {
		t.Errorf("Name = %q, want the display name", id.name)
	}
	if id.metadata["email_sender"] != "alice@corp.com" {
		t.Errorf("Metadata = %v, want email_sender set", id.metadata)
	}
	if id.authMethod != AuthMethodEmail {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodEmail)
	}
}

func TestForEmailSender_HasNoReservedPrincipalGuard(t *testing.T) {
	id, err := ForEmailSender(ExternalID{Type: IDEmail, Value: "system:default"}, "", false)
	if err != nil {
		t.Fatalf("err = %v, want the address admitted as the email flow always has", err)
	}
	if id.userID != "system:default" {
		t.Errorf("UserID = %q, want the address unchanged", id.userID)
	}
}

func TestForEmailSender_RejectsAnEmptyAddress(t *testing.T) {
	if _, err := ForEmailSender(ExternalID{Type: IDEmail, Value: ""}, "", false); !errors.Is(err, ErrInvalidExternalID) {
		t.Fatalf("err = %v, want ErrInvalidExternalID", err)
	}
}

func TestForEmailSender_RejectsANonEmailIdentifier(t *testing.T) {
	if _, err := ForEmailSender(ExternalID{Type: IDPhone, Value: "+15550005555"}, "", false); !errors.Is(err, ErrInvalidExternalID) {
		t.Fatalf("err = %v, want ErrInvalidExternalID", err)
	}
}

func TestForScheduledTaskOwner_EmptyScopesAreAHardError(t *testing.T) {
	lk := &fakeLookup{ent: Entitlement{Roles: []string{"viewer"}}}

	_, err := ForScheduledTaskOwner(t.Context(), "alice", lk)

	if !errors.Is(err, ErrNoEffectiveScopes) {
		t.Fatalf("err = %v, want ErrNoEffectiveScopes — an unattended task must fail, not run unscoped", err)
	}
}

func TestForScheduledTaskOwner_PassesNoGatewayContext(t *testing.T) {
	lk := &fakeLookup{ent: Entitlement{Scopes: []string{"agent:a:invoke"}}}

	id, err := ForScheduledTaskOwner(t.Context(), "alice", lk)
	if err != nil {
		t.Fatalf("ForScheduledTaskOwner: %v", err)
	}
	if lk.gotGatewayCx != nil {
		t.Errorf("gatewayContext = %v, want nil at fire time", lk.gotGatewayCx)
	}
	if id.authMethod != AuthMethodScheduledTask {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodScheduledTask)
	}
}

func TestForScheduledTaskOwner_DoesNotRejectAStoredSystemOwner(t *testing.T) {
	lk := &fakeLookup{ent: Entitlement{Scopes: []string{"agent:a:invoke"}}}

	if _, err := ForScheduledTaskOwner(t.Context(), "system:ops", lk); err != nil {
		t.Fatalf("err = %v, want nil — a stored owner id is not a message-derived identity", err)
	}
}

func TestForScheduledRunAs_BuildsTheSystemUserWithItsRow(t *testing.T) {
	const rowID = "0193a1c2-7d4e-7f00-8a00-000000000001"
	lk := &fakeLookup{ent: Entitlement{Roles: []string{"nightly"}, Scopes: []string{"agent:reporter:invoke"}, SystemUserID: rowID}}

	id, err := ForScheduledRunAs(t.Context(), "system:nightly", lk)
	if err != nil {
		t.Fatalf("ForScheduledRunAs: %v", err)
	}
	if id.UserID() != "system:nightly" || id.PlatformUserID() != rowID {
		t.Errorf("caller = (%q, %q), want the service account and its users.id", id.UserID(), id.PlatformUserID())
	}
	if !slices.Equal(id.Scopes(), []string{"agent:reporter:invoke"}) {
		t.Errorf("Scopes = %v, want the service account's own", id.Scopes())
	}
	if lk.gotUserID != "system:nightly" || lk.gotGatewayCx != nil {
		t.Errorf("looked up (%q, %v), want the service account with no gateway context", lk.gotUserID, lk.gotGatewayCx)
	}
	if id.authMethod != AuthMethodScheduledTask {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodScheduledTask)
	}
}

func TestForScheduledRunAs_BuiltInRowID(t *testing.T) {
	id, err := ForScheduledRunAs(t.Context(), rbac.DefaultSystemUserSub, &fakeLookup{ent: Entitlement{Scopes: []string{"agent:*:invoke"}}})
	if err != nil {
		t.Fatalf("ForScheduledRunAs: %v", err)
	}
	if id.PlatformUserID() != rbac.DefaultSystemUserID {
		t.Errorf("PlatformUserID = %q, want the seeded id", id.PlatformUserID())
	}
	const shadow = "0193a1c2-7d4e-7f00-8a00-000000000002"
	id, err = ForScheduledRunAs(t.Context(), rbac.DefaultSystemUserSub, &fakeLookup{ent: Entitlement{Scopes: []string{"agent:*:invoke"}, SystemUserID: shadow}})
	if err != nil {
		t.Fatalf("ForScheduledRunAs: %v", err)
	}
	if id.PlatformUserID() != shadow {
		t.Errorf("PlatformUserID = %q, want the reported row %q", id.PlatformUserID(), shadow)
	}
}

func TestForScheduledRunAs_FailsClosed(t *testing.T) {
	const rowID = "0193a1c2-7d4e-7f00-8a00-000000000001"
	cases := map[string]struct {
		principal string
		lookup    EntitlementLookup
		want      error
	}{
		"no users row":       {"system:gone", &fakeLookup{ent: Entitlement{Scopes: []string{"*:*:*"}, SystemUserRow: SystemUserRowMissing}}, ErrUnknownSystemUser},
		"no scopes":          {"system:idle", &fakeLookup{ent: Entitlement{SystemUserID: rowID}}, ErrNoEffectiveScopes},
		"no lookup":          {"system:idle", nil, ErrNoEntitlementLookup},
		"a person":           {"alice@example.com", &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}, ErrInvalidScheduledRunAs},
		"hidden built-in":    {rbac.EvalSystemUserSub, &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}, ErrInvalidScheduledRunAs},
		"bare prefix":        {"system:", &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}, ErrInvalidScheduledRunAs},
		"inner whitespace":   {"system:night ly", &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}, ErrInvalidScheduledRunAs},
		"over the size cap":  {"system:" + strings.Repeat("a", 255), &fakeLookup{ent: Entitlement{Scopes: []string{"s"}}}, ErrInvalidScheduledRunAs},
		"lookup unreachable": {"system:idle", &fakeLookup{err: errors.New("broker down")}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			id, err := ForScheduledRunAs(t.Context(), tc.principal, tc.lookup)
			if err == nil || id != nil {
				t.Fatalf("got (%v, %v), want a refusal", id, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestForScheduledRunAs_AnUnconfirmedRowIsNotARefusal(t *testing.T) {
	cases := []struct {
		name      string
		principal string
		ent       Entitlement
	}{
		{"the platform reports found with no id", "system:nightly", Entitlement{Scopes: []string{"agent:reporter:invoke"}, SystemUserRow: SystemUserRowFound}},
		{"the platform could not look the row up", "system:nightly", Entitlement{Scopes: []string{"agent:reporter:invoke"}, SystemUserRow: SystemUserRowUnavailable}},
		{"the platform reports a state this build does not know", "system:nightly", Entitlement{Scopes: []string{"agent:reporter:invoke"}, SystemUserRow: "archived"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := ForScheduledRunAs(t.Context(), c.principal, &fakeLookup{ent: c.ent})

			if id != nil {
				t.Errorf("ForScheduledRunAs(%q) built %v, want no caller", c.principal, id)
			}
			if !errors.Is(err, ErrSystemUserLookupUnavailable) {
				t.Errorf("ForScheduledRunAs(%q) error = %v, want ErrSystemUserLookupUnavailable", c.principal, err)
			}
			if !errors.Is(err, ErrIdentityLookupUnavailable) {
				t.Errorf("ForScheduledRunAs(%q) error = %v, want it to be an unavailable identity lookup, which send waits for", c.principal, err)
			}
			if IsScheduledRunAsRefusal(err) {
				t.Errorf("ForScheduledRunAs(%q) error %v is classed as a refusal, want a transient failure", c.principal, err)
			}
			if errors.Is(err, ErrUnknownSystemUser) {
				t.Errorf("ForScheduledRunAs(%q) error %v says the service account does not exist", c.principal, err)
			}
		})
	}
}

func TestScheduledIdentityLookups_ATransientFailureIsKeptApartFromARefusal(t *testing.T) {
	errBrokerDown := errors.New("broker down")
	failures := []struct {
		name                    string
		err                     error
		wantUnavailable         bool
		wantPlatformUnreachable bool
	}{
		{"the platform's lookup failed", fmt.Errorf("platform authz lookup: %w", ErrIdentityLookupUnavailable), true, false},
		{"the platform was unreachable", fmt.Errorf("platform authz lookup: %w", ErrIdentityPlatformUnreachable), true, true},
		{"the lookup timed out", fmt.Errorf("lookup: %w", context.DeadlineExceeded), true, false},
		{"the lookup was canceled", fmt.Errorf("lookup: %w", context.Canceled), true, false},
		{"an error nothing classifies", errBrokerDown, false, false},
	}
	for _, owner := range []bool{true, false} {
		name, isRefusal := "run-as", IsScheduledRunAsRefusal
		if owner {
			name, isRefusal = "owner", IsScheduledOwnerRefusal
		}
		for _, f := range failures {
			t.Run(name+": "+f.name, func(t *testing.T) {
				var id *Subject
				var err error
				if owner {
					id, err = ForScheduledTaskOwner(t.Context(), "alice", &fakeLookup{err: f.err})
				} else {
					id, err = ForScheduledRunAs(t.Context(), "system:nightly", &fakeLookup{err: f.err})
				}

				if id != nil {
					t.Errorf("%s lookup built %v, want no caller", name, id)
				}
				if got := errors.Is(err, ErrIdentityLookupUnavailable); got != f.wantUnavailable {
					t.Errorf("%s lookup unavailable = %v, want %v (err: %v)", name, got, f.wantUnavailable, err)
				}
				if !errors.Is(err, f.err) {
					t.Errorf("%s lookup error = %v, want it to keep the cause %v", name, err, f.err)
				}
				if got := errors.Is(err, ErrIdentityPlatformUnreachable); got != f.wantPlatformUnreachable {
					t.Errorf("%s lookup platform unreachable = %v, want %v (err: %v)", name, got, f.wantPlatformUnreachable, err)
				}
				if isRefusal(err) {
					t.Errorf("%s lookup error %v is classed as a refusal, want a transient failure", name, err)
				}
			})
		}
	}
}

func TestForScheduledRunAs_AnOlderPlatformIsToldToUpgradeNotWaitedFor(t *testing.T) {
	lk := &fakeLookup{ent: Entitlement{Scopes: []string{"agent:reporter:invoke"}}}

	id, err := ForScheduledRunAs(t.Context(), "system:nightly", lk)

	if id != nil {
		t.Errorf("ForScheduledRunAs(system:nightly) built %v, want no caller", id)
	}
	if !errors.Is(err, ErrSystemUserRowNotReported) {
		t.Errorf("ForScheduledRunAs(system:nightly) error = %v, want ErrSystemUserRowNotReported", err)
	}
	for name, got := range map[string]bool{
		"ErrSystemUserLookupUnavailable": errors.Is(err, ErrSystemUserLookupUnavailable),
		"ErrUnknownSystemUser":           errors.Is(err, ErrUnknownSystemUser),
		"IsScheduledRunAsRefusal":        IsScheduledRunAsRefusal(err),
	} {
		if got {
			t.Errorf("ForScheduledRunAs(system:nightly) error %v matches %s, want it kept apart", err, name)
		}
	}
}

func TestForScheduledRunAs_ABuiltInResolvesWhateverTheRowState(t *testing.T) {
	for _, state := range []SystemUserRow{"", SystemUserRowMissing, SystemUserRowUnavailable} {
		t.Run("row state "+string(state), func(t *testing.T) {
			id, err := ForScheduledRunAs(t.Context(), rbac.DefaultSystemUserSub, &fakeLookup{ent: Entitlement{Scopes: []string{"agent:*:invoke"}, SystemUserRow: state}})

			if err != nil {
				t.Fatalf("ForScheduledRunAs(%q): %v", rbac.DefaultSystemUserSub, err)
			}
			if id.PlatformUserID() != rbac.DefaultSystemUserID {
				t.Errorf("ForScheduledRunAs(%q) PlatformUserID = %q, want the seeded id %q", rbac.DefaultSystemUserSub, id.PlatformUserID(), rbac.DefaultSystemUserID)
			}
		})
	}
}

var resolutionRows = []struct {
	name            string
	resolution      identity.LookupResolution
	wantUnavailable bool
}{
	{"the platform could not resolve the user", identity.LookupDegraded, true},
	{"the platform reports a resolution this build does not know", "archived", true},
	{"the platform matched the user", identity.LookupMatched, false},
	{"the platform found no matching user", identity.LookupNoMatch, false},
	{"nothing resolved the user, as in-process modes", identity.LookupUnknown, false},
}

func TestForScheduledTaskOwner_ADegradedLookupIsNotASmallerEntitlement(t *testing.T) {
	for _, c := range resolutionRows {
		t.Run(c.name, func(t *testing.T) {
			lk := &fakeLookup{ent: Entitlement{Scopes: []string{"agent:*:invoke"}, Resolution: c.resolution, RolesSource: identity.RolesSourceAssigned}}

			id, err := ForScheduledTaskOwner(t.Context(), "alice", lk)

			if got := errors.Is(err, ErrIdentityLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ForScheduledTaskOwner(alice) unavailable = %v, want %v (err: %v)", got, c.wantUnavailable, err)
			}
			if c.wantUnavailable && (id != nil || IsScheduledOwnerRefusal(err)) {
				t.Errorf("ForScheduledTaskOwner(alice) = (%v, %v), want no caller and a transient failure, not a refusal", id, err)
			}
			if !c.wantUnavailable && (err != nil || id == nil) {
				t.Errorf("ForScheduledTaskOwner(alice) = (%v, %v), want the caller", id, err)
			}
		})
	}
}

func TestForScheduledRunAs_ADegradedLookupIsNotASmallerEntitlement(t *testing.T) {
	for _, principal := range []string{"system:nightly", rbac.DefaultSystemUserSub} {
		for _, c := range resolutionRows {
			t.Run(principal+": "+c.name, func(t *testing.T) {
				lk := &fakeLookup{ent: Entitlement{Scopes: []string{"agent:*:invoke"}, SystemUserID: "row-1", SystemUserRow: SystemUserRowFound, Resolution: c.resolution}}

				id, err := ForScheduledRunAs(t.Context(), principal, lk)

				if got := errors.Is(err, ErrIdentityLookupUnavailable); got != c.wantUnavailable {
					t.Errorf("ForScheduledRunAs(%q) unavailable = %v, want %v (err: %v)", principal, got, c.wantUnavailable, err)
				}
				if c.wantUnavailable && (id != nil || IsScheduledRunAsRefusal(err)) {
					t.Errorf("ForScheduledRunAs(%q) = (%v, %v), want no caller and a transient failure, not a refusal", principal, id, err)
				}
				if !c.wantUnavailable && (err != nil || id == nil) {
					t.Errorf("ForScheduledRunAs(%q) = (%v, %v), want the caller", principal, id, err)
				}
			})
		}
	}
}

func TestForScheduledTaskOwner_RejectsEmptyOwnerAndMissingLookup(t *testing.T) {
	if _, err := ForScheduledTaskOwner(t.Context(), "", &fakeLookup{}); !errors.Is(err, ErrInvalidExternalID) {
		t.Errorf("empty owner: err = %v, want ErrInvalidExternalID", err)
	}
	if _, err := ForScheduledTaskOwner(t.Context(), "alice", nil); !errors.Is(err, ErrNoEntitlementLookup) {
		t.Errorf("no lookup: err = %v, want ErrNoEntitlementLookup", err)
	}
}

func TestForDevMiddlewareCaller_IsNotTheNoopProviderFlow(t *testing.T) {
	dev := ForDevMiddlewareCaller(nil)
	if dev.userID != DevIdentity().userID {
		t.Errorf("UserID = %q, want the DevIdentity baseline", dev.userID)
	}
	if !slices.Equal(dev.scopes, []string{"*"}) {
		t.Errorf("Scopes = %v, want DevIdentity's \"*\"", dev.scopes)
	}

	overridden := ForDevMiddlewareCaller([]string{"analytics:_:read"})
	if !slices.Equal(overridden.scopes, []string{"analytics:_:read"}) {
		t.Errorf("Scopes = %v, want the X-SAM-Scopes override", overridden.scopes)
	}

	noop := ForNoopDevCaller("configured", DevHeaders{UserID: "hdr"}, func(string, []string) ([]string, bool) {
		return []string{"resolved:scope"}, true
	})
	if dev.userID == noop.userID {
		t.Error("the two dev flows produced the same caller; DevMiddleware must ignore the user overrides NoopProvider honours")
	}
}

func TestForPlatformAdminToken_PrefersBakedScopes(t *testing.T) {
	exp := &fakeExpander{scopes: []string{"resolved:scope"}}

	id := ForPlatformAdminToken(TokenClaims{
		UserID: "admin", Email: "admin@corp.com", Name: "Admin",
		Roles: []string{"platform-admin"}, Scopes: []string{"baked:scope"},
	}, exp)

	if !slices.Equal(id.scopes, []string{"baked:scope"}) {
		t.Errorf("Scopes = %v, want the baked-in set", id.scopes)
	}
	if exp.calls != 0 {
		t.Errorf("expander called %d times, want 0", exp.calls)
	}
	if id.email != "admin@corp.com" || id.name != "Admin" {
		t.Errorf("Email/Name = %q/%q — both are supplied by the flow's own trust-card view", id.email, id.name)
	}
	if !id.authenticated || id.authMethod != AuthMethodSAMAccessToken {
		t.Errorf("Authenticated/AuthMethod = %v/%q, want true/%q — the token was verified", id.authenticated, id.authMethod, AuthMethodSAMAccessToken)
	}
}

func TestForPlatformAdminToken_FallsBackWhenTokenCarriedNoScopes(t *testing.T) {
	exp := &fakeExpander{scopes: []string{"resolved:scope"}}

	id := ForPlatformAdminToken(TokenClaims{UserID: "admin", Roles: []string{"db-only-role"}}, exp)

	if !slices.Equal(id.scopes, []string{"resolved:scope"}) {
		t.Errorf("Scopes = %v, want the expander's set", id.scopes)
	}
	if exp.gotUserID != "admin" || !slices.Equal(exp.gotRoles, []string{"db-only-role"}) {
		t.Errorf("expander got (%q, %v), want (\"admin\", [db-only-role])", exp.gotUserID, exp.gotRoles)
	}
}

func TestForChatResponder_DoesNotEnrich(t *testing.T) {
	real := &Subject{userID: "alice", authenticated: true}

	id := ForChatResponder(real, ChannelPolicy{IsMultiParty: false, RunAs: "ops"})

	if id.userID != "alice" {
		t.Errorf("UserID = %q, want the real user on a 1:1", id.userID)
	}
	if real.profile != nil || real.enrichmentStatus != "" {
		t.Error("the HIL path enriched; it must not — the profile is already on the task being answered")
	}
}

func TestForChatResponder_MirrorsTheSubmitPathSwap(t *testing.T) {
	for name, policy := range map[string]ChannelPolicy{
		"shared surface":                     {IsMultiParty: true, RunAs: "ops"},
		"shared surface, user identity kept": {IsMultiParty: true, UseUserIdentityInChannels: true, RunAs: "ops"},
		"one-to-one":                         {IsMultiParty: false, RunAs: "ops"},
		"shared surface, no run_as":          {IsMultiParty: true},
	} {
		t.Run(name, func(t *testing.T) {
			real := &Subject{userID: "alice", authenticated: true}
			submitIdentity, _ := ForChatUser(t.Context(), real, nil, policy)
			responderIdentity := ForChatResponder(real, policy)
			if responderIdentity.userID != submitIdentity.userID {
				t.Errorf("responder authorizes as %q but the task ran as %q — the two must agree",
					responderIdentity.userID, submitIdentity.userID)
			}
		})
	}
}

func TestForEmailHILResponder_IsTheTokensUserWithScopesLeftToTheGate(t *testing.T) {
	id := ForEmailHILResponder("alice@corp.com")
	if id.userID != "alice@corp.com" || id.email != "alice@corp.com" {
		t.Errorf("UserID/Email = %q/%q, want the token's user", id.userID, id.email)
	}
	if !id.FullIdentity() || !id.authenticated || id.authMethod != AuthMethodEmail {
		t.Errorf("full/authenticated/method = %v/%v/%q, want an authenticated email caller",
			id.FullIdentity(), id.authenticated, id.authMethod)
	}
	if len(id.scopes) != 0 || id.scopesResolved {
		t.Errorf("Scopes/ScopesResolved = %v/%v, want none resolved here", id.scopes, id.scopesResolved)
	}
}

func TestForUserIDOnly_CarriesOnlyTheUserID(t *testing.T) {
	id := ForUserIDOnly("alice@corp.com")
	if id.userID != "alice@corp.com" {
		t.Errorf("UserID = %q, want the user id it was given", id.userID)
	}
	if id.FullIdentity() {
		t.Error("FullIdentity() = true; nothing vouched for this caller")
	}
	if len(id.scopes) != 0 {
		t.Errorf("Scopes = %v, want none — granting scopes here is a security change, not a migration", id.scopes)
	}
	if id.authenticated {
		t.Error("Authenticated = true; this caller was never authenticated")
	}
}

func TestForEvalSystemPrincipal_EmptyScopesAreAHardError(t *testing.T) {
	_, err := ForEvalSystemPrincipal("system:eval", "", rbac.Resolution{Roles: []string{"viewer"}})

	if !errors.Is(err, ErrNoEffectiveScopes) {
		t.Fatalf("err = %v, want ErrNoEffectiveScopes — an unattended run must fail, not publish unscoped", err)
	}
}

func TestForEvalSystemPrincipal_CarriesTheComposersEntitlement(t *testing.T) {
	res := rbac.Resolution{Roles: []string{"eval-runner"}, Scopes: []string{"agent:a:invoke"}}

	id, err := ForEvalSystemPrincipal("system:eval", "", res)
	if err != nil {
		t.Fatalf("ForEvalSystemPrincipal: %v", err)
	}
	if id.userID != "system:eval" {
		t.Errorf("UserID = %q, want the principal verbatim — it is the JWT subject", id.userID)
	}
	if !slices.Equal(id.roles, res.Roles) || !slices.Equal(id.scopes, res.Scopes) {
		t.Errorf("Roles/Scopes = %v/%v, want %v/%v", id.roles, id.scopes, res.Roles, res.Scopes)
	}
	if id.authMethod != AuthMethodEvalSystem {
		t.Errorf("AuthMethod = %q, want %q", id.authMethod, AuthMethodEvalSystem)
	}
	if !id.authenticated {
		t.Error("Authenticated = false; the platform resolved this principal against its own RBAC authority")
	}
}

func TestForEvalSystemPrincipal_DoesNotRejectAConfiguredSystemPrincipal(t *testing.T) {
	res := rbac.Resolution{Scopes: []string{"agent:a:invoke"}}

	if _, err := ForEvalSystemPrincipal("system:eval", "", res); err != nil {
		t.Fatalf("err = %v, want nil — an operator-configured principal is not a message-derived identity", err)
	}
}

func TestForGoogleChatSender_EmailIsThePrincipalAndAnEmptyMatchIsResolved(t *testing.T) {
	profile := &identity.Profile{}
	res := SenderAuthorization{Roles: []string{"user"}, Profile: profile, EnrichmentStatus: identity.EnrichmentOK}

	id := ForGoogleChatSender("ada@example.com", "Ada", "users/42", res)

	if id.UserID() != "ada@example.com" || id.Email() != "ada@example.com" || id.Name() != "Ada" {
		t.Errorf("UserID/Email/Name = %q/%q/%q, want the Workspace email as principal", id.UserID(), id.Email(), id.Name())
	}
	if !id.ScopesResolved() || len(id.Scopes()) != 0 {
		t.Errorf("ScopesResolved/Scopes = %v/%v, want an empty resolution marked resolved", id.ScopesResolved(), id.Scopes())
	}
	if id.AuthMethod() != AuthMethodGoogleChat || !id.Authenticated() || !id.FullIdentity() {
		t.Errorf("AuthMethod/Authenticated/FullIdentity = %q/%v/%v", id.AuthMethod(), id.Authenticated(), id.FullIdentity())
	}
	if id.Profile() != profile || id.EnrichmentStatus() != identity.EnrichmentOK {
		t.Error("the profile the platform returned with the match must be carried")
	}
	if id.Metadata()[metadataGoogleChatUser] != "users/42" {
		t.Errorf("Metadata = %v, want the Chat user resource name", id.Metadata())
	}
}

func TestForGoogleChatUser_SwapsOnASharedSpaceOnly(t *testing.T) {
	sender := ForGoogleChatSender("ada@example.com", "Ada", "users/42", SenderAuthorization{})

	id, swapped := ForGoogleChatUser(sender, ChannelPolicy{IsMultiParty: false, RunAs: "ops"})
	if swapped || id != sender {
		t.Errorf("1:1: swapped=%v id=%+v, want the sender itself", swapped, id)
	}

	id, swapped = ForGoogleChatUser(sender, ChannelPolicy{IsMultiParty: true})
	if !swapped || id.UserID() != rbac.SystemUserPrincipal("") {
		t.Errorf("shared space, empty run_as: swapped=%v UserID=%q, want the default system user", swapped, id.UserID())
	}
	if id.Email() != "" || id.AuthMethod() != AuthMethodGoogleChat {
		t.Errorf("swapped Email/AuthMethod = %q/%q, want no email and the sender's method", id.Email(), id.AuthMethod())
	}
}

func TestForGoogleChatResponder_FollowsTheTasksRecordedSwap(t *testing.T) {
	clicker := ForGoogleChatSender("ada@example.com", "Ada", "users/42", SenderAuthorization{})

	if id := ForGoogleChatResponder(clicker, false, "ops"); id != clicker {
		t.Errorf("unswapped task: id = %+v, want the clicker", id)
	}
	id := ForGoogleChatResponder(clicker, true, "ops")
	if id.UserID() != "system:ops" || id.AuthMethod() != AuthMethodGoogleChat {
		t.Errorf("swapped task: UserID/AuthMethod = %q/%q, want the run-as principal with the clicker's method", id.UserID(), id.AuthMethod())
	}
}

func TestForAPIKey_SystemPrincipalWithNoEmailAndAResolvedScopeSet(t *testing.T) {
	id := ForAPIKey(apikey.Resolution{KeyID: "key-1", OwnerID: "u-1", OwnerSub: "system:ci"})

	if id.UserID() != "system:ci" || id.Name() != "system:ci" || id.Email() != "" {
		t.Errorf("UserID/Name/Email = %q/%q/%q, want the owner subject and no email", id.UserID(), id.Name(), id.Email())
	}
	if id.CredentialID() != "key-1" || id.AuthMethod() != APIKeyAuthMethod || !id.Authenticated() || !id.FullIdentity() {
		t.Errorf("CredentialID/AuthMethod/Authenticated/FullIdentity = %q/%q/%v/%v",
			id.CredentialID(), id.AuthMethod(), id.Authenticated(), id.FullIdentity())
	}
	if !id.ScopesResolved() {
		t.Error("ScopesResolved = false; an empty set from the authority must not be backfilled")
	}
	if id.EnrichmentStatus() != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %q, want not-configured so the middleware does not look a profile up", id.EnrichmentStatus())
	}
}

func TestConstructorsDoNotAliasTheirInputSlices(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(roles, scopes []string) *Subject{
		"ForSAMAccessToken": func(roles, scopes []string) *Subject {
			id, _ := ForSAMAccessToken("tok", &fakeTokens{claims: TokenClaims{UserID: "u1", Roles: roles, Scopes: scopes}}, nil)
			return id
		},
		"ForIDPAccessToken": func(roles, scopes []string) *Subject {
			id, _ := ForIDPAccessToken(ctx, ExternalID{Type: IDSub, Value: "u1"}, "", "", nil, &fakeLookup{ent: Entitlement{Roles: roles, Scopes: scopes}})
			return id
		},
		"ForPlatformAdminToken": func(roles, scopes []string) *Subject {
			return ForPlatformAdminToken(TokenClaims{UserID: "admin", Roles: roles, Scopes: scopes}, nil)
		},
		"ForScheduledTaskOwner": func(roles, scopes []string) *Subject {
			id, _ := ForScheduledTaskOwner(ctx, "u1", &fakeLookup{ent: Entitlement{Roles: roles, Scopes: scopes}})
			return id
		},
		"ForEvalSystemPrincipal": func(roles, scopes []string) *Subject {
			id, _ := ForEvalSystemPrincipal("system:eval", "", rbac.Resolution{Roles: roles, Scopes: scopes})
			return id
		},
		"ForDevMiddlewareCaller": func(_, scopes []string) *Subject {
			return ForDevMiddlewareCaller(scopes)
		},
		"ForNoopDevCaller, header": func(_, scopes []string) *Subject {
			return ForNoopDevCaller("", DevHeaders{Scopes: scopes}, nil)
		},
		"ForNoopDevCaller, hook": func(_, scopes []string) *Subject {
			return ForNoopDevCaller("", DevHeaders{}, func(string, []string) ([]string, bool) { return scopes, true })
		},
		"ForSentinelMonitor": func(_, scopes []string) *Subject {
			return ForSentinelMonitor("sentinel-monitor", scopes)
		},
		"ForSAMAccessToken, expander": func(_, scopes []string) *Subject {
			id, _ := ForSAMAccessToken("tok", &fakeTokens{claims: TokenClaims{UserID: "u1"}}, &fakeExpander{scopes: scopes})
			return id
		},
		"ForPlatformAdminToken, expander": func(_, scopes []string) *Subject {
			return ForPlatformAdminToken(TokenClaims{UserID: "admin"}, &fakeExpander{scopes: scopes})
		},
		"ForMcpDefaultCaller, expander": func(_, scopes []string) *Subject {
			return ForMcpDefaultCaller(nil, &fakeExpander{scopes: scopes}, false)
		},
		"ForSyntheticMonitor": func(roles, scopes []string) *Subject {
			return ForSyntheticMonitor(roles, nil, &fakeExpander{scopes: scopes})
		},
		"ForAuthorizedWhatsAppSender": func(roles, scopes []string) *Subject {
			return ForAuthorizedWhatsAppSender(ForWhatsAppSenderIdentity("15550005555", "Alice"), "alice@example.com",
				SenderAuthorization{Roles: roles, Scopes: scopes})
		},
		"ForGoogleChatSender": func(roles, scopes []string) *Subject {
			return ForGoogleChatSender("ada@example.com", "Ada", "users/42", SenderAuthorization{Roles: roles, Scopes: scopes})
		},
		"ForAPIKey": func(_, scopes []string) *Subject {
			return ForAPIKey(apikey.Resolution{OwnerSub: "system:ci", Scopes: scopes})
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			roles, scopes := []string{"role"}, []string{"agent:a:invoke"}
			id := build(roles, scopes)
			roles[0], scopes[0] = "admin", "*"
			if slices.Contains(id.roles, "admin") || slices.Contains(id.scopes, "*") {
				t.Errorf("roles=%v scopes=%v changed with the constructor's input", id.roles, id.scopes)
			}
		})
	}
}

func TestConstructorsCarryThePlatformUserRowTheirFlowResolved(t *testing.T) {
	const row = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	must := func(id *Subject, err error) *Subject {
		t.Helper()
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		return id
	}
	ent := Entitlement{Scopes: []string{"agent:a:invoke"}, PlatformUserID: row}
	matched := SenderAuthorization{Resolution: identity.LookupMatched, ResolvedUserID: row}
	unmatched := SenderAuthorization{Resolution: identity.LookupUnknown, ResolvedUserID: row}

	for _, tc := range []struct {
		name         string
		id           *Subject
		wantRow      string
		wantVerified bool
	}{
		{"SAM access token", must(ForSAMAccessToken("tok", &fakeTokens{claims: TokenClaims{UserID: "u1", PlatformUserID: row}}, nil)), row, false},
		{"IdP access token", must(ForIDPAccessToken(t.Context(), ExternalID{Type: IDEmail, Value: "a@b.com"}, "a@b.com", "", nil, &fakeLookup{ent: ent})), row, false},
		{"scheduled task owner", must(ForScheduledTaskOwner(t.Context(), "alice", &fakeLookup{ent: ent})), row, false},
		{"API key owner", ForAPIKey(apikey.Resolution{KeyID: "key-1", OwnerID: row, OwnerSub: "system:ci"}), row, false},
		{"eval system principal", must(ForEvalSystemPrincipal("system:eval", row, rbac.Resolution{Scopes: []string{"agent:a:invoke"}})), row, false},
		{"verified email sender", must(ForEmailSender(ExternalID{Type: IDEmail, Value: "a@b.com"}, "", true)), "", true},
		{"unverified email sender", must(ForEmailSender(ExternalID{Type: IDEmail, Value: "a@b.com"}, "", false)), "", false},
		{"google chat sender the platform matched", ForGoogleChatSender("a@b.com", "", "users/1", matched), row, true},
		{"google chat sender the platform did not match", ForGoogleChatSender("a@b.com", "", "users/1", unmatched), "", true},
		{"whatsapp sender the platform matched", ForAuthorizedWhatsAppSender(ForWhatsAppSenderIdentity("15551234567", ""), "a@b.com", matched), row, false},
		{"slack user", ForSlackUser("a@b.com", "a@b.com", "U1", "T1"), "", true},
		{"teams user", ForTeamsUser("a@b.com", "a@b.com", "", "T1", "aad-1", "azure_ad_email"), "", true},
		{"sentinel monitor", ForSentinelMonitor("sentinel-monitor", []string{"s"}), rbac.DefaultSystemUserID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.id.platformUserID != tc.wantRow || tc.id.senderVerified != tc.wantVerified {
				t.Errorf("PlatformUserID/SenderVerified = %q/%v, want %q/%v", tc.id.platformUserID, tc.id.senderVerified, tc.wantRow, tc.wantVerified)
			}
		})
	}
}
