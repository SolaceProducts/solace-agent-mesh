// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func TestResolveTarget(t *testing.T) {
	if name, typ, err := resolveTarget(&RouteConfig{TargetWorkflowName: "WF"}); err != nil || name != "WF" || typ != "workflow" {
		t.Errorf("workflow target = (%q,%q,%v)", name, typ, err)
	}
	if name, typ, err := resolveTarget(&RouteConfig{TargetAgentName: "Agent"}); err != nil || name != "Agent" || typ != "agent" {
		t.Errorf("agent target = (%q,%q,%v)", name, typ, err)
	}
	if _, _, err := resolveTarget(&RouteConfig{}); err == nil {
		t.Error("no target should error")
	}
}

func TestEvalInputExpression(t *testing.T) {
	body := `{"message":"hi","num":7,"obj":{"a":1}}`
	req := httptest.NewRequest("POST", "/h", strings.NewReader(body))
	ctx := buildRequestContext(req, &RouteConfig{Name: "r"}, []byte(body), nil, slog.Default())

	if got, err := evalInputExpression(ctx, "input.payload:message"); err != nil || got != "hi" {
		t.Errorf("string expr = (%q,%v)", got, err)
	}
	if got, err := evalInputExpression(ctx, "input.payload:obj"); err != nil || got != `{"a":1}` {
		t.Errorf("object expr = (%q,%v)", got, err)
	}
	if got, err := evalInputExpression(ctx, "input.payload:absent"); err != nil || got != "" {
		t.Errorf("absent expr = (%q,%v)", got, err)
	}
}

func newScopedMemoryArtifacts() *artifact.ScopedService {
	return &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "local-dev"}
}

func artifactRouteWithBody(t *testing.T, body string) (*sacexpr.Context, *RouteConfig) {
	t.Helper()
	route := &RouteConfig{
		Name:            "upload",
		InputExpression: "input.payload:message",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				MimeType:        "list_item:type",
				ContentEncoding: "static:base64",
			},
		},
	}
	req := httptest.NewRequest("POST", "/upload", strings.NewReader(body))
	return buildRequestContext(req, route, []byte(body), nil, slog.Default()), route
}

func TestProcessArtifacts_Base64Decode(t *testing.T) {
	plain := "Hello, this is a base64-encoded webhook artifact."
	enc := base64.StdEncoding.EncodeToString([]byte(plain))
	body := `{"message":"process","files":[{"name":"test.txt","data":"` + enc + `","type":"text/plain"}]}`

	exprCtx, route := artifactRouteWithBody(t, body)
	artSvc := newScopedMemoryArtifacts()

	parts, uris, err := processArtifacts(t.Context(), exprCtx, route, "channel-user", artSvc)
	if err != nil {
		t.Fatalf("processArtifacts: %v", err)
	}
	if len(parts) != 1 || len(uris) != 1 {
		t.Fatalf("parts=%d uris=%d, want 1 each", len(parts), len(uris))
	}

	loc := artSvc.Resolve("", "channel-user", sessionIDFromURI(t, uris[0].(string)), "test.txt")
	content, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(content) != plain {
		t.Errorf("stored content = %q, want decoded %q", content, plain)
	}
}

func TestProcessArtifacts_SharedSessionAndSanitize(t *testing.T) {
	a := base64.StdEncoding.EncodeToString([]byte("one"))
	b := base64.StdEncoding.EncodeToString([]byte("two"))
	body := `{"message":"m","files":[` +
		`{"name":"../evil.txt","data":"` + a + `","type":"text/plain"},` +
		`{"name":"second.txt","data":"` + b + `","type":"text/plain"}]}`

	exprCtx, route := artifactRouteWithBody(t, body)
	artSvc := newScopedMemoryArtifacts()

	parts, uris, err := processArtifacts(t.Context(), exprCtx, route, "channel-user", artSvc)
	if err != nil {
		t.Fatalf("processArtifacts: %v", err)
	}
	if len(parts) != 2 || len(uris) != 2 {
		t.Fatalf("want 2 parts/uris, got parts=%d uris=%d", len(parts), len(uris))
	}
	s0, s1 := sessionIDFromURI(t, uris[0].(string)), sessionIDFromURI(t, uris[1].(string))
	if s0 != s1 {
		t.Errorf("session IDs differ across items: %q vs %q", s0, s1)
	}
	if strings.Contains(uris[0].(string), "..") {
		t.Errorf("URI not sanitized: %q", uris[0])
	}
	loc := artSvc.Resolve("", "channel-user", s0, filenameFromURI(t, uris[0].(string)))
	content, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("Load sanitized artifact: %v", err)
	}
	if string(content) != "one" {
		t.Errorf("stored content = %q, want %q", content, "one")
	}
}

func TestProcessArtifacts_SameFilenameLastWins(t *testing.T) {
	a := base64.StdEncoding.EncodeToString([]byte("first"))
	b := base64.StdEncoding.EncodeToString([]byte("second"))
	body := `{"message":"m","files":[` +
		`{"name":"dup.txt","data":"` + a + `","type":"text/plain"},` +
		`{"name":"dup.txt","data":"` + b + `","type":"text/plain"}]}`

	exprCtx, route := artifactRouteWithBody(t, body)
	artSvc := newScopedMemoryArtifacts()

	parts, uris, err := processArtifacts(t.Context(), exprCtx, route, "u", artSvc)
	if err != nil {
		t.Fatalf("processArtifacts: %v", err)
	}
	if len(parts) != 2 || len(uris) != 2 {
		t.Fatalf("want 2 parts/uris, got parts=%d uris=%d", len(parts), len(uris))
	}
	if uris[0].(string) != uris[1].(string) {
		t.Errorf("expected identical URIs for same-filename items, got %q vs %q", uris[0], uris[1])
	}
	sid := sessionIDFromURI(t, uris[0].(string))
	loc := artSvc.Resolve("", "u", sid, "dup.txt")
	if _, _, err := artSvc.Inner.Load(t.Context(), loc, nil); err != nil {
		t.Fatalf("Load same-filename artifact: %v", err)
	}
}

func TestProcessArtifacts_PlainContentSingleItem(t *testing.T) {
	body := `{"file":{"name":"note.txt","data":"plain hello"}}`
	route := &RouteConfig{
		Name: "single",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:file",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "static:utf-8",
			},
		},
	}
	req := httptest.NewRequest("POST", "/single", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	artSvc := newScopedMemoryArtifacts()

	parts, uris, err := processArtifacts(t.Context(), exprCtx, route, "channel-user", artSvc)
	if err != nil {
		t.Fatalf("processArtifacts: %v", err)
	}
	if len(parts) != 1 || len(uris) != 1 {
		t.Fatalf("parts=%d uris=%d, want 1 each", len(parts), len(uris))
	}
	loc := artSvc.Resolve("", "channel-user", sessionIDFromURI(t, uris[0].(string)), "note.txt")
	content, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("Load artifact: %v", err)
	}
	if string(content) != "plain hello" {
		t.Errorf("stored content = %q, want %q", content, "plain hello")
	}
}

func TestResolveDefinitionField_NonStringMarshal(t *testing.T) {
	body := `{"meta":{"count":3,"tags":["a","b"]}}`
	req := httptest.NewRequest("POST", "/r", strings.NewReader(body))
	ctx := buildRequestContext(req, &RouteConfig{Name: "r"}, []byte(body), nil, slog.Default())

	got, err := resolveDefinitionField(ctx, "input.payload:meta", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"count":3,"tags":["a","b"]}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProcessArtifacts_BadBase64(t *testing.T) {
	body := `{"message":"m","files":[{"name":"x.txt","data":"!!not-base64!!","type":"text/plain"}]}`
	exprCtx, route := artifactRouteWithBody(t, body)
	if _, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts()); err == nil {
		t.Fatal("undecodable base64 must error")
	}
}

func TestProcessArtifacts_BinaryEncodingRejected(t *testing.T) {
	body := `{"files":[{"name":"f.bin","data":"aGVsbG8="}]}`
	route := &RouteConfig{
		Name: "v",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "static:binary",
			},
		},
	}
	req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected 'not supported' error for binary encoding, got: %v", err)
	}
}

func TestProcessArtifacts_UnknownEncodingRejected(t *testing.T) {
	body := `{"files":[{"name":"f.bin","data":"hello"}]}`
	route := &RouteConfig{
		Name: "v",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "static:gzip",
			},
		},
	}
	req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
	if err == nil || !strings.Contains(err.Error(), "unrecognised") {
		t.Fatalf("expected unrecognised encoding error, got: %v", err)
	}
}

func TestProcessArtifacts_Base64NonStringContent(t *testing.T) {
	body := `{"files":[{"name":"f.bin","data":{"nested":"object"}}]}`
	route := &RouteConfig{
		Name: "v",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "static:base64",
			},
		},
	}
	req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
	if err == nil || !strings.Contains(err.Error(), "expected a string value") {
		t.Fatalf("expected 'expected a string value' error for non-string base64 content, got: %v", err)
	}
}

func TestProcessArtifacts_NonStringMimeType(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("payload"))
	body := `{"files":[{"name":"f.bin","data":"` + data + `","type":{"media":"image"}}]}`
	route := &RouteConfig{
		Name: "v",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				MimeType:        "list_item:type",
				ContentEncoding: "static:base64",
			},
		},
	}
	req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
	if err == nil || !strings.Contains(err.Error(), "expected a string value") {
		t.Fatalf("expected 'expected a string value' error for non-string mime_type, got: %v", err)
	}
}

func TestProcessArtifacts_Base64Variants(t *testing.T) {
	plain := []byte("hello base64 variant artifact")
	cases := []struct {
		encoding string
		encode   func([]byte) string
	}{
		{"base64url", base64.URLEncoding.EncodeToString},
		{"base64raw", base64.RawStdEncoding.EncodeToString},
		{"base64rawurl", base64.RawURLEncoding.EncodeToString},
	}
	for _, tc := range cases {
		t.Run(tc.encoding, func(t *testing.T) {
			enc := tc.encode(plain)
			body := `{"files":[{"name":"f.bin","data":"` + enc + `"}]}`
			route := &RouteConfig{
				Name: "v",
				ArtifactProcessing: &ArtifactProcessingConfig{
					ExtractExpression: "input.payload:files",
					Definition: ArtifactDefinition{
						Filename:        "list_item:name",
						Content:         "list_item:data",
						ContentEncoding: "static:" + tc.encoding,
					},
				},
			}
			req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
			exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
			artSvc := newScopedMemoryArtifacts()

			parts, uris, err := processArtifacts(t.Context(), exprCtx, route, "u", artSvc)
			if err != nil {
				t.Fatalf("%s: processArtifacts: %v", tc.encoding, err)
			}
			if len(parts) != 1 || len(uris) != 1 {
				t.Fatalf("%s: want 1 part/uri, got %d/%d", tc.encoding, len(parts), len(uris))
			}
			loc := artSvc.Resolve("", "u", sessionIDFromURI(t, uris[0].(string)), "f.bin")
			content, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
			if err != nil {
				t.Fatalf("%s: Load: %v", tc.encoding, err)
			}
			if string(content) != string(plain) {
				t.Errorf("%s: content = %q, want %q", tc.encoding, content, plain)
			}
		})
	}
}

func sessionIDFromURI(t *testing.T, uri string) string {
	t.Helper()
	rest := strings.TrimPrefix(uri, "artifact://")
	parts := strings.Split(rest, "/")
	if len(parts) < 4 {
		t.Fatalf("malformed artifact URI %q", uri)
	}
	return parts[2]
}

func filenameFromURI(t *testing.T, uri string) string {
	t.Helper()
	rest := strings.TrimPrefix(uri, "artifact://")
	parts := strings.Split(rest, "/")
	if len(parts) < 4 {
		t.Fatalf("malformed artifact URI %q", uri)
	}
	return parts[3]
}

func TestArtifactClientError_Message(t *testing.T) {
	err := &artifactClientError{Index: 2, Reason: "base64_decode"}
	if got := err.Error(); got != "artifact 2: base64_decode" {
		t.Errorf("Error() = %q, want %q", got, "artifact 2: base64_decode")
	}
}

func TestProcessArtifacts_NilArtifactService(t *testing.T) {
	body := `{"files":[{"name":"f.txt","data":"aGVsbG8=","type":"text/plain"}]}`
	route := &RouteConfig{
		Name: "upload",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "static:base64",
			},
		},
	}
	req := httptest.NewRequest("POST", "/upload", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", nil)
	if err == nil {
		t.Fatal("nil artSvc should error")
	}
	var storeErr *artifactStoreError
	if !errors.As(err, &storeErr) {
		t.Errorf("expected artifactStoreError, got %T: %v", err, err)
	}
}

func TestProcessArtifacts_ContentEncodingExprError(t *testing.T) {
	body := `{"files":[{"name":"f.bin","data":"aGVsbG8="}]}`
	route := &RouteConfig{
		Name: "v",
		ArtifactProcessing: &ArtifactProcessingConfig{
			ExtractExpression: "input.payload:files",
			Definition: ArtifactDefinition{
				Filename:        "list_item:name",
				Content:         "list_item:data",
				ContentEncoding: "badsource:encoding",
			},
		},
	}
	req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
	exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
	_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
	if err == nil {
		t.Fatal("bad content_encoding expression should error")
	}
}

func TestProcessArtifacts_Base64VariantDecodeFailures(t *testing.T) {
	for _, tc := range []struct {
		encoding string
		reason   string
	}{
		{"base64url", "base64url_decode"},
		{"base64raw", "base64raw_decode"},
		{"base64rawurl", "base64rawurl_decode"},
	} {
		t.Run(tc.encoding, func(t *testing.T) {
			body := `{"files":[{"name":"f.bin","data":"!!not-valid!!"}]}`
			route := &RouteConfig{
				Name: "v",
				ArtifactProcessing: &ArtifactProcessingConfig{
					ExtractExpression: "input.payload:files",
					Definition: ArtifactDefinition{
						Filename:        "list_item:name",
						Content:         "list_item:data",
						ContentEncoding: "static:" + tc.encoding,
					},
				},
			}
			req := httptest.NewRequest("POST", "/v", strings.NewReader(body))
			exprCtx := buildRequestContext(req, route, []byte(body), nil, slog.Default())
			_, _, err := processArtifacts(t.Context(), exprCtx, route, "u", newScopedMemoryArtifacts())
			if err == nil {
				t.Fatalf("%s: bad data should error", tc.encoding)
			}
			var clientErr *artifactClientError
			if !errors.As(err, &clientErr) || clientErr.Reason != tc.reason {
				t.Errorf("%s: expected artifactClientError reason %q, got: %v", tc.encoding, tc.reason, err)
			}
		})
	}
}

func TestResolveStringField_NilValue(t *testing.T) {
	body := `{"name":"f.bin"}`
	req := httptest.NewRequest("POST", "/r", strings.NewReader(body))
	ctx := buildRequestContext(req, &RouteConfig{Name: "r"}, []byte(body), nil, slog.Default())
	got, err := resolveStringField(ctx, "input.payload:absent", "fallback/type")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fallback/type" {
		t.Errorf("got %q, want %q", got, "fallback/type")
	}
}

func TestResolveStringField_GetDataError(t *testing.T) {
	body := `{"name":"f.bin"}`
	req := httptest.NewRequest("POST", "/r", strings.NewReader(body))
	ctx := buildRequestContext(req, &RouteConfig{Name: "r"}, []byte(body), nil, slog.Default())
	_, err := resolveStringField(ctx, "badsource:field", "")
	if err == nil {
		t.Fatal("unknown source expression must error")
	}
}
