// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func makeJSONLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestSoftSubsetAssertBodyAbsent(t *testing.T) {
	id := &auth.Identity{Scopes: []string{"tool:foo"}}
	var buf bytes.Buffer
	applySoftSubsetAssert(id, "", "task-1", "", "u-1", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, []string{"tool:foo"}) {
		t.Errorf("Scopes = %v, want unchanged JWT scopes", id.Scopes)
	}
	if buf.Len() != 0 {
		t.Errorf("body absent must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertBodyEqualsJWT(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(jwtScopes), "task-1", "", "u-1", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want %v", id.Scopes, jwtScopes)
	}
	if buf.Len() != 0 {
		t.Errorf("body == JWT must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertBodyNarrowsJWT(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar", "tool:baz"}
	bodyScopes := []string{"tool:foo"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(bodyScopes), "task-1", "", "u-1", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, bodyScopes) {
		t.Errorf("Scopes = %v, want narrowed body %v", id.Scopes, bodyScopes)
	}
	if buf.Len() != 0 {
		t.Errorf("legitimate narrowing must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertBodyWidensJWT(t *testing.T) {
	jwtScopes := []string{"tool:foo"}
	inflated := []string{"tool:foo", "agent:Stripe:invoke"}
	id := &auth.Identity{UserID: "u-1", Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(inflated), "task-1", "agent-A", "u-1", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want JWT scopes %v (body must be dropped)", id.Scopes, jwtScopes)
	}
	logged := buf.String()
	if !strings.Contains(logged, `"event":"capability_widening_blocked"`) {
		t.Errorf("expected capability_widening_blocked event, got: %s", logged)
	}
	if !strings.Contains(logged, `"taskID":"task-1"`) {
		t.Errorf("expected taskID field, got: %s", logged)
	}
	if !strings.Contains(logged, `"delegatingAgentName":"agent-A"`) {
		t.Errorf("expected delegatingAgentName field, got: %s", logged)
	}
}

func TestSoftSubsetAssertOrthogonalWidening(t *testing.T) {
	jwtScopes := []string{"tool:foo"}
	body := []string{"tool:bar"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(body), "t", "", "u", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want JWT scopes %v", id.Scopes, jwtScopes)
	}
	if !strings.Contains(buf.String(), `"event":"capability_widening_blocked"`) {
		t.Errorf("orthogonal widening must WARN; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertMixedNarrowingAndWidening(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar"}
	body := []string{"tool:foo", "agent:Stripe:invoke"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(body), "t", "", "u", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want JWT scopes %v", id.Scopes, jwtScopes)
	}
	if !strings.Contains(buf.String(), `"event":"capability_widening_blocked"`) {
		t.Errorf("mixed narrowing+widening must WARN; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertWildcardJWT(t *testing.T) {
	jwtScopes := []string{"tool:*"}
	body := []string{"tool:artifact:create"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities(body), "t", "", "u", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, body) {
		t.Errorf("Scopes = %v, want %v (body narrows under JWT wildcard)", id.Scopes, body)
	}
	if buf.Len() != 0 {
		t.Errorf("wildcard JWT covering body must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertEmptyBodyArray(t *testing.T) {
	jwtScopes := []string{"tool:foo"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, makeUserConfigWithCapabilities([]string{}), "t", "", "u", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want %v", id.Scopes, jwtScopes)
	}
	if buf.Len() != 0 {
		t.Errorf("empty body array must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertNilIdentity(t *testing.T) {
	var buf bytes.Buffer
	applySoftSubsetAssert(nil, makeUserConfigWithCapabilities([]string{"tool:foo"}), "t", "", "u", makeJSONLogger(&buf))
	if buf.Len() != 0 {
		t.Errorf("nil identity must not log; got: %s", buf.String())
	}
}

func TestSoftSubsetAssertMalformedJSON(t *testing.T) {
	jwtScopes := []string{"tool:foo"}
	id := &auth.Identity{Scopes: append([]string(nil), jwtScopes...)}
	var buf bytes.Buffer

	applySoftSubsetAssert(id, "{not-json", "t", "", "u", makeJSONLogger(&buf))

	if !reflect.DeepEqual(id.Scopes, jwtScopes) {
		t.Errorf("Scopes = %v, want JWT scopes preserved on malformed JSON", id.Scopes)
	}
	if buf.Len() != 0 {
		t.Errorf("malformed JSON must not log a widening event; got: %s", buf.String())
	}
}

func TestParseEnterpriseCapabilitiesScopes(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   []string
		wantOK bool
	}{
		{"empty input", "", nil, false},
		{"missing field", `{"foo":"bar"}`, nil, false},
		{"empty array", `{"_enterprise_capabilities":[]}`, []string{}, true},
		{"populated", `{"_enterprise_capabilities":["a","b"]}`, []string{"a", "b"}, true},
		{"non-array", `{"_enterprise_capabilities":"oops"}`, nil, false},
		{"malformed", `{not-json`, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseEnterpriseCapabilitiesScopes(tc.input)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if tc.wantOK && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("scopes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsScopeSubset(t *testing.T) {
	tests := []struct {
		name string
		body []string
		jwt  []string
		want bool
	}{
		{"empty body", nil, []string{"tool:foo"}, true},
		{"identical", []string{"a"}, []string{"a"}, true},
		{"narrowing", []string{"a"}, []string{"a", "b"}, true},
		{"widening", []string{"a", "c"}, []string{"a", "b"}, false},
		{"orthogonal", []string{"c"}, []string{"a", "b"}, false},
		{"jwt wildcard covers body", []string{"tool:foo"}, []string{"tool:*"}, true},
		{"global wildcard", []string{"tool:foo"}, []string{"*"}, true},
		{"body wildcard not covered", []string{"tool:*"}, []string{"tool:foo"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isScopeSubset(tc.body, tc.jwt); got != tc.want {
				t.Errorf("isScopeSubset(%v, %v) = %v, want %v", tc.body, tc.jwt, got, tc.want)
			}
		})
	}
}

func TestSetupTaskBodyNarrowsJWT_NewBehavior(t *testing.T) {
	jwtScopes := []string{"tool:foo", "tool:bar", "agent:Stripe:invoke"}
	bodyScopes := []string{"tool:foo"}

	agent, _, _ := newTestAgent()
	svc := agent.svc.(*mockServices)
	svc.authn = &stubAuthenticator{
		identity:  &auth.Identity{UserID: "u-1", Scopes: jwtScopes},
		jwtTaskID: "task-narrow",
	}

	state := runSetupTaskCapturingState(t, agent, "task-narrow", newUserMessage("hi"), a2a.UserProperties{
		ClientID:      "gw",
		UserID:        "u-1",
		AuthToken:     "fake-jwt",
		A2AUserConfig: makeUserConfigWithCapabilities(bodyScopes),
	})

	if !reflect.DeepEqual(state.Identity.Scopes, bodyScopes) {
		t.Errorf("identity.Scopes = %v, want narrowed body %v", state.Identity.Scopes, bodyScopes)
	}
}
