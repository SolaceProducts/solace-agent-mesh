// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

type (
	DataResponse[T any]      = apicontract.DataResponse[T]
	PaginatedResponse[T any] = apicontract.PaginatedResponse[T]
	Meta                     = apicontract.Meta
	PaginationMeta           = apicontract.PaginationMeta
)

func NewPaginationMeta(pageNumber, pageSize, totalCount int) Meta {
	return apicontract.NewPaginationMeta(pageNumber, pageSize, totalCount)
}

func unpagedResponse[T any](data []T) PaginatedResponse[T] {
	return PaginatedResponse[T]{
		Data: apicontract.NonNilList(data),
		Meta: NewPaginationMeta(1, len(data), len(data)),
	}
}
