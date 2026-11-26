// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func plantSentinel(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateLiquidBlock_IncludeBlocked(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "TEMPLATE-LIQUID-INCLUDE-SENTINEL"
	plantSentinel(t, dir, "secret.txt", sentinel)

	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`{"x": 1}`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)
	scoped := svc

	text := `«««template_liquid: data="data.json"
prefix {% include "secret.txt" %} suffix»»»`

	out, err := ResolveTemplateBlocks(ctx, text, scoped, "test-agent", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatalf("ResolveTemplateBlocks returned error: %v", err)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("template_liquid block leaked file content via {%% include %%}: %q", out)
	}
}

func TestApplyToTemplate_IncludeBlocked(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "APPLY-TO-TEMPLATE-INCLUDE-SENTINEL"
	plantSentinel(t, dir, "secret.txt", sentinel)

	store := newMemArtifactStore()
	ctx := context.Background()

	tmplLoc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "tmpl.liquid"}
	if _, err := store.Save(ctx, tmplLoc, []byte(`prefix {% include "secret.txt" %} suffix`), "text/plain"); err != nil {
		t.Fatal(err)
	}

	rctx := &ResolveContext{
		Artifacts: newTestArtifactSvc(store),
		AgentName: "test-agent",
		UserID:    "user1",
		SessionID: "sess1",
		Logger:    slog.Default(),
	}

	var totalSize int64
	out, err := applyToTemplate(ctx, map[string]any{"x": 1}, "tmpl.liquid", "text/plain", rctx, 0, map[string]bool{}, &totalSize, &[]Signal{})
	if err == nil && strings.Contains(out, sentinel) {
		t.Fatalf("apply_to_template leaked file content via {%% include %%}: %q", out)
	}
	if strings.Contains(out, sentinel) {
		t.Fatalf("apply_to_template output contained sentinel even with error: out=%q err=%v", out, err)
	}
}
