// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"fmt"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const listPageSize = 100

const maxListPages = 100

func listAllPages[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	return listPagesUpTo[T](ctx, c, path, maxListPages)
}

func listPagesUpTo[T any](ctx context.Context, c *Client, path string, maxPages int) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	out := make([]T, 0)
	for page := 1; page <= maxPages; page++ {
		var resp entplatform.PaginatedResponse[T]
		if err := c.do(ctx, "GET", fmt.Sprintf("%s%spageNumber=%d&pageSize=%d", path, sep, page, listPageSize), nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Data...)
		if len(resp.Data) == 0 || resp.Meta.Pagination.NextPage == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("GET %s: still paging after %d pages", path, maxPages)
}
