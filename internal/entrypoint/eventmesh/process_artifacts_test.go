// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func newTestArtifactSvc() *artifact.ScopedService {
	return &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
}

func TestProcessArtifactItems_BinaryEncodingReturnsError(t *testing.T) {
	payload := map[string]any{"data": "anything", "name": "file.bin"}
	exprCtx := sacexpr.NewContext([]any{payload}, "", nil)
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:",
		Definition: ArtifactDefinition{
			Filename:        "static:file.bin",
			Content:         "list_item:data",
			ContentEncoding: "static:binary",
		},
	}
	_, _, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", newTestArtifactSvc(), slog.Default())
	if err == nil {
		t.Fatal("expected error for binary content_encoding")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error should mention binary, got: %v", err)
	}
}

func TestProcessArtifactItems_UnrecognisedEncodingReturnsError(t *testing.T) {
	payload := map[string]any{"data": "anything"}
	exprCtx := sacexpr.NewContext([]any{payload}, "", nil)
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:",
		Definition: ArtifactDefinition{
			Filename:        "static:f.txt",
			Content:         "list_item:data",
			ContentEncoding: "static:base256",
		},
	}
	_, _, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", newTestArtifactSvc(), slog.Default())
	if err == nil {
		t.Fatal("expected error for unrecognised content_encoding")
	}
	if !strings.Contains(err.Error(), "base256") {
		t.Errorf("error should mention the bad encoding, got: %v", err)
	}
}

func TestProcessArtifactItems_MultiItemUniqueURIs(t *testing.T) {
	items := []any{
		map[string]any{"data": "hello", "name": "doc.txt"},
		map[string]any{"data": "world", "name": "doc.txt"},
	}
	exprCtx := sacexpr.NewContext(items, "", nil)
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:",
		Definition: ArtifactDefinition{
			Filename: "list_item:name",
			Content:  "list_item:data",
		},
	}
	parts, uris, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", newTestArtifactSvc(), slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if uris[0] == uris[1] {
		t.Errorf("two items with the same filename must produce distinct URIs; both got %v", uris[0])
	}
}

func TestProcessArtifactItems_UTF8Content(t *testing.T) {
	payload := map[string]any{"msg": "hello world"}
	exprCtx := sacexpr.NewContext(payload, "", nil)
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:msg",
		Definition: ArtifactDefinition{
			Filename:        "static:msg.txt",
			Content:         "input.payload:msg",
			ContentEncoding: "static:utf-8",
		},
	}
	parts, uris, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", newTestArtifactSvc(), slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 || len(uris) != 1 {
		t.Fatalf("expected 1 part and URI, got %d parts %d uris", len(parts), len(uris))
	}
}

func TestProcessArtifactItems_Base64Content(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("binary payload"))
	payload := map[string]any{"data": encoded}
	exprCtx := sacexpr.NewContext(payload, "", nil)
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:data",
		Definition: ArtifactDefinition{
			Filename:        "static:blob.bin",
			Content:         "input.payload:data",
			ContentEncoding: "static:base64",
		},
	}
	parts, uris, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", newTestArtifactSvc(), slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 || len(uris) != 1 {
		t.Fatalf("expected 1 part and URI, got %d parts %d uris", len(parts), len(uris))
	}
}

func TestProcessArtifactItems_BareEncodingLiteral(t *testing.T) {
	want := []byte("decoded payload")
	encoded := base64.StdEncoding.EncodeToString(want)
	payload := map[string]any{"data": encoded}
	exprCtx := sacexpr.NewContext(payload, "", nil)
	artSvc := newTestArtifactSvc()
	ap := &ArtifactProcessingConfig{
		ExtractExpression: "input.payload:data",
		Definition: ArtifactDefinition{
			Filename:        "static:f.bin",
			Content:         "input.payload:data",
			ContentEncoding: "static:base64",
		},
	}
	_, uris, err := processArtifactItems(context.Background(), exprCtx, ap, "user1", artSvc, slog.Default())
	if err != nil {
		t.Fatalf("base64 encoding should work; got: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("expected 1 URI, got %d", len(uris))
	}
	uri := uris[0].(string)
	rest := strings.TrimPrefix(uri, "artifact://")
	segs := strings.SplitN(rest, "/", 4)
	if len(segs) < 4 {
		t.Fatalf("malformed URI %q", uri)
	}
	loc := artSvc.Resolve("", "user1", segs[2], segs[3])
	content, _, loadErr := artSvc.Inner.Load(context.Background(), loc, nil)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if string(content) != string(want) {
		t.Errorf("stored content = %q, want %q — bare encoding was not decoded", content, want)
	}
}
