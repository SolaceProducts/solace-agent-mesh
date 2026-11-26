// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPricingRejectsAKeyThatIsNotABucket(t *testing.T) {
	for name, body := range map[string]string{
		"snake_case cache read":        `{"input":"3","output":"15","cache_read":"0.3","cache_write":"3.75"}`,
		"snake_case cache write":       `{"input":"3","output":"15","cacheRead":"0.3","cache_write":"3.75"}`,
		"a bucket that does not exist": `{"input":"3","output":"15","reasoning":"9"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var p ModelPricing
			err := json.Unmarshal([]byte(body), &p)
			if err == nil {
				t.Fatalf("a key that is not a bucket was accepted; the price decoded as %+v", p)
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("error was %v, want it to name the unknown field", err)
			}
		})
	}
}

func TestPricingAcceptsTheFourBuckets(t *testing.T) {
	var p ModelPricing
	if err := json.Unmarshal([]byte(
		`{"input":"3","output":"15","cacheRead":"0.3","cacheWrite":"3.75"}`), &p); err != nil {
		t.Fatalf("the four buckets were refused: %v", err)
	}
	if p.Input == nil || *p.Input != "3" || p.CacheWrite == nil || *p.CacheWrite != "3.75" {
		t.Errorf("decoded as %+v", p)
	}
}

func TestPricingMarshalsRatesCanonically(t *testing.T) {
	typed := ModelPricing{Input: strPtr("3.00"), Output: strPtr("15.0000")}
	stored := ModelPricing{Input: strPtr("3"), Output: strPtr("15")}

	a, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal typed: %v", err)
	}
	b, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("3.00/15.0000 marshalled as %s but 3/15 as %s", a, b)
	}
}

func TestPricingLeavesAnUnparseableRateAlone(t *testing.T) {
	out, err := json.Marshal(ModelPricing{Input: strPtr("free"), Output: strPtr("15")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"free"`) {
		t.Errorf("marshalled as %s, want the unparseable rate left as written", out)
	}
}

func strPtr(s string) *string { return &s }

func TestAModelResponseWithAnUnknownBucketStillDecodes(t *testing.T) {
	body := `[{"id":"a","alias":"m","pricing":{"input":"3","output":"15","cacheWrite1h":"6"}}]`
	var models []ModelConfigResponse
	if err := json.Unmarshal([]byte(body), &models); err != nil {
		t.Fatalf("a response with an unknown bucket failed to decode: %v", err)
	}
	if p := models[0].Pricing; p == nil || p.Input == nil || *p.Input != "3" {
		t.Errorf("known rates were lost decoding around the unknown one: %+v", p)
	}
}
