// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestConsistentTotalFollowsThePageWhenTheCachedCountLags(t *testing.T) {
	rows := func(n int) []*store.Session { return make([]*store.Session, n) }
	cases := []struct {
		name             string
		page             sessionPage
		pageNumber, size int
		want             int
	}{
		{"fresh count is kept", sessionPage{rows: rows(20), total: 45, hasMore: true}, 1, 20, 45},
		{"stale count too low on a page with more behind it", sessionPage{rows: rows(20), total: 20, hasMore: true}, 1, 20, 21},
		{"stale count too high on the last page", sessionPage{rows: rows(5), total: 60, hasMore: false}, 2, 20, 25},
		{"exact last page is kept", sessionPage{rows: rows(5), total: 25, hasMore: false}, 2, 20, 25},
		{"empty page past the end keeps the count", sessionPage{rows: rows(0), total: 3, hasMore: false}, 3, 20, 3},
		{"full last page with a stale cached total", sessionPage{rows: rows(20), total: 60, hasMore: false}, 2, 20, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.page.consistentTotal(tc.pageNumber, tc.size); got != tc.want {
				t.Errorf("consistentTotal = %d, want %d", got, tc.want)
			}
		})
	}
}
