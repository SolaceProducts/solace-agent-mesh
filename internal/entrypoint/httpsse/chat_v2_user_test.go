// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

const chatV2SuperAdminScope = "*"

func chatV2GetUser(t *testing.T, mux http.Handler, path string, userID string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userID != "" {
		req = withIdentity(req, userID, scopes...)
	}
	return chatV2Serve(mux, req)
}

func chatV2CapabilitiesPath(scopes string) string {
	return chatv2.Prefix + "/user/capabilities?" + url.Values{"scopes": {scopes}}.Encode()
}

func TestChatV2UserMatchesV1(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	const caller = "alice@example.com"
	v1 := chatV2RawBody(t, chatV2GetUser(t, mux, "/api/v1/user", caller, "agent:*:invoke"))
	v2 := decodeData[chatv2.ChatUser](t, chatV2GetUser(t, mux, chatv2.Prefix+"/user", caller, "agent:*:invoke"))

	if v1["username"] != v2.ID {
		t.Errorf("ID = %q, want v1 username %v", v2.ID, v1["username"])
	}
	if !reflect.DeepEqual(v1["email"], *v2.Email) {
		t.Errorf("email: v1 = %v, v2 = %v", v1["email"], *v2.Email)
	}
	if !reflect.DeepEqual(v1["scopes"], toAnySlice(v2.Scopes)) {
		t.Errorf("scopes: v1 = %v, v2 = %v", v1["scopes"], v2.Scopes)
	}
	if !reflect.DeepEqual(v1["roles"], toAnySlice(v2.Roles)) {
		t.Errorf("roles: v1 = %v, v2 = %v", v1["roles"], v2.Roles)
	}
	if v1["authMethod"] != v2.AuthMethod {
		t.Errorf("authMethod: v1 = %v, v2 = %q", v1["authMethod"], v2.AuthMethod)
	}
}

func TestChatV2UserAnswersForTheCallerOnly(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	alice := decodeData[chatv2.ChatUser](t, chatV2GetUser(t, mux, chatv2.Prefix+"/user", "alice@example.com", "agent:*:invoke"))
	bob := decodeData[chatv2.ChatUser](t, chatV2GetUser(t, mux, chatv2.Prefix+"/user", "bob@example.com", "session:_:read"))

	if alice.ID != "alice@example.com" || !reflect.DeepEqual(alice.Scopes, []string{"agent:*:invoke"}) {
		t.Errorf("alice got id %q scopes %v, want her own", alice.ID, alice.Scopes)
	}
	if bob.ID != "bob@example.com" || !reflect.DeepEqual(bob.Scopes, []string{"session:_:read"}) {
		t.Errorf("bob got id %q scopes %v, want his own", bob.ID, bob.Scopes)
	}
}

func TestChatV2UserWithoutACallerIs401(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2GetUser(t, mux, chatv2.Prefix+"/user", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/v2 status = %d, want 401: %s", rec.Code, rec.Body)
	}
	v1 := chatV2GetUser(t, mux, "/api/v1/user", "")
	if v1.Code != http.StatusOK {
		t.Errorf("/api/v1 status = %d, want 200 anonymous: %s", v1.Code, v1.Body)
	}
}

func TestChatV2UserScopesAndRolesAreNeverNull(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2GetUser(t, mux, chatv2.Prefix+"/user", "carol@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	data := chatV2Object(t, chatV2RawBody(t, rec), "data")
	for _, key := range []string{"scopes", "roles"} {
		if !reflect.DeepEqual(data[key], []any{}) {
			t.Errorf("%s = %v, want []", key, data[key])
		}
	}
}

func TestChatV2UserCapabilities(t *testing.T) {
	const query = "agent:*:invoke, ,session:_:read,agent:*:invoke"
	path := chatV2CapabilitiesPath(query)
	want := []chatv2.UserCapability{
		{Scope: "agent:*:invoke"},
		{Scope: "session:_:read"},
	}

	cases := map[string]struct {
		scopes      []string
		wantGranted []bool
	}{
		"universal grant":     {scopes: []string{chatV2SuperAdminScope}, wantGranted: []bool{true, true}},
		"only agent:*:invoke": {scopes: []string{"agent:*:invoke"}, wantGranted: []bool{true, false}},
	}
	mux := newCoverageAdapter(t).Mux()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := chatV2GetUser(t, mux, path, "dave@example.com", tc.scopes...)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
			}
			got := decodeData[chatv2.ChatUserCapabilities](t, rec)
			if len(got.Capabilities) != len(want) {
				t.Fatalf("capabilities = %+v, want %d entries", got.Capabilities, len(want))
			}
			v1 := chatV2RawBody(t, chatV2GetUser(t, mux, "/api/v1/user/capabilities?"+url.Values{"scopes": {query}}.Encode(), "dave@example.com", tc.scopes...))
			v1Caps := v1["capabilities"].(map[string]any)
			for i, c := range got.Capabilities {
				if c.Scope != want[i].Scope {
					t.Errorf("entry %d scope = %q, want %q", i, c.Scope, want[i].Scope)
				}
				if c.Granted != tc.wantGranted[i] {
					t.Errorf("%s granted = %v, want %v", c.Scope, c.Granted, tc.wantGranted[i])
				}
				if v1Caps[c.Scope] != c.Granted {
					t.Errorf("%s granted: v1 = %v, v2 = %v", c.Scope, v1Caps[c.Scope], c.Granted)
				}
			}
		})
	}
}

func TestChatV2UserCapabilitiesManageRbac(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	path := chatV2CapabilitiesPath("agent:*:invoke")

	ordinary := decodeData[chatv2.ChatUserCapabilities](t, chatV2GetUser(t, mux, path, "erin@example.com", "agent:*:invoke"))
	v1Ordinary := chatV2RawBody(t, chatV2GetUser(t, mux, "/api/v1/user/capabilities?scopes=agent:*:invoke", "erin@example.com", "agent:*:invoke"))
	if ordinary.ManageRbac || v1Ordinary["capabilities"].(map[string]any)["manageRbac"] != false {
		t.Errorf("ordinary caller manageRbac: v2 = %v, want false", ordinary.ManageRbac)
	}

	admin := decodeData[chatv2.ChatUserCapabilities](t, chatV2GetUser(t, mux, path, "root@example.com", chatV2SuperAdminScope))
	v1Admin := chatV2RawBody(t, chatV2GetUser(t, mux, "/api/v1/user/capabilities?scopes=agent:*:invoke", "root@example.com", chatV2SuperAdminScope))
	if !admin.ManageRbac || v1Admin["capabilities"].(map[string]any)["manageRbac"] != true {
		t.Errorf("superadmin manageRbac: v2 = %v, want true", admin.ManageRbac)
	}
}

func TestChatV2UserCapabilitiesWithoutScopesParam(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2GetUser(t, mux, chatv2.Prefix+"/user/capabilities", "frank@example.com", "agent:*:invoke")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	data := chatV2Object(t, chatV2RawBody(t, rec), "data")
	if !reflect.DeepEqual(data["capabilities"], []any{}) {
		t.Errorf("capabilities = %v, want []", data["capabilities"])
	}
	if _, ok := data["manageRbac"]; !ok {
		t.Errorf("manageRbac is omitted, want it present")
	}
}

func TestChatV2UserCapabilitiesRefusesMoreThanTheCapOfDistinctScopes(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	scopes := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("agent:a%d:invoke", i)
		}
		return strings.Join(parts, ",")
	}

	atCap := chatV2GetUser(t, mux, chatV2CapabilitiesPath(scopes(chatv2.MaxCapabilityScopes)), "gina@example.com", "agent:*:invoke")
	if atCap.Code != http.StatusOK {
		t.Errorf("status at the cap = %d, want 200: %s", atCap.Code, atCap.Body)
	}
	overCap := chatV2GetUser(t, mux, chatV2CapabilitiesPath(scopes(chatv2.MaxCapabilityScopes+1)), "gina@example.com", "agent:*:invoke")
	if overCap.Code != http.StatusUnprocessableEntity {
		t.Errorf("status over the cap = %d, want 422: %s", overCap.Code, overCap.Body)
	}
}

func TestChatV2UserCapabilitiesWithoutACallerIs401(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2GetUser(t, mux, chatV2CapabilitiesPath("agent:*:invoke"), "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", rec.Code, rec.Body)
	}
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
