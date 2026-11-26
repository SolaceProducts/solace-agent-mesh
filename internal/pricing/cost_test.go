// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"math/big"
	"testing"
)

func TestCostIsTheBucketRateTimesTheTokens(t *testing.T) {
	cacheRead := amount(t, "0.3")
	cacheWrite := amount(t, "3.75")
	price := ModelPrice{
		Input:      amount(t, "3"),
		Output:     amount(t, "15"),
		CacheRead:  &cacheRead,
		CacheWrite: &cacheWrite,
	}

	for bucket, want := range map[Bucket]int64{
		BucketInput:      30_000 * 1_000,
		BucketOutput:     150_000 * 1_000,
		BucketCacheRead:  3_000 * 1_000,
		BucketCacheWrite: 37_500 * 1_000,
	} {
		if got := price.Cost(bucket, 1_000); got != want {
			t.Errorf("Cost(%s, 1000) = %d, want %d", bucket, got, want)
		}
	}
}

func TestOneTokenAtThreeDollarsPerMillionCostsThreeMillionthsOfADollar(t *testing.T) {
	price := ModelPrice{Input: amount(t, "3"), Output: amount(t, "3")}

	const threeMillionthsAtTenPlaces = 30_000
	if got := price.Cost(BucketInput, 1); got != threeMillionthsAtTenPlaces {
		t.Errorf("Cost(input, 1) = %d, want %d", got, threeMillionthsAtTenPlaces)
	}
}

func TestCostOfACacheBucketNotBrokenOutIsBilledAtTheInputRate(t *testing.T) {
	price := ModelPrice{Input: amount(t, "3"), Output: amount(t, "15")}

	for _, bucket := range []Bucket{BucketCacheRead, BucketCacheWrite} {
		if got, want := price.Cost(bucket, 500), price.Cost(BucketInput, 500); got != want {
			t.Errorf("Cost(%s, 500) = %d, want the input cost %d", bucket, got, want)
		}
	}
}

func TestCostAtTheMaximumTokenCountAndRateDoesNotOverflow(t *testing.T) {
	price := ModelPrice{Input: MaxAmount, Output: MaxAmount}

	got := price.Cost(BucketInput, MaxTokensBeforeCostOverflows)

	want := new(big.Int).Mul(big.NewInt(int64(MaxAmount)), big.NewInt(MaxTokensBeforeCostOverflows))
	if !want.IsInt64() {
		t.Fatalf("the exact cost %s does not fit an int64", want)
	}
	if got != want.Int64() {
		t.Errorf("Cost at the documented limit = %d, want exactly %s", got, want)
	}
}

func TestCostRefusesCountsAndRatesThatCouldOverflow(t *testing.T) {
	cases := map[string]struct {
		price  ModelPrice
		tokens int64
	}{
		"negative tokens":       {ModelPrice{Input: 1}, -1},
		"tokens over the limit": {ModelPrice{Input: 1}, MaxTokensBeforeCostOverflows + 1},
		"negative rate":         {ModelPrice{Input: -1}, 1},
		"rate over the maximum": {ModelPrice{Input: MaxAmount + 1}, MaxTokensBeforeCostOverflows},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Cost(input, %d) at rate %d did not panic", c.tokens, c.price.Input)
				}
			}()
			c.price.Cost(BucketInput, c.tokens)
		})
	}
}
