// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"errors"
	"math/big"
	"testing"
)

func TestParseAmountAcceptsOperatorInput(t *testing.T) {
	tests := []struct {
		text string
		want Amount
	}{
		{"0", 0},
		{"0.0", 0},
		{"3", 30_000},
		{"3.00", 30_000},
		{"0.0237", 237},
		{"0.002", 20},
		{"0.0001", 1},
		{".5", 5_000},
		{"+1.25", 12_500},
		{"  2.5  ", 25_000},
		{"75", 750_000},
		{"1000000", MaxAmount},
	}

	for _, tc := range tests {
		got, err := ParseAmount(tc.text)
		if err != nil {
			t.Errorf("ParseAmount(%q) returned error: %v", tc.text, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseAmount(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

func TestParseAmountRejectsUnusableInput(t *testing.T) {
	tests := []struct {
		text string
		want error
	}{
		{"", ErrMalformedAmount},
		{"   ", ErrMalformedAmount},
		{"abc", ErrMalformedAmount},
		{"1.2.3", ErrMalformedAmount},
		{"1,000", ErrMalformedAmount},
		{"1e5", ErrMalformedAmount},
		{".", ErrMalformedAmount},
		{"-1", ErrNegativeAmount},
		{"-0.5", ErrNegativeAmount},
		{"0.00001", ErrAmountTooPrecise},
		{"1.23456", ErrAmountTooPrecise},
		{"1000000.0001", ErrAmountTooLarge},
		{"99999999999999999999", ErrAmountTooLarge},
	}

	for _, tc := range tests {
		_, err := ParseAmount(tc.text)
		if !errors.Is(err, tc.want) {
			t.Errorf("ParseAmount(%q) error = %v, want %v", tc.text, err, tc.want)
		}
	}
}

func TestAmountRoundTripsThroughText(t *testing.T) {
	for _, text := range []string{"0", "3", "3.5", "0.0237", "0.0001", "75.25", "1000000"} {
		parsed, err := ParseAmount(text)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", text, err)
		}
		got := parsed.String()
		if got != text {
			t.Errorf("ParseAmount(%q).String() = %q, want %q", text, got, text)
		}
		again, err := ParseAmount(got)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", got, err)
		}
		if again != parsed {
			t.Errorf("re-parsing %q gave %d, want %d", got, again, parsed)
		}
	}
}

func TestAmountStringTrimsTrailingZeros(t *testing.T) {
	tests := map[Amount]string{
		0:       "0",
		1:       "0.0001",
		20:      "0.002",
		237:     "0.0237",
		30_000:  "3",
		12_500:  "1.25",
		750_000: "75",
	}
	for amount, want := range tests {
		if got := amount.String(); got != want {
			t.Errorf("Amount(%d).String() = %q, want %q", amount, got, want)
		}
	}
}

func TestPriceScaleDividesIntoCostScaleExactly(t *testing.T) {
	priceScale := new(big.Rat).SetFrac64(1, pow10(DecimalPlaces))
	perToken := new(big.Rat).Quo(priceScale, new(big.Rat).SetInt64(TokenQuantity))
	costScale := new(big.Rat).SetFrac64(1, pow10(CostDecimalPlaces))

	if perToken.Cmp(costScale) != 0 {
		t.Fatalf("one token at the smallest rate is %s, want the cost scale %s",
			perToken.RatString(), costScale.RatString())
	}
}

func TestCostIsRateTimesTokenCount(t *testing.T) {
	cases := []struct {
		rate   string
		tokens int64
	}{
		{"0.002", 1},
		{"3", 1_000_000},
		{"0.0237", 12_345},
		{"75.25", 987_654},
	}

	for _, tc := range cases {
		rate, err := ParseAmount(tc.rate)
		if err != nil {
			t.Fatalf("ParseAmount(%q): %v", tc.rate, err)
		}

		exact := new(big.Rat).SetFrac64(int64(rate), pow10(DecimalPlaces))
		exact.Mul(exact, new(big.Rat).SetInt64(tc.tokens))
		exact.Quo(exact, new(big.Rat).SetInt64(TokenQuantity))
		exact.Mul(exact, new(big.Rat).SetInt64(pow10(CostDecimalPlaces)))

		if !exact.IsInt() {
			t.Errorf("rate %q over %d tokens is not a whole number of cost units", tc.rate, tc.tokens)
			continue
		}
		want := exact.Num().Int64()

		if got := int64(rate) * tc.tokens; got != want {
			t.Errorf("rate %q over %d tokens: rate*tokens = %d, exact = %d", tc.rate, tc.tokens, got, want)
		}
	}
}

func TestOneTokenAtTheSmallestRateIsNotLost(t *testing.T) {
	smallest := new(big.Rat).SetFrac64(1, pow10(DecimalPlaces))
	rate, err := ParseAmount(smallest.FloatString(DecimalPlaces))
	if err != nil {
		t.Fatalf("the smallest representable rate was rejected: %v", err)
	}
	if rate == 0 {
		t.Fatalf("the smallest representable rate parsed to zero, which means free")
	}

	cost := int64(rate) * 1
	if cost == 0 {
		t.Fatalf("one token at the smallest rate cost 0 — the token is lost")
	}

	want := new(big.Rat).Quo(smallest, new(big.Rat).SetInt64(TokenQuantity))
	got := new(big.Rat).SetFrac64(cost, pow10(CostDecimalPlaces))
	if got.Cmp(want) != 0 {
		t.Errorf("one token at the smallest rate stored as %s, exact is %s",
			got.RatString(), want.RatString())
	}
}

func TestARateFinerThanTheScaleIsRefused(t *testing.T) {
	tooFine := new(big.Rat).SetFrac64(1, pow10(DecimalPlaces+1))

	if _, err := ParseAmount(tooFine.FloatString(DecimalPlaces + 1)); err == nil {
		t.Errorf("a rate one decimal place finer than the scale was accepted")
	} else if !errors.Is(err, ErrAmountTooPrecise) {
		t.Errorf("error was %v, want ErrAmountTooPrecise", err)
	}
}

func TestMaxAmountLeavesHeadroomForACostMultiply(t *testing.T) {
	const wantTokens = 900_000_000

	if MaxTokensBeforeCostOverflows < wantTokens {
		t.Errorf("MaxAmount leaves room for %d tokens in one call, want at least %d",
			MaxTokensBeforeCostOverflows, wantTokens)
	}
}

func TestResolveBillsAbsentCacheAtTheInputRate(t *testing.T) {
	input := amount(t, "3")
	output := amount(t, "15")

	price := ModelPrice{Input: input, Output: output}

	for bucket, want := range map[Bucket]Amount{
		BucketInput:      input,
		BucketOutput:     output,
		BucketCacheRead:  input,
		BucketCacheWrite: input,
	} {
		if got := price.Resolve(bucket); got != want {
			t.Errorf("Resolve(%s) = %s, want %s", bucket, got, want)
		}
	}
	if price.BreaksOutCache() {
		t.Error("BreaksOutCache() = true for a price with no cache rates")
	}
}

func TestResolveUsesStatedCacheRates(t *testing.T) {
	cacheRead := amount(t, "0.3")
	cacheWrite := amount(t, "3.75")
	price := ModelPrice{
		Input:      amount(t, "3"),
		Output:     amount(t, "15"),
		CacheRead:  &cacheRead,
		CacheWrite: &cacheWrite,
	}

	if got := price.Resolve(BucketCacheRead); got != cacheRead {
		t.Errorf("Resolve(cacheRead) = %s, want %s", got, cacheRead)
	}
	if got := price.Resolve(BucketCacheWrite); got != cacheWrite {
		t.Errorf("Resolve(cacheWrite) = %s, want %s", got, cacheWrite)
	}
	if !price.BreaksOutCache() {
		t.Error("BreaksOutCache() = false for a price with both cache rates")
	}
}

func TestZeroIsARate(t *testing.T) {
	zero := Amount(0)
	price := ModelPrice{Input: amount(t, "3"), Output: amount(t, "15"), CacheRead: &zero, CacheWrite: &zero}

	if got := price.Resolve(BucketCacheRead); got != 0 {
		t.Errorf("Resolve(cacheRead) = %s, want 0", got)
	}
	if !price.BreaksOutCache() {
		t.Error("BreaksOutCache() = false for a price whose cache rates are zero")
	}
}

func TestBucketsCoversEveryBucketOnce(t *testing.T) {
	seen := map[Bucket]bool{}
	for _, b := range Buckets() {
		if seen[b] {
			t.Errorf("bucket %q listed twice", b)
		}
		seen[b] = true
	}
	for _, b := range []Bucket{BucketInput, BucketOutput, BucketCacheRead, BucketCacheWrite} {
		if !seen[b] {
			t.Errorf("bucket %q missing from Buckets", b)
		}
	}
}

func TestBucketsCannotBeReorderedByACaller(t *testing.T) {
	Buckets()[0] = BucketCacheWrite
	if got := Buckets()[0]; got != BucketInput {
		t.Errorf("Buckets()[0] = %q after a caller wrote to its copy, want %q", got, BucketInput)
	}
}

func amount(t *testing.T, text string) Amount {
	t.Helper()
	a, err := ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return a
}

func pow10(n int) int64 {
	result := int64(1)
	for range n {
		result *= 10
	}
	return result
}

func TestFormatCostRendersTenFractionalDigits(t *testing.T) {
	cases := map[int64]string{
		0:               "0.0000000000",
		12:              "0.0000000012",
		142_000_000:     "0.0142000000",
		10_000_000_000:  "1.0000000000",
		-5_000_000_000:  "-0.5000000000",
		123_456_789_012: "12.3456789012",
	}
	for v, want := range cases {
		if got := FormatCost(v); got != want {
			t.Errorf("FormatCost(%d) = %q, want %q", v, got, want)
		}
	}
}
