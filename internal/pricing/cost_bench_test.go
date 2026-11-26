// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pricing

import "testing"

func BenchmarkModelPrice_Cost(b *testing.B) {
	cacheRead, cacheWrite := Amount(300_000), Amount(3_750_000)
	price := ModelPrice{Input: 3_000_000, Output: 15_000_000, CacheRead: &cacheRead, CacheWrite: &cacheWrite}
	tokens := map[Bucket]int64{
		BucketInput:      4_000,
		BucketOutput:     1_200,
		BucketCacheRead:  60_000,
		BucketCacheWrite: 8_000,
	}

	for _, bucket := range Buckets() {
		count := tokens[bucket]
		b.Run(string(bucket), func(b *testing.B) {
			b.ReportAllocs()
			var sink int64
			for b.Loop() {
				sink += price.Cost(bucket, count)
			}
			_ = sink
		})
	}
}
