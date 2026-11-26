// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func servePages(t *testing.T, pages [][]string, alwaysNext bool) (*Client, *[]string) {
	t.Helper()
	var queries []string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
		var resp entplatform.PaginatedResponse[wire.APIKeyResponse]
		resp.Data = []wire.APIKeyResponse{}
		if page >= 1 && page <= len(pages) {
			for _, id := range pages[page-1] {
				resp.Data = append(resp.Data, wire.APIKeyResponse{ID: id})
			}
		}
		if alwaysNext || page < len(pages) {
			next := page + 1
			resp.Meta.Pagination.NextPage = &next
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), &queries
}

func TestListAPIKeys_WalksEveryPage(t *testing.T) {
	c, queries := servePages(t, [][]string{{"a", "b"}, {"c"}}, false)
	keys, err := c.ListAPIKeys(t.Context(), APIKeyFilter{UserID: "owner-1", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys[2].ID != "c" {
		t.Errorf("keys = %+v, want a, b, c", keys)
	}
	for i, q := range *queries {
		got, err := url.ParseQuery(q)
		if err != nil {
			t.Fatalf("request %d query %q: %v", i+1, q, err)
		}
		want := fmt.Sprintf("pageNumber=%d&pageSize=100&status=active&userId=owner-1", i+1)
		if got.Encode() != want {
			t.Errorf("request %d query = %q, want %q", i+1, got.Encode(), want)
		}
	}
}

func TestListAPIKeys_StopsOnEmptyPageWhenNextPageAlwaysAdvertised(t *testing.T) {
	c, queries := servePages(t, [][]string{{"a"}}, true)
	keys, err := c.ListAPIKeys(t.Context(), APIKeyFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(*queries) != 2 {
		t.Errorf("got %d keys over %d requests, want 1 over 2", len(keys), len(*queries))
	}
}

func TestListAPIKeys_EmptyIsNotNil(t *testing.T) {
	c, _ := servePages(t, nil, false)
	keys, err := c.ListAPIKeys(t.Context(), APIKeyFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if keys == nil {
		t.Error("an empty inventory is nil, which marshals to null")
	}
}
