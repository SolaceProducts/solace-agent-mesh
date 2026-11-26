// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
)

func TestRenderBlock_DeadlineExceeded_ReturnsMarker(t *testing.T) {
	prev := liquidsafe.DefaultRenderDeadline
	liquidsafe.DefaultRenderDeadline = 50 * time.Millisecond
	t.Cleanup(func() { liquidsafe.DefaultRenderDeadline = prev })

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
		Content:    `{% for n in (1..10000000) %}{{ n }},{% endfor %}`,
		Limit:      -1,
	}

	start := time.Now()
	out, err := tr.RenderBlock(ctx, block)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RenderBlock should not error on deadline (returns marker instead); got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("render exceeded deadline window: elapsed=%s", elapsed)
	}
	if !strings.Contains(out, TemplateRenderDeadlineExceededMarker) {
		t.Errorf("expected render-deadline truncation marker; got %d bytes", len(out))
	}
}
