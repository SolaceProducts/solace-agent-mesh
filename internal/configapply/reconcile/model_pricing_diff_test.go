// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestPricingDiffIgnoresHowARateWasTyped(t *testing.T) {
	desired := desiredModelView(wire.CreateModelRequest{
		Alias:   "gpt4",
		Pricing: &apiv2.Pricing{Input: sp("3.00"), Output: sp("15.0000")},
	})
	actual := actualModelView(wire.ModelResponse{
		Alias:   "gpt4",
		Pricing: &apiv2.Pricing{Input: sp("3"), Output: sp("15")},
	})

	if !reflect.DeepEqual(desired["pricing"], actual["pricing"]) {
		t.Errorf("pricing views differ:\n desired %v\n actual  %v", desired["pricing"], actual["pricing"])
	}
}

func TestPricingDiffSeesARealRateChange(t *testing.T) {
	desired := desiredModelView(wire.CreateModelRequest{
		Pricing: &apiv2.Pricing{Input: sp("3"), Output: sp("15")},
	})
	actual := actualModelView(wire.ModelResponse{
		Pricing: &apiv2.Pricing{Input: sp("2.50"), Output: sp("15")},
	})

	if reflect.DeepEqual(desired["pricing"], actual["pricing"]) {
		t.Errorf("a rate change from 2.50 to 3 did not register: %v", desired["pricing"])
	}
}

func TestPricingDiffPassesAnUnparseableRateThrough(t *testing.T) {
	view := modelPricingView(&apiv2.Pricing{Input: sp("free"), Output: sp("15")})

	if got := view["input"]; got != "free" {
		t.Errorf("input rendered as %v, want it left as the author wrote it", got)
	}
}

func TestPricingDiffOmitsAnUnpricedModel(t *testing.T) {
	desired := desiredModelView(wire.CreateModelRequest{Alias: "gpt4"})
	actual := actualModelView(wire.ModelResponse{Alias: "gpt4"})

	if _, ok := desired["pricing"]; ok {
		t.Error("an unpriced desired model carried a pricing key")
	}
	if _, ok := actual["pricing"]; ok {
		t.Error("an unpriced actual model carried a pricing key")
	}
}

func TestPricingDiffOmitsRatesThatWereNotGiven(t *testing.T) {
	view := modelPricingView(&apiv2.Pricing{Input: sp("3"), Output: sp("15")})

	for _, absent := range []string{"cacheRead", "cacheWrite"} {
		if _, ok := view[absent]; ok {
			t.Errorf("%s appeared in the view as %v, want it absent", absent, view[absent])
		}
	}
}

func TestHashTreatsARetypedRateAsUnchanged(t *testing.T) {
	desired, err := generic.Hash(authoredPricedModel(t, map[string]any{"input": "2.50", "output": "10.00"}), nil)
	if err != nil {
		t.Fatalf("hash desired: %v", err)
	}

	actual, err := generic.Hash(wire.ModelResponse{
		Alias:   "gpt4",
		Pricing: &apiv2.Pricing{Input: sp("2.5"), Output: sp("10")},
	}, nil)
	if err != nil {
		t.Fatalf("hash actual: %v", err)
	}

	if desired != actual {
		t.Error("2.50/10.00 and 2.5/10 hash differently, so every plan reports a pricing change that applying cannot resolve")
	}
}

func TestHashStillSeesARealRateChange(t *testing.T) {
	desired, err := generic.Hash(wire.CreateModelRequest{
		Alias:   "gpt4",
		Pricing: &apiv2.Pricing{Input: sp("3"), Output: sp("15")},
	}, nil)
	if err != nil {
		t.Fatalf("hash desired: %v", err)
	}

	actual, err := generic.Hash(wire.ModelResponse{
		Alias:   "gpt4",
		Pricing: &apiv2.Pricing{Input: sp("4"), Output: sp("15")},
	}, nil)
	if err != nil {
		t.Fatalf("hash actual: %v", err)
	}

	if desired == actual {
		t.Error("a changed input rate hashed the same, so a real price change would never be applied")
	}
}

func TestHashTreatsBlankRatesAsUnpriced(t *testing.T) {
	desired, err := generic.Hash(authoredPricedModel(t, map[string]any{"input": "", "output": " "}), nil)
	if err != nil {
		t.Fatalf("hash desired: %v", err)
	}
	actual, err := generic.Hash(wire.ModelResponse{Alias: "gpt4"}, nil)
	if err != nil {
		t.Fatalf("hash actual: %v", err)
	}
	if desired != actual {
		t.Error("blank rates hash differently from no price, so every plan reports an update with an empty diff")
	}
}

func TestTranslateModelRefusesAnUnknownRateKey(t *testing.T) {
	_, err := translateModelDesired(pricedModelResource(map[string]any{"input": "3", "output": "15", "cache_read": "0.3"}))
	if err == nil || !strings.Contains(err.Error(), "cache_read") {
		t.Fatalf("err = %v, want a refusal naming cache_read", err)
	}
}

func pricedModelResource(pricing map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Name:       "gpt4",
		SourceFile: "models/gpt4.yaml",
		Spec:       map[string]any{"provider": "openai", "modelName": "gpt-4o", "pricing": pricing},
	}
}

func authoredPricedModel(t *testing.T, pricing map[string]any) wire.CreateModelRequest {
	t.Helper()
	md, err := translateModelDesired(pricedModelResource(pricing))
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	md.req.Provider, md.req.ModelName = "", ""
	return md.req
}
