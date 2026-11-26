// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"slices"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func TestParseTreatsNoRatesAsUnpriced(t *testing.T) {
	cases := map[string]Fields{
		"all absent": {},
		"all blank":  {Input: ptr(""), Output: ptr(""), CacheRead: ptr(""), CacheWrite: ptr("")},
		"all whitespace": {
			Input: ptr("   "), Output: ptr("\t"), CacheRead: ptr(" "), CacheWrite: ptr(""),
		},
	}

	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			price, errs := Parse(fields)
			if len(errs) > 0 {
				t.Fatalf("Parse returned errors for an unpriced model: %v", errs)
			}
			if price != nil {
				t.Errorf("Parse returned a price %+v, want nil", *price)
			}
		})
	}
}

func TestParseAcceptsInputAndOutputAlone(t *testing.T) {
	price, errs := Parse(Fields{Input: ptr("3"), Output: ptr("15")})
	if len(errs) > 0 {
		t.Fatalf("Parse returned errors: %v", errs)
	}
	if price == nil {
		t.Fatal("Parse returned no price")
	}
	if price.Input != amount(t, "3") || price.Output != amount(t, "15") {
		t.Errorf("Parse gave input %s output %s, want 3 and 15", price.Input, price.Output)
	}
	if price.CacheRead != nil || price.CacheWrite != nil {
		t.Error("Parse invented cache rates that were not given")
	}
}

func TestParseAcceptsAllFourRates(t *testing.T) {
	price, errs := Parse(Fields{
		Input: ptr("3"), Output: ptr("15"),
		CacheRead: ptr("0.3"), CacheWrite: ptr("3.75"),
	})
	if len(errs) > 0 {
		t.Fatalf("Parse returned errors: %v", errs)
	}
	if !price.BreaksOutCache() {
		t.Fatal("Parse did not record the cache rates")
	}
	if *price.CacheRead != amount(t, "0.3") || *price.CacheWrite != amount(t, "3.75") {
		t.Errorf("cache rates are %s / %s, want 0.3 / 3.75", *price.CacheRead, *price.CacheWrite)
	}
}

func TestParseAcceptsZeroAsARate(t *testing.T) {
	price, errs := Parse(Fields{Input: ptr("0"), Output: ptr("0")})
	if len(errs) > 0 {
		t.Fatalf("Parse returned errors: %v", errs)
	}
	if price == nil {
		t.Fatal("Parse read a zero rate as no rate at all")
	}
	if price.Input != 0 || price.Output != 0 {
		t.Errorf("Parse gave %s / %s, want 0 / 0", price.Input, price.Output)
	}
}

func TestParseRequiresInputAndOutputTogether(t *testing.T) {
	cases := map[string]struct {
		fields Fields
		want   Bucket
	}{
		"output without input": {Fields{Output: ptr("15")}, BucketInput},
		"input without output": {Fields{Input: ptr("3")}, BucketOutput},
		"output blanked":       {Fields{Input: ptr("3"), Output: ptr("")}, BucketOutput},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			price, errs := Parse(tc.fields)
			if price != nil {
				t.Error("Parse returned a price for half a pair")
			}
			if !hasErrorFor(errs, tc.want) {
				t.Errorf("Parse errors %v do not name %s", errs, tc.want)
			}
		})
	}
}

func TestParseRequiresCacheRatesTogether(t *testing.T) {
	cases := map[string]struct {
		fields Fields
		want   Bucket
	}{
		"read without write": {
			Fields{Input: ptr("3"), Output: ptr("15"), CacheRead: ptr("0.3")}, BucketCacheWrite,
		},
		"write without read": {
			Fields{Input: ptr("3"), Output: ptr("15"), CacheWrite: ptr("3.75")}, BucketCacheRead,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			price, errs := Parse(tc.fields)
			if price != nil {
				t.Error("Parse returned a price for half a cache pair")
			}
			if !hasErrorFor(errs, tc.want) {
				t.Errorf("Parse errors %v do not name %s", errs, tc.want)
			}
		})
	}
}

func TestParseRefusesCacheRatesWithoutTheRequiredPair(t *testing.T) {
	price, errs := Parse(Fields{CacheRead: ptr("0.3"), CacheWrite: ptr("3.75")})
	if price != nil {
		t.Error("Parse returned a price with cache rates and nothing else")
	}
	if !hasErrorFor(errs, "") {
		t.Errorf("Parse errors %v do not report the combination as the fault", errs)
	}
}

func TestParseReportsEveryFaultAtOnce(t *testing.T) {
	_, errs := Parse(Fields{
		Input: ptr("-1"), Output: ptr("1.234567"), CacheRead: ptr("abc"), CacheWrite: ptr("2"),
	})

	for _, bucket := range []Bucket{BucketInput, BucketOutput, BucketCacheRead} {
		if !hasErrorFor(errs, bucket) {
			t.Errorf("Parse errors %v do not name %s", errs, bucket)
		}
	}
}

func TestParseExplainsWhyARateWasRefused(t *testing.T) {
	cases := map[string]struct {
		text     string
		contains string
	}{
		"negative":     {"-1", "negative"},
		"too precise":  {"0.00001", "decimal places"},
		"too large":    {"2000000", "at most"},
		"not a number": {"free", "for example"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Parse(Fields{Input: ptr(tc.text), Output: ptr("1")})
			idx := slices.IndexFunc(errs, func(e FieldError) bool { return e.Bucket == BucketInput })
			if idx < 0 {
				t.Fatalf("Parse errors %v do not name input", errs)
			}
			if !strings.Contains(errs[idx].Message, tc.contains) {
				t.Errorf("message %q does not explain %q", errs[idx].Message, tc.contains)
			}
		})
	}
}

func TestBucketNamesAreTheWireFieldNames(t *testing.T) {
	want := []string{"input", "output", "cacheRead", "cacheWrite"}
	for i, b := range Buckets() {
		if string(b) != want[i] {
			t.Errorf("Buckets[%d] = %q, want %q", i, b, want[i])
		}
	}
}

func hasErrorFor(errs []FieldError, bucket Bucket) bool {
	return slices.ContainsFunc(errs, func(e FieldError) bool { return e.Bucket == bucket })
}
