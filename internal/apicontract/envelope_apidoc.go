// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (DataResponse[_]) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "DataResponse wraps a single item in a \"data\" envelope.",
		Fields: map[string]apidoc.Field{
			"Data": {Doc: "Data is the wrapped item."},
		},
	}
}

func (PaginatedResponse[_]) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "PaginatedResponse wraps a list with pagination metadata.",
		Fields: map[string]apidoc.Field{
			"Data": {Doc: "Data holds the items on this page."},
			"Meta": {Doc: "Meta describes the page."},
		},
	}
}

func (Meta) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "Meta wraps the pagination metadata.",
		Fields: map[string]apidoc.Field{
			"Pagination": {Doc: "Pagination describes the position of this page in the full result."},
		},
	}
}

func (PaginationMeta) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "PaginationMeta holds pagination details.",
		Fields: map[string]apidoc.Field{
			"PageNumber": {Doc: "PageNumber is the 1-based number of this page."},
			"Count":      {Doc: "Count is the total number of items across all pages."},
			"PageSize":   {Doc: "PageSize is the maximum number of items per page."},
			"NextPage":   {Doc: "NextPage is the number of the next page, or null on the last page."},
			"TotalPages": {Doc: "TotalPages is the number of pages available."},
		},
	}
}
