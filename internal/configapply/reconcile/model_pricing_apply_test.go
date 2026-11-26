// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type pricingPlatform struct {
	patches  [][]byte
	returned *apiv2.Pricing
}

func newPricingPlatform(t *testing.T, p *pricingPlatform) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v2/platform/models/{id}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.patches = append(p.patches, body)
		writeV2Single(w, http.StatusOK, wire.ModelResponse{ID: r.PathValue("id"), Alias: "priced", Pricing: p.returned})
	})
	return newV2PlatformClient(t, mux)
}

func pricedActual() wire.ModelResponse {
	return wire.ModelResponse{
		ID: "id-1", Alias: "priced", Provider: sp("anthropic"), ModelName: sp("claude-opus-4-7"),
		APIBase: sp("https://api.anthropic.com"), AuthConfig: map[string]any{"apiKey": "sk-ant"},
		ModelParams: map[string]any{"temperature": 0.2}, Description: sp("fixture"),
		Pricing: &apiv2.Pricing{Input: sp("3"), Output: sp("15")},
	}
}

func applyPricingSpec(t *testing.T, p *pricingPlatform, pricing any, set bool) *kindOutcome {
	t.Helper()
	return applySpec(t, p, fixtureSpec(func(m map[string]any) {
		if set {
			m["pricing"] = pricing
		}
	}))
}

func planSpec(t *testing.T, spec map[string]any) *kindPlan {
	t.Helper()
	plan, err := planModels(t.Context(), nil, []resolve.ResolvedResource{resolved("priced", spec)}, []wire.ModelResponse{pricedActual()})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func applySpec(t *testing.T, p *pricingPlatform, spec map[string]any) *kindOutcome {
	t.Helper()
	return applyModelUpserts(t.Context(), newPricingPlatform(t, p), planSpec(t, spec))
}

func patchedPricing(t *testing.T, body []byte) (json.RawMessage, bool) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode PATCH body %s: %v", body, err)
	}
	raw, ok := fields["pricing"]
	return raw, ok
}

func TestApplyAnEmptyPricingSendsTheClear(t *testing.T) {
	p := &pricingPlatform{}
	applyPricingSpec(t, p, map[string]any{}, true)
	if len(p.patches) != 1 {
		t.Fatalf("PATCHes = %d, want 1", len(p.patches))
	}
	raw, ok := patchedPricing(t, p.patches[0])
	if !ok || string(raw) != "null" {
		t.Errorf("pricing on the wire = %s (present %v), want null, which clears the price", raw, ok)
	}
}

func TestPlanLeavesAnUnmanagedPriceOutOfTheComparison(t *testing.T) {
	plan := planSpec(t, fixtureSpec(nil))
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 1 {
		t.Errorf("updates = %d, unchanged = %d; want the model unchanged", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestApplyWithoutPricingLeavesTheStoredPriceAlone(t *testing.T) {
	p := &pricingPlatform{returned: &apiv2.Pricing{Input: sp("3"), Output: sp("15")}}
	out := applySpec(t, p, fixtureSpec(func(m map[string]any) { m["modelName"] = "claude-sonnet-5" }))
	if len(out.Updated) != 1 || len(p.patches) != 1 {
		t.Fatalf("updated = %d, PATCHes = %d; the modelName change should plan one update", len(out.Updated), len(p.patches))
	}
	for _, body := range p.patches {
		if raw, ok := patchedPricing(t, body); ok {
			t.Errorf("an unmanaged price was sent as %s", raw)
		}
	}
	for _, r := range out.Updated {
		var buf bytes.Buffer
		renderModelUpdateVerbose(&buf, r.Op, FormatOptions{})
		if diff := buf.String(); strings.Contains(diff, "pricing") || !strings.Contains(diff, "modelName") {
			t.Errorf("the plan diff should show modelName and not pricing:\n%s", diff)
		}
	}
}

func TestApplySendsRatesInCanonicalForm(t *testing.T) {
	p := &pricingPlatform{returned: &apiv2.Pricing{Input: sp("2.5"), Output: sp("10")}}
	applyPricingSpec(t, p, map[string]any{"input": "2.50", "output": "10.00"}, true)
	if len(p.patches) != 1 {
		t.Fatalf("PATCHes = %d, want 1", len(p.patches))
	}
	raw, _ := patchedPricing(t, p.patches[0])
	if string(raw) != `{"input":"2.5","output":"10"}` {
		t.Errorf("pricing on the wire = %s, want canonical rates", raw)
	}
}

func TestApplyWarnsWhenSentPricingDoesNotComeBack(t *testing.T) {
	for name, tc := range map[string]struct {
		returned *apiv2.Pricing
		pricing  map[string]any
		warn     bool
	}{
		"rates sent, none returned": {nil, map[string]any{"input": "4", "output": "16"}, true},
		"rates sent and returned":   {&apiv2.Pricing{Input: sp("4"), Output: sp("16")}, map[string]any{"input": "4", "output": "16"}, false},
		"a clear sent":              {nil, map[string]any{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			out := applyPricingSpec(t, &pricingPlatform{returned: tc.returned}, tc.pricing, true)
			if len(out.Updated) != 1 {
				t.Fatalf("updated = %d, want 1 (failures %+v)", len(out.Updated), out.Failures)
			}
			if got := len(out.Updated[0].Warnings) > 0; got != tc.warn {
				t.Errorf("warned = %v, want %v (%v)", got, tc.warn, out.Updated[0].Warnings)
			}
		})
	}
}
