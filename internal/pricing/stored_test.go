// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import (
	"errors"
	"testing"
)

func TestStoredRoundTripsAPrice(t *testing.T) {
	cacheRead, cacheWrite := amount(t, "0.3"), amount(t, "3.75")
	cases := map[string]*ModelPrice{
		"unpriced":      nil,
		"required pair": {Input: amount(t, "3"), Output: amount(t, "15")},
		"free":          {Input: 0, Output: 0},
		"with cache":    {Input: amount(t, "3"), Output: amount(t, "15"), CacheRead: &cacheRead, CacheWrite: &cacheWrite},
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Store(want).Price()
			if err != nil {
				t.Fatalf("Price() of what Store wrote: %v", err)
			}
			switch {
			case want == nil && got != nil:
				t.Fatalf("an unpriced model came back priced: %+v", *got)
			case want == nil:
				return
			case got == nil:
				t.Fatal("a priced model came back unpriced")
			}
			if got.Input != want.Input || got.Output != want.Output {
				t.Errorf("got %s / %s, want %s / %s", got.Input, got.Output, want.Input, want.Output)
			}
			if got.BreaksOutCache() != want.BreaksOutCache() {
				t.Fatalf("BreaksOutCache() = %v, want %v", got.BreaksOutCache(), want.BreaksOutCache())
			}
			if want.BreaksOutCache() && (*got.CacheRead != *want.CacheRead || *got.CacheWrite != *want.CacheWrite) {
				t.Errorf("cache rates %s / %s, want %s / %s",
					*got.CacheRead, *got.CacheWrite, *want.CacheRead, *want.CacheWrite)
			}
		})
	}
}

func TestStoredReportsARowStoreCouldNotHaveWritten(t *testing.T) {
	ok, stray := int64(30_000), int64(1)
	for name, stored := range map[string]Stored{
		"output missing":          {Input: &ok},
		"input missing":           {Output: &ok},
		"cache write missing":     {Input: &ok, Output: &ok, CacheRead: &stray},
		"cache read missing":      {Input: &ok, Output: &ok, CacheWrite: &stray},
		"cache without the pair":  {CacheRead: &ok, CacheWrite: &ok},
		"cache without an output": {Input: &ok, CacheRead: &ok, CacheWrite: &ok},
	} {
		price, err := stored.Price()
		if !errors.Is(err, ErrStoredPriceInvalid) {
			t.Errorf("%s: err = %v, want ErrStoredPriceInvalid", name, err)
		}
		if price != nil {
			t.Errorf("%s: read as %+v alongside the error", name, *price)
		}
	}
}

func TestStoredReadsFourNullsAsUnpriced(t *testing.T) {
	price, err := Stored{}.Price()
	if err != nil || price != nil {
		t.Errorf("Stored{}.Price() = %v, %v; want nil, nil", price, err)
	}
}

func TestStoreWritesNullsForAnUnpricedModel(t *testing.T) {
	stored := Store(nil)
	if stored.Input != nil || stored.Output != nil || stored.CacheRead != nil || stored.CacheWrite != nil {
		t.Errorf("Store(nil) = %+v, want every column null", stored)
	}
}

func TestStoreClearsCacheColumnsWhenCacheIsNotBrokenOut(t *testing.T) {
	stored := Store(&ModelPrice{Input: amount(t, "3"), Output: amount(t, "15")})

	if stored.Input == nil || stored.Output == nil {
		t.Fatal("Store dropped the required pair")
	}
	if stored.CacheRead != nil || stored.CacheWrite != nil {
		t.Errorf("Store kept cache columns %v / %v, want both null", stored.CacheRead, stored.CacheWrite)
	}
}

func TestResolveAgreesWithStoreOnAHalfSetCachePair(t *testing.T) {
	stray := amount(t, "0.3")
	for name, price := range map[string]ModelPrice{
		"cache write missing": {Input: amount(t, "3"), Output: amount(t, "15"), CacheRead: &stray},
		"cache read missing":  {Input: amount(t, "3"), Output: amount(t, "15"), CacheWrite: &stray},
	} {
		t.Run(name, func(t *testing.T) {
			for _, bucket := range []Bucket{BucketCacheRead, BucketCacheWrite} {
				if got := price.Resolve(bucket); got != price.Input {
					t.Errorf("Resolve(%s) = %s, want the input rate %s", bucket, got, price.Input)
				}
			}

			stored, err := Store(&price).Price()
			if err != nil {
				t.Fatalf("Price() of what Store wrote: %v", err)
			}
			if stored == nil {
				t.Fatal("read back as unpriced")
			}
			for _, bucket := range []Bucket{BucketCacheRead, BucketCacheWrite} {
				if before, after := price.Resolve(bucket), stored.Resolve(bucket); before != after {
					t.Errorf("Resolve(%s) is %s in memory and %s after a round trip", bucket, before, after)
				}
			}
		})
	}
}

func TestStoredRejectsAColumnOutsideTheRange(t *testing.T) {
	ok, tooLarge, negative := int64(30_000), int64(MaxAmount)+1, int64(-1)

	for name, stored := range map[string]Stored{
		"input too large":      {Input: &tooLarge, Output: &ok},
		"output too large":     {Input: &ok, Output: &tooLarge},
		"input negative":       {Input: &negative, Output: &ok},
		"output negative":      {Input: &ok, Output: &negative},
		"cache read too large": {Input: &ok, Output: &ok, CacheRead: &tooLarge, CacheWrite: &ok},
		"cache write negative": {Input: &ok, Output: &ok, CacheRead: &ok, CacheWrite: &negative},
	} {
		if price, err := stored.Price(); !errors.Is(err, ErrStoredPriceInvalid) || price != nil {
			t.Errorf("%s: Price() = %v, %v; want nil, ErrStoredPriceInvalid", name, price, err)
		}
	}
}

func TestStoredAcceptsTheMaximumRate(t *testing.T) {
	max := int64(MaxAmount)
	if got, err := (Stored{Input: &max, Output: &max, CacheRead: &max, CacheWrite: &max}).Price(); err != nil || got == nil {
		t.Fatalf("MaxAmount read as %v, %v", got, err)
	}
}
