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
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestListToolsets_WalksEveryPage(t *testing.T) {
	const pages = 3
	var queries []string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
		resp := entplatform.PaginatedResponse[wire.ToolsetResponse]{
			Data: []wire.ToolsetResponse{{ID: fmt.Sprintf("id-%d", page), Name: fmt.Sprintf("ts-%d", page)}},
		}
		if page < pages {
			next := page + 1
			resp.Meta.Pagination.NextPage = &next
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	got, err := NewClient(target.Resolved{URL: srv.URL}).ListToolsets(t.Context())
	if err != nil {
		t.Fatalf("ListToolsets: %v", err)
	}
	if len(got) != pages {
		t.Fatalf("got %d toolsets, want %d (one per page)", len(got), pages)
	}
	if want := "origin=custom&pageNumber=1&pageSize=100"; queries[0] != want {
		t.Errorf("first query = %q, want %q", queries[0], want)
	}
}

func TestListAllPages_StopsOnEmptyPage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		next := calls + 1
		resp := entplatform.PaginatedResponse[wire.RoleResponse]{Data: []wire.RoleResponse{}}
		resp.Meta.Pagination.NextPage = &next
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	got, err := NewClient(target.Resolved{URL: srv.URL}).ListRBACRoles(t.Context())
	if err != nil {
		t.Fatalf("ListRBACRoles: %v", err)
	}
	if calls != 1 || len(got) != 0 {
		t.Errorf("calls = %d, roles = %d; want 1 call and no roles", calls, len(got))
	}
}

func TestListAllPages_FailsWhenPagingNeverEnds(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		next := 2
		resp := entplatform.PaginatedResponse[wire.RoleResponse]{Data: []wire.RoleResponse{{ID: "r1", Name: "same"}}}
		resp.Meta.Pagination.NextPage = &next
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(target.Resolved{URL: srv.URL}).ListRBACRoles(t.Context()); err == nil {
		t.Fatal("a server that never stops paging must fail the list, not return a partial one")
	}
	if calls != maxListPages {
		t.Errorf("calls = %d, want %d", calls, maxListPages)
	}
}
