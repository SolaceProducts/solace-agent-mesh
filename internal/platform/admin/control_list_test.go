// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func TestListEveryPage_ConcatenatesPagesAndKeepsTheTotal(t *testing.T) {
	const total = 250
	var asked []int
	fetch := func(pageNumber, pageSize int) (*PaginatedResponse[int], error) {
		asked = append(asked, pageNumber)
		start := (pageNumber - 1) * pageSize
		rows := make([]int, 0, pageSize)
		for i := start; i < min(start+pageSize, total); i++ {
			rows = append(rows, i)
		}
		return &PaginatedResponse[int]{Data: rows, Meta: NewPaginationMeta(pageNumber, pageSize, total)}, nil
	}

	resp, err := listEveryPage(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != total || resp.Data[0] != 0 || resp.Data[total-1] != total-1 {
		t.Errorf("got %d rows, want all %d in order", len(resp.Data), total)
	}
	if resp.Meta.Pagination.Count != total {
		t.Errorf("count = %d, want %d", resp.Meta.Pagination.Count, total)
	}
	if len(asked) != 3 {
		t.Errorf("asked pages %v, want three", asked)
	}
}

func TestListEveryPage_StopsAtTheCeilingAndReportsTheRealTotal(t *testing.T) {
	const total = controlListMaxRows + 150
	fetch := func(pageNumber, pageSize int) (*PaginatedResponse[int], error) {
		rows := make([]int, pageSize)
		return &PaginatedResponse[int]{Data: rows, Meta: NewPaginationMeta(pageNumber, pageSize, total)}, nil
	}

	resp, err := listEveryPage(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != controlListMaxRows {
		t.Errorf("got %d rows, want the ceiling %d", len(resp.Data), controlListMaxRows)
	}
	if resp.Meta.Pagination.Count != total {
		t.Errorf("count = %d, want the real total %d so the caller can say what was cut", resp.Meta.Pagination.Count, total)
	}
}

func TestListEveryPage_StopsAtTheByteCeilingAndReportsTheRealTotal(t *testing.T) {
	const total = 1000
	row := strings.Repeat("x", controlListMaxBytes/(3*controlListPageSize)-8)
	var asked []int
	fetch := func(pageNumber, pageSize int) (*PaginatedResponse[string], error) {
		asked = append(asked, pageNumber)
		rows := make([]string, pageSize)
		for i := range rows {
			rows[i] = row
		}
		return &PaginatedResponse[string]{Data: rows, Meta: NewPaginationMeta(pageNumber, pageSize, total)}, nil
	}

	resp, err := listEveryPage(fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 4 || len(resp.Data) != 3*controlListPageSize {
		t.Errorf("asked pages %v and kept %d rows, want four asked and three pages kept", asked, len(resp.Data))
	}
	if resp.Meta.Pagination.Count != total {
		t.Errorf("count = %d, want the real total %d so the caller can say what was cut", resp.Meta.Pagination.Count, total)
	}
	if raw, _ := json.Marshal(resp); len(raw) > controlListMaxBytes {
		t.Errorf("reply is %d bytes, over the %d ceiling", len(raw), controlListMaxBytes)
	}
}

func TestListEveryPage_AFirstPageOverTheByteCeilingIsStillReturned(t *testing.T) {
	row := strings.Repeat("x", controlListMaxBytes/controlListPageSize)
	resp, err := listEveryPage(func(pageNumber, pageSize int) (*PaginatedResponse[string], error) {
		rows := make([]string, pageSize)
		for i := range rows {
			rows[i] = row
		}
		return &PaginatedResponse[string]{Data: rows, Meta: NewPaginationMeta(pageNumber, pageSize, 5000)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != controlListPageSize || resp.Meta.Pagination.Count != 5000 {
		t.Errorf("got %d rows with count %d, want the first page and the real total", len(resp.Data), resp.Meta.Pagination.Count)
	}
}

func TestListEveryPage_EmptyListIsAnEmptyArray(t *testing.T) {
	resp, err := listEveryPage(func(pageNumber, pageSize int) (*PaginatedResponse[int], error) {
		return &PaginatedResponse[int]{Data: []int{}, Meta: NewPaginationMeta(pageNumber, pageSize, 0)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp)
	if string(raw) != `{"data":[],"meta":{"pagination":{"pageNumber":1,"count":0,"pageSize":0,"nextPage":null,"totalPages":0}}}` {
		t.Errorf("got %s", raw)
	}
}

func TestListEveryPage_ReturnsTheFetchError(t *testing.T) {
	_, err := listEveryPage(func(int, int) (*PaginatedResponse[int], error) { return nil, fmt.Errorf("boom") })
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v", err)
	}
}

func TestControlProtocol_ListToolsBeyondOnePage(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	const registered = controlListPageSize + 7
	for i := range registered {
		env.ToolSvc.UpsertFromInit(ctx, remotetool.InitParams{ToolName: fmt.Sprintf("paged-tool-%03d", i), Description: "one of many"})
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "tools", nil)
	if err != nil {
		t.Fatalf("custom request: %v", err)
	}
	var resp PaginatedResponse[ToolDetailResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Data) != registered || resp.Meta.Pagination.Count != registered {
		t.Errorf("got %d rows with count %d, want all %d", len(resp.Data), resp.Meta.Pagination.Count, registered)
	}
	seen := make(map[string]bool, registered)
	for _, tl := range resp.Data {
		seen[tl.Name] = true
	}
	if len(seen) != registered {
		t.Errorf("listing repeats rows: %d distinct of %d", len(seen), registered)
	}
}

func TestControlProtocol_ListConnectorsCountsOnlyTheVisibleTypes(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)
	ctx := t.Context()

	const visible = controlListMaxRows + 1
	for i := range visible {
		req := CreateConnectorRequest{Name: fmt.Sprintf("shown-%04d", i), Type: "sql", Subtype: "postgres", Description: "a type every deployment lists", Values: map[string]any{"host": "localhost", "port": 5432}}
		if _, err := env.ConnectorSvc.Create(ctx, req, "ctrl-user"); err != nil {
			t.Fatalf("create sql connector: %v", err)
		}
	}
	gated := CreateConnectorRequest{Name: "gated", Type: "email", Subtype: "smtp", Description: "a type the feature flag hides", Values: map[string]any{"host": "localhost", "port": 587}}
	if _, err := env.ConnectorSvc.Create(ctx, gated, "ctrl-user"); err != nil {
		t.Fatalf("create email connector: %v", err)
	}

	raw, err := env.Client.Custom(ctx, "platform", "get", "connectors", nil)
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	var resp PaginatedResponse[ConnectorResponse]
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Data) != controlListMaxRows || resp.Meta.Pagination.Count != visible {
		t.Errorf("got %d rows with count %d, want the ceiling %d of the %d visible connectors", len(resp.Data), resp.Meta.Pagination.Count, controlListMaxRows, visible)
	}
	for _, c := range resp.Data {
		if c.Type == "email" {
			t.Fatalf("the hidden type was listed: %s", c.Name)
		}
	}
}
