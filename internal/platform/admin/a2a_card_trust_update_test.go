// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func trustedURLs(n int) []string {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = jwksURL
	}
	return urls
}

func requireValidationErrorOn(t *testing.T, label string, err error, field string) {
	t.Helper()
	if ve, ok := errors.AsType[*ValidationError](err); !ok || ve.Field != field {
		t.Errorf("%s: error = %v, want a ValidationError on %s", label, err, field)
	}
}

func TestA2AAgentService_UpdateValidatesCardTrust(t *testing.T) {
	t.Parallel()
	svc, _ := newSvcForTest(t)
	ctx := t.Context()
	created, err := svc.Create(ctx, remoteAgentCreate("update-validation"), "alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	invalid := []struct {
		name  string
		req   UpdateRemoteAgentRequest
		field string
	}{
		{"unknown pin", UpdateRemoteAgentRequest{ProtocolVersion: "1.0"}, fieldProtocolVersion},
		{"plain http off loopback", UpdateRemoteAgentRequest{TrustedCardJWKSURLs: []string{"http://keys.example.com/jwks.json"}}, fieldTrustedCardJWKSURLs},
		{"eleven URLs", UpdateRemoteAgentRequest{TrustedCardJWKSURLs: trustedURLs(proxy.MaxTrustedCardJWKSURLs + 1)}, fieldTrustedCardJWKSURLs},
	}
	for _, tt := range invalid {
		_, updateErr := svc.Update(ctx, created.ID, tt.req, "alice")
		requireValidationErrorOn(t, tt.name, updateErr, tt.field)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProtocolVersion != ProtocolPinAuto || len(got.TrustedCardJWKSURLs) != 0 {
		t.Errorf("a refused update was stored: %q %v", got.ProtocolVersion, got.TrustedCardJWKSURLs)
	}
}

func TestA2AAgentService_MovingATrustedAgentToNoCardIsRefused(t *testing.T) {
	t.Parallel()
	svc, _ := newSvcForTest(t)
	ctx := t.Context()
	skills := []any{map[string]any{"name": "s"}}

	withURLs := remoteAgentCreate("trusted-urls")
	withURLs.TrustedCardJWKSURLs = []string{jwksURL}
	signed := remoteAgentCreate("trusted-and-required")
	signed.TrustedCardJWKSURLs = []string{jwksURL}
	signed.RequireSignedCard = new(true)

	for _, req := range []CreateRemoteAgentRequest{withURLs, signed} {
		created, err := svc.Create(ctx, req, "alice")
		if err != nil {
			t.Fatalf("create %s: %v", req.Name, err)
		}

		_, err = svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{AgentCardLocation: "none", Skills: skills}, "alice")
		requireValidationErrorOn(t, req.Name+": update keeping the stored trust", err, fieldTrustedCardJWKSURLs)

		replace := req
		replace.AgentCardLocation = "none"
		replace.Skills = skills
		_, err = svc.Replace(ctx, created.ID, replace, "alice")
		requireValidationErrorOn(t, req.Name+": replace", err, fieldTrustedCardJWKSURLs)

		got, err := svc.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("get %s: %v", req.Name, err)
		}
		if got.AgentCardLocation != "well_known" {
			t.Errorf("%s: a refused move to none was stored: %q", req.Name, got.AgentCardLocation)
		}

		cleared, err := svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{
			AgentCardLocation: "none", Skills: skills, TrustedCardJWKSURLs: []string{}, RequireSignedCard: new(false),
		}, "alice")
		if err != nil {
			t.Fatalf("%s: a move to none that clears the trust was refused: %v", req.Name, err)
		}
		if cleared.AgentCardLocation != "none" || len(cleared.TrustedCardJWKSURLs) != 0 || cleared.RequireSignedCard {
			t.Errorf("%s: after the clearing move = %q %v %v", req.Name, cleared.AgentCardLocation, cleared.TrustedCardJWKSURLs, cleared.RequireSignedCard)
		}
	}
}

func TestBuildResponse_LegacyDeployedSnapshotReadsAsTheDefaults(t *testing.T) {
	t.Parallel()
	svc := &A2AAgentService{}
	a := &store.A2AAgent{ID: "agent-1", AgentURL: "https://example.com", Name: "legacy", AgentCardLocation: "well_known"}
	legacy := `{"name":"legacy","agentUrl":"https://example.com","agentCardLocation":"well_known","agentAuthType":"none"}`
	hash := computeA2AAgentConfigHash(a)
	deploy := &store.AgentDeployment{
		Status: DeployStatusSuccess, Action: DeployActionDeploy, ConfigHash: &hash,
		AgentConfigSnapshot: &legacy, CreatedBy: "alice", CreatedTime: 1700000000000,
	}

	blob, err := json.Marshal(svc.buildResponse(a, deploy, DisplayStatusDeployed).Deployed)
	if err != nil {
		t.Fatalf("marshal deployed: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatalf("decode deployed: %v", err)
	}
	if wire["protocolVersion"] != ProtocolPinAuto {
		t.Errorf("deployed.protocolVersion = %#v, want %q", wire["protocolVersion"], ProtocolPinAuto)
	}
	if urls, ok := wire["trustedCardJwksUrls"].([]any); !ok || len(urls) != 0 {
		t.Errorf("deployed.trustedCardJwksUrls = %#v, want []", wire["trustedCardJwksUrls"])
	}
	if wire["requireSignedCard"] != false {
		t.Errorf("deployed.requireSignedCard = %#v, want false", wire["requireSignedCard"])
	}
}

func oneofValues(t *testing.T, typ reflect.Type, jsonName string) []string {
	t.Helper()
	for f := range typ.Fields() {
		if strings.Split(f.Tag.Get("json"), ",")[0] != jsonName {
			continue
		}
		for rule := range strings.SplitSeq(f.Tag.Get("validate"), ",") {
			if values, ok := strings.CutPrefix(rule, "oneof="); ok {
				return strings.Fields(values)
			}
		}
		t.Fatalf("%s.%s has no oneof rule", typ.Name(), f.Name)
	}
	t.Fatalf("%s has no %s field", typ.Name(), jsonName)
	return nil
}

func schemaEnums(node any, property string, found *[][]string) {
	switch n := node.(type) {
	case map[string]any:
		if props, ok := n["properties"].(map[string]any); ok {
			if prop, ok := props[property].(map[string]any); ok {
				if enum, ok := prop["enum"].([]any); ok {
					values := make([]string, 0, len(enum))
					for _, v := range enum {
						s, _ := v.(string)
						values = append(values, s)
					}
					*found = append(*found, values)
				}
			}
		}
		for _, v := range n {
			schemaEnums(v, property, found)
		}
	case []any:
		for _, v := range n {
			schemaEnums(v, property, found)
		}
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

func TestProtocolPins_EveryCopyMatches(t *testing.T) {
	t.Parallel()
	pins := A2AProtocolPins()
	for _, typ := range []reflect.Type{reflect.TypeFor[CreateRemoteAgentRequest](), reflect.TypeFor[UpdateRemoteAgentRequest]()} {
		if got := oneofValues(t, typ, fieldProtocolVersion); !sameSet(got, pins) {
			t.Errorf("%s protocolVersion oneof = %v, want %v", typ.Name(), got, pins)
		}
	}

	raw, err := schema.SchemaContent("a2a-agent")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse a2a-agent schema: %v", err)
	}
	var enums [][]string
	schemaEnums(doc, fieldProtocolVersion, &enums)
	if len(enums) == 0 {
		t.Fatal("the a2a-agent schema declares no protocolVersion enum")
	}
	for _, enum := range enums {
		if !sameSet(enum, pins) {
			t.Errorf("a2a-agent schema protocolVersion enum = %v, want %v", enum, pins)
		}
	}
}

func proxyParseOfTrustedURLs(t *testing.T, urls []string) error {
	t.Helper()
	a := &store.A2AAgent{ID: "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f", AgentURL: "https://example.com/agent", Name: "a",
		AgentCardLocation: "well_known", ProtocolVersion: ProtocolPinAuto, TrustedCardJWKSURLs: marshalTrustedCardJWKSURLs(urls)}
	_, err := parseAsTheProxyAfterAJSONRoundTrip(t, buildA2AProxyAppConfig(a, "test-ns"))
	return err
}

func TestTrustedCardJWKSURLs_PlatformNeverAcceptsWhatTheProxyRefuses(t *testing.T) {
	t.Parallel()
	long := "https://keys.example.com/" + strings.Repeat("a", maxTrustedCardJWKSURLBytes)
	tests := []struct {
		name         string
		urls         []string
		platformTake bool
	}{
		{"https", []string{jwksURL}, true},
		{"http on localhost", []string{"http://localhost:8080/jwks.json"}, true},
		{"http on 127.0.0.1", []string{"http://127.0.0.1/jwks.json"}, true},
		{"http on 127.0.0.2", []string{"http://127.0.0.2/jwks.json"}, true},
		{"http on LOCALHOST", []string{"http://LOCALHOST/jwks.json"}, true},
		{"http on [::1]", []string{"http://[::1]:8080/jwks.json"}, true},
		{"http on localhost.", []string{"http://localhost./jwks.json"}, false},
		{"http off loopback", []string{"http://keys.example.com/jwks.json"}, false},
		{"http on a host named like localhost", []string{"http://localhost.evil.example.com/jwks.json"}, false},
		{"userinfo", []string{"https://alice:secret@keys.example.com/jwks.json"}, true},
		{"over 2048 bytes", []string{long}, false},
		{"relative", []string{"/jwks.json"}, false},
		{"ten entries", trustedURLs(proxy.MaxTrustedCardJWKSURLs), true},
		{"eleven entries", trustedURLs(proxy.MaxTrustedCardJWKSURLs + 1), false},
	}
	for _, tt := range tests {
		platformErr := validateCardTrustRequest("", tt.urls)
		if (platformErr == nil) != tt.platformTake {
			t.Errorf("%s: platform error = %v, want accepted %v", tt.name, platformErr, tt.platformTake)
		}
		if platformErr != nil {
			continue
		}
		if proxyErr := proxyParseOfTrustedURLs(t, tt.urls); proxyErr != nil {
			t.Errorf("%s: the platform accepts it but the proxy refuses the deploy: %v", tt.name, proxyErr)
		}
	}
}
