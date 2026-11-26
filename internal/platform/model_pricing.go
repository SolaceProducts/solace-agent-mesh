// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

func ParsePricing(p *ModelPricing) (*pricing.ModelPrice, error) {
	var fields pricing.Fields
	if p != nil {
		fields = pricing.Fields{
			Input: p.Input, Output: p.Output,
			CacheRead: p.CacheRead, CacheWrite: p.CacheWrite,
		}
	}

	price, fieldErrs := pricing.Parse(fields)
	if len(fieldErrs) > 0 {
		return nil, &apierr.ValidationError{
			Message: "one or more prices are invalid",
			Details: pricing.Details(fieldErrs, "pricing."),
		}
	}
	return price, nil
}

func ApplyPricing(mc *store.ModelConfiguration, price *pricing.ModelPrice) {
	stored := pricing.Store(price)
	mc.PriceInput, mc.PriceOutput = stored.Input, stored.Output
	mc.PriceCacheRead, mc.PriceCacheWrite = stored.CacheRead, stored.CacheWrite
}

func PricingResponse(mc *store.ModelConfiguration) (*ModelPricingResponse, error) {
	price, err := pricing.Stored{
		Input: mc.PriceInput, Output: mc.PriceOutput,
		CacheRead: mc.PriceCacheRead, CacheWrite: mc.PriceCacheWrite,
	}.Price()
	if price == nil {
		return nil, err
	}

	input, output := price.Input.String(), price.Output.String()
	out := &ModelPricingResponse{Input: &input, Output: &output}
	if price.BreaksOutCache() {
		cacheRead, cacheWrite := price.CacheRead.String(), price.CacheWrite.String()
		out.CacheRead, out.CacheWrite = &cacheRead, &cacheWrite
	}
	return out, nil
}

func (p ModelPricing) MarshalJSON() ([]byte, error) {
	type wire ModelPricing
	return json.Marshal(wire{
		Input:      canonicalRate(p.Input),
		Output:     canonicalRate(p.Output),
		CacheRead:  canonicalRate(p.CacheRead),
		CacheWrite: canonicalRate(p.CacheWrite),
	})
}

func (p *ModelPricing) UnmarshalJSON(b []byte) error {
	type wire ModelPricing
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return err
	}
	*p = ModelPricing(w)
	return nil
}

func canonicalRate(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	amount, err := pricing.ParseAmount(*s)
	if err != nil {
		return s
	}
	canonical := amount.String()
	return &canonical
}
