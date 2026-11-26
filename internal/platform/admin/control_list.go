// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const controlListMaxRows = 1000

const controlListMaxBytes = broker.MaxMessageSize / 2

const controlListPageSize = 100

func listEveryPage[T any](fetch func(pageNumber, pageSize int) (*PaginatedResponse[T], error)) (*PaginatedResponse[T], error) {
	rows := make([]T, 0, controlListPageSize)
	total, size := 0, 0
	for page := 1; ; page++ {
		resp, err := fetch(page, controlListPageSize)
		if err != nil {
			return nil, err
		}
		total = resp.Meta.Pagination.Count
		pageSize, err := encodedSize(resp.Data)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && size+pageSize > controlListMaxBytes {
			break
		}
		rows = append(rows, resp.Data...)
		size += pageSize
		if len(resp.Data) < controlListPageSize || len(rows) >= total || len(rows) >= controlListMaxRows || size >= controlListMaxBytes {
			break
		}
	}
	return &PaginatedResponse[T]{Data: rows, Meta: NewPaginationMeta(1, len(rows), max(total, len(rows)))}, nil
}

func encodedSize[T any](rows []T) (int, error) {
	raw, err := json.Marshal(rows)
	if err != nil {
		return 0, err
	}
	return len(raw), nil
}
