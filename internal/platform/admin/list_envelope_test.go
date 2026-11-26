// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func assertDataIsEmptyArray(t *testing.T, body []byte) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if got := string(envelope.Data); got != "[]" {
		t.Errorf("data = %s, want []", got)
	}
}

func assertMarshalsWithEmptyData(t *testing.T, resp any) {
	t.Helper()
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertDataIsEmptyArray(t, body)
}

func TestListEnvelope_EmptyToolsetsIsArray(t *testing.T) {
	f := newToolsetFixture(t)

	resp, err := f.svc.ListPaginated(t.Context(), 1, 10, "custom", nil)
	if err != nil {
		t.Fatalf("list toolsets: %v", err)
	}
	assertMarshalsWithEmptyData(t, resp)
}

func TestListEnvelope_EmptyResultsAreArrays(t *testing.T) {
	comparators := map[string]func(a, b int) int{"value": func(a, b int) int { return a - b }}
	page := func(t *testing.T, data []int, pageNumber int) PaginatedResponse[int] {
		t.Helper()
		resp, err := sortAndPage(data, pageNumber, 20, "value", "asc", "value", comparators)
		if err != nil {
			t.Fatalf("sortAndPage: %v", err)
		}
		return resp
	}

	t.Run("sortAndPage on an empty list", func(t *testing.T) {
		assertMarshalsWithEmptyData(t, page(t, []int{}, 1))
	})
	t.Run("sortAndPage past the last page", func(t *testing.T) {
		assertMarshalsWithEmptyData(t, page(t, []int{1, 2}, 9))
	})
	t.Run("unpagedResponse of a nil list", func(t *testing.T) {
		assertMarshalsWithEmptyData(t, unpagedResponse[int](nil))
	})
	t.Run("paginateBuiltinSkills with an empty catalog", func(t *testing.T) {
		assertMarshalsWithEmptyData(t, paginateBuiltinSkills(nil, 1, 20))
	})
}
