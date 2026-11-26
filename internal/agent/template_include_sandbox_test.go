// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
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

func TestRenderBlock_IncludeBlocked(t *testing.T) {
	dir := chdirTemp(t)
	const sentinel = "RENDER-BLOCK-INCLUDE-SENTINEL"
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`{"x": 1}`), "application/json"); err != nil {
		t.Fatal(err)
	}

	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "data.json",
		Content:    `prefix {% include "secret.txt" %} suffix`,
		Limit:      -1,
	}

	out, _ := tr.RenderBlock(ctx, block)
	if strings.Contains(out, sentinel) {
		t.Fatalf("RenderBlock leaked file content via {%% include %%}: %q", out)
	}
}

func TestValidateTemplateSyntax_AcceptsIncludeSyntax(t *testing.T) {
	chdirTemp(t)

	if err := validateTemplateSyntax(`{% include "anything" %}`); err != nil {
		t.Fatalf("validateTemplateSyntax should accept include syntax (parse-only), got error: %v", err)
	}
}
