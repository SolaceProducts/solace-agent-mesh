// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"strings"
	"testing"
)

func baseRoute() map[string]any {
	return map[string]any{
		"name":  "r1",
		"match": map[string]any{"path": "/hook", "method": "POST"},
		"auth": map[string]any{
			"token": map[string]any{"value": "dev-webhook-token"},
		},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
	}
}

func parseOneRoute(r map[string]any) (*Config, error) {
	return ParseConfig(map[string]any{"routes": []any{r}})
}

func TestParseConfig_ValidDefaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"routes": []any{baseRoute()}})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(cfg.Routes))
	}
	if cfg.MaxBodyBytes != defaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want default %d", cfg.MaxBodyBytes, defaultMaxBodyBytes)
	}
	tok := cfg.Routes[0].Auth.Token
	if tok == nil || tok.Location != "bearer" {
		t.Errorf("token location = %+v, want default bearer", tok)
	}
}

func TestParseConfig_HMACEncodingAllowList(t *testing.T) {
	hmacBase := func(enc string) map[string]any {
		r := baseRoute()
		r["auth"] = map[string]any{"hmac": map[string]any{
			"header":   "X-Sig",
			"secret":   "sixteen-char-secret!",
			"algo":     "sha256",
			"encoding": enc,
		}}
		return r
	}
	for _, enc := range []string{"hex", "base64", "base64url", "base64raw", "base64rawurl"} {
		if _, err := parseOneRoute(hmacBase(enc)); err != nil {
			t.Errorf("encoding %q should be accepted by ParseConfig, got: %v", enc, err)
		}
	}
	if _, err := parseOneRoute(hmacBase("binary")); err == nil {
		t.Error("encoding 'binary' should be rejected by ParseConfig")
	}
}

func TestParseConfig_HMACBase64UrlIntegration(t *testing.T) {
	secret := "dev-hmac-secret-value"
	route := map[string]any{
		"name":  "b64url_route",
		"match": map[string]any{"path": "/b64url", "method": "POST"},
		"auth": map[string]any{"hmac": map[string]any{
			"header": "X-Sig", "algo": "sha256", "secret": secret, "encoding": "base64url",
		}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
	}
	if _, err := ParseConfig(map[string]any{"routes": []any{route}}); err != nil {
		t.Fatalf("ParseConfig rejected base64url encoding: %v", err)
	}
}

func TestParseConfig_HMACDefaults(t *testing.T) {
	r := baseRoute()
	r["auth"] = map[string]any{
		"hmac": map[string]any{"header": "X-Hub-Signature-256", "secret": "sixteen-char-secret!"},
	}
	cfg, err := parseOneRoute(r)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	h := cfg.Routes[0].Auth.HMAC
	if h.Algo != "sha256" || h.Encoding != "hex" {
		t.Errorf("hmac defaults = algo %q enc %q, want sha256/hex", h.Algo, h.Encoding)
	}
}

func TestParseConfig_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		wantErr string
	}{
		{"missing input_expression", func(m map[string]any) { delete(m, "input_expression") }, "input_expression"},
		{"no target", func(m map[string]any) { delete(m, "target_agent_name") }, "target_agent_name or target_workflow_name"},
		{"both targets", func(m map[string]any) { m["target_workflow_name"] = "WF" }, "mutually exclusive"},
		{"missing name", func(m map[string]any) { delete(m, "name") }, "route 'name' is required"},
		{"missing path", func(m map[string]any) { m["match"] = map[string]any{"method": "POST"} }, "match.path is required"},
		{"path no leading slash", func(m map[string]any) { m["match"] = map[string]any{"path": "hook", "method": "POST"} }, "must start with '/'"},
		{"bad method", func(m map[string]any) { m["match"] = map[string]any{"path": "/hook", "method": "DELETE"} }, "POST/PUT/PATCH"},
		{"no auth block", func(m map[string]any) { delete(m, "auth") }, "auth block is required"},
		{"short token", func(m map[string]any) { m["auth"] = map[string]any{"token": map[string]any{"value": "short"}} }, "at least 8 characters"},
		{"short hmac secret", func(m map[string]any) {
			m["auth"] = map[string]any{"hmac": map[string]any{"header": "X-Sig", "secret": "tooshort"}}
		}, "at least 16 characters"},
		{"bad hmac algo", func(m map[string]any) {
			m["auth"] = map[string]any{"hmac": map[string]any{"header": "X-Sig", "secret": "sixteen-char-secret!", "algo": "md5"}}
		}, "sha256 or sha512"},
		{"empty header alias", func(m map[string]any) { m["headers"] = map[string]any{"": "X-Event"} }, "headers"},
		{"non-string header value", func(m map[string]any) { m["headers"] = map[string]any{"event": 123} }, "must be a string"},
		{"blocked header Authorization", func(m map[string]any) { m["headers"] = map[string]any{"auth": "Authorization"} }, "may not be exposed"},
		{"blocked header Cookie", func(m map[string]any) { m["headers"] = map[string]any{"session": "Cookie"} }, "may not be exposed"},
		{"bad token location", func(m map[string]any) {
			m["auth"] = map[string]any{"token": map[string]any{"location": "cookie", "value": "dev-webhook-token"}}
		}, "auth.token.location"},
		{"header token no name", func(m map[string]any) {
			m["auth"] = map[string]any{"token": map[string]any{"location": "header", "value": "dev-webhook-token"}}
		}, "auth.token.name"},
		{"query token no name", func(m map[string]any) {
			m["auth"] = map[string]any{"token": map[string]any{"location": "query", "value": "dev-webhook-token"}}
		}, "auth.token.name"},
		{"hmac no header", func(m map[string]any) {
			m["auth"] = map[string]any{"hmac": map[string]any{"secret": "sixteen-char-secret!"}}
		}, "auth.hmac.header is required"},
		{"bad hmac encoding", func(m map[string]any) {
			m["auth"] = map[string]any{"hmac": map[string]any{"header": "X-Sig", "secret": "sixteen-char-secret!", "encoding": "binary"}}
		}, "auth.hmac.encoding"},
		{"hmac signature header aliased", func(m map[string]any) {
			m["auth"] = map[string]any{"hmac": map[string]any{"header": "X-Hub-Sig", "secret": "sixteen-char-secret!", "algo": "sha256"}}
			m["headers"] = map[string]any{"sig": "X-Hub-Sig"}
		}, "may not be exposed"},
		{"token auth header aliased", func(m map[string]any) {
			m["auth"] = map[string]any{"token": map[string]any{"location": "header", "name": "X-Auth-Token", "value": "dev-webhook-token"}}
			m["headers"] = map[string]any{"tok": "X-Auth-Token"}
		}, "may not be exposed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := baseRoute()
			tt.mutate(r)
			_, err := parseOneRoute(r)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseConfig_NoRoutes(t *testing.T) {
	if _, err := ParseConfig(map[string]any{}); err == nil {
		t.Fatal("want error for zero routes")
	}
}

func TestParseConfig_DuplicateName(t *testing.T) {
	r1 := baseRoute()
	r2 := baseRoute()
	r2["match"] = map[string]any{"path": "/other", "method": "POST"}
	_, err := ParseConfig(map[string]any{"routes": []any{r1, r2}})
	if err == nil || !strings.Contains(err.Error(), "duplicate route name") {
		t.Fatalf("want duplicate route name error, got %v", err)
	}
}

func TestParseConfig_ConflictingWildcardRoutes(t *testing.T) {
	r1 := baseRoute()
	r1["name"] = "r1"
	r1["match"] = map[string]any{"path": "/a/{x}/b", "method": "POST"}
	r2 := baseRoute()
	r2["name"] = "r2"
	r2["match"] = map[string]any{"path": "/a/c/{y}", "method": "POST"}
	_, err := ParseConfig(map[string]any{"routes": []any{r1, r2}})
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("want conflict error, got %v", err)
	}
}

func TestParseConfig_EmptyAuthBlock(t *testing.T) {
	r := baseRoute()
	r["auth"] = map[string]any{}
	_, err := parseOneRoute(r)
	if err == nil || !strings.Contains(err.Error(), "auth block is required") {
		t.Fatalf("want auth error for empty auth block, got %v", err)
	}
}

func TestParseConfig_NonConflictingWildcardRoutes(t *testing.T) {
	r1 := baseRoute()
	r1["name"] = "r1"
	r1["match"] = map[string]any{"path": "/a/{x}", "method": "POST"}
	r2 := baseRoute()
	r2["name"] = "r2"
	r2["match"] = map[string]any{"path": "/b/{y}", "method": "POST"}
	if _, err := ParseConfig(map[string]any{"routes": []any{r1, r2}}); err != nil {
		t.Fatalf("non-conflicting wildcard routes must be accepted, got: %v", err)
	}
}

func TestParseConfig_MaxBodyBytesNegative(t *testing.T) {
	r := baseRoute()
	r["max_body_bytes"] = -1
	_, err := parseOneRoute(r)
	if err == nil || !strings.Contains(err.Error(), "max_body_bytes must be non-negative") {
		t.Fatalf("want non-negative error, got %v", err)
	}
}

func TestParseConfig_MethodNormalized(t *testing.T) {
	r := baseRoute()
	r["match"] = map[string]any{"path": "/hook", "method": "post"}
	cfg, err := parseOneRoute(r)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Routes[0].Match.Method != "POST" {
		t.Errorf("Method = %q, want POST", cfg.Routes[0].Match.Method)
	}
}

func baseArtifactRoute() map[string]any {
	r := baseRoute()
	r["artifact_processing"] = map[string]any{
		"extract_artifacts_expression": "input.payload:files",
		"artifact_definition": map[string]any{
			"content": "list_item:data",
		},
	}
	return r
}

func TestParseConfig_ArtifactProcessing_Valid(t *testing.T) {
	if _, err := parseOneRoute(baseArtifactRoute()); err != nil {
		t.Fatalf("valid artifact_processing should be accepted: %v", err)
	}
}

func TestParseConfig_ArtifactProcessing_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		wantErr string
	}{
		{"missing extract expression", func(m map[string]any) {
			m["artifact_processing"] = map[string]any{
				"artifact_definition": map[string]any{"content": "list_item:data"},
			}
		}, "extract_artifacts_expression is required"},
		{"empty extract expression", func(m map[string]any) {
			m["artifact_processing"] = map[string]any{
				"extract_artifacts_expression": "",
				"artifact_definition":          map[string]any{"content": "list_item:data"},
			}
		}, "extract_artifacts_expression is required"},
		{"missing content", func(m map[string]any) {
			m["artifact_processing"] = map[string]any{
				"extract_artifacts_expression": "input.payload:files",
				"artifact_definition":          map[string]any{"filename": "list_item:name"},
			}
		}, "artifact_definition.content is required"},
		{"empty artifact_processing block", func(m map[string]any) {
			m["artifact_processing"] = map[string]any{}
		}, "extract_artifacts_expression is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := baseRoute()
			tt.mutate(r)
			_, err := parseOneRoute(r)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseConfig_GlobalMaxBodyBytesNegative(t *testing.T) {
	r := baseRoute()
	_, err := ParseConfig(map[string]any{
		"max_body_bytes": -1,
		"routes":         []any{r},
	})
	if err == nil || !strings.Contains(err.Error(), "max_body_bytes must be non-negative") {
		t.Fatalf("want non-negative error for global max_body_bytes, got %v", err)
	}
}

func TestParseConfig_ArtifactProcessing_StaticEncodingInvalid(t *testing.T) {
	for _, bad := range []string{"static:binary", "static:gzip", "static:latin-1", "binary", "gzip", "latin-1"} {
		r := baseArtifactRoute()
		r["artifact_processing"].(map[string]any)["artifact_definition"].(map[string]any)["content_encoding"] = bad
		_, err := parseOneRoute(r)
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%s: want 'not supported' error, got %v", bad, err)
		}
	}
}

func TestParseConfig_ArtifactProcessing_StaticEncodingValid(t *testing.T) {
	for _, good := range []string{"static:base64", "static:utf-8", "static:base64url", "static:base64raw", "static:base64rawurl", "static:", "base64", "utf-8", "base64url", "base64raw", "base64rawurl"} {
		r := baseArtifactRoute()
		r["artifact_processing"].(map[string]any)["artifact_definition"].(map[string]any)["content_encoding"] = good
		if _, err := parseOneRoute(r); err != nil {
			t.Errorf("%s: unexpected error: %v", good, err)
		}
	}
}

func TestParseConfig_ArtifactProcessing_DynamicEncodingAccepted(t *testing.T) {
	r := baseArtifactRoute()
	r["artifact_processing"].(map[string]any)["artifact_definition"].(map[string]any)["content_encoding"] = "list_item:encoding"
	if _, err := parseOneRoute(r); err != nil {
		t.Errorf("dynamic encoding expression must not be rejected at parse time: %v", err)
	}
}

func TestParseConfig_MaxBodyBytesExceedsLimit(t *testing.T) {
	r := baseRoute()
	r["max_body_bytes"] = float64(50<<20 + 1)
	_, err := parseOneRoute(r)
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("want exceed-limit error, got %v", err)
	}
}

func TestParseConfig_GlobalMaxBodyBytesExceedsLimit(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"max_body_bytes": float64(50<<20 + 1),
		"routes":         []any{baseRoute()},
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("want exceed-limit error for global max_body_bytes, got %v", err)
	}
}

func TestParseConfig_ArtifactProcessingWrongType(t *testing.T) {
	r := baseRoute()
	r["artifact_processing"] = []any{"not", "a", "map"}
	_, err := parseOneRoute(r)
	if err == nil || !strings.Contains(err.Error(), "must be a map") {
		t.Fatalf("want type error for list artifact_processing, got %v", err)
	}
}

func TestParseConfig_MaxBodyBytesWrongType(t *testing.T) {
	r := baseRoute()
	r["max_body_bytes"] = "5MB"
	_, err := parseOneRoute(r)
	if err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("want type error for string max_body_bytes, got %v", err)
	}
}

func TestParseConfig_GlobalMaxBodyBytesWrongType(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"max_body_bytes": "1MB",
		"routes":         []any{baseRoute()},
	})
	if err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("want type error for string global max_body_bytes, got %v", err)
	}
}

func TestParseConfig_ServeMuxErrorsNameRoute(t *testing.T) {
	makeRoute := func(name, path string) map[string]any {
		r := baseRoute()
		r["name"] = name
		r["match"] = map[string]any{"path": path, "method": "POST"}
		return r
	}
	cases := []struct {
		label      string
		routes     []any
		wantSubstr string
	}{
		{
			"conflicting wildcards names the second route",
			[]any{makeRoute("alpha", "/a/{x}/b"), makeRoute("beta", "/a/c/{y}")},
			`"beta"`,
		},
		{
			"malformed pattern names the route",
			[]any{makeRoute("bad", "/a/{oops")},
			`"bad"`,
		},
		{
			"duplicate wildcard names the route",
			[]any{makeRoute("dup", "/a/{id}/b/{id}")},
			`"dup"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := ParseConfig(map[string]any{"routes": tc.routes})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestParseConfig_DuplicateMethodPath(t *testing.T) {
	r1 := baseRoute()
	r1["match"] = map[string]any{"path": "/orders/{id}", "method": "POST"}
	r2 := baseRoute()
	r2["name"] = "r2"
	r2["match"] = map[string]any{"path": "/orders/{orderId}", "method": "POST"}
	_, err := ParseConfig(map[string]any{"routes": []any{r1, r2}})
	if err == nil || !strings.Contains(err.Error(), "duplicate method+path") {
		t.Fatalf("want duplicate method+path error, got %v", err)
	}
}

func TestValidateAuth_NonNilBothNilSubfields(t *testing.T) {
	r := &RouteConfig{Name: "r", Auth: &AuthConfig{}}
	err := r.validateAuth()
	if err == nil {
		t.Fatal("expected error for auth block with neither token nor hmac")
	}
	if !strings.Contains(err.Error(), "at least one of token or hmac") {
		t.Errorf("error = %v, want 'at least one of token or hmac'", err)
	}
}
