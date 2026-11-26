// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"math"
)

type DataResponse[T any] struct {
	Data T `json:"data"`
}

type PaginatedResponse[T any] struct {
	Data []T  `json:"data"`
	Meta Meta `json:"meta"`
}

type Meta struct {
	Pagination PaginationMeta `json:"pagination"`
}

type PaginationMeta struct {
	PageNumber int  `json:"pageNumber" example:"1"`
	Count      int  `json:"count" example:"42"`
	PageSize   int  `json:"pageSize" example:"20"`
	NextPage   *int `json:"nextPage" example:"2"`
	TotalPages int  `json:"totalPages" example:"3"`
}

func NewPaginationMeta(pageNumber, pageSize, totalCount int) Meta {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(totalCount) / float64(pageSize)))
	}
	var nextPage *int
	if pageNumber < totalPages {
		np := pageNumber + 1
		nextPage = &np
	}
	return Meta{
		Pagination: PaginationMeta{
			PageNumber: pageNumber,
			Count:      totalCount,
			PageSize:   pageSize,
			NextPage:   nextPage,
			TotalPages: totalPages,
		},
	}
}

const MaxRawBodyBytes = 8 << 20
