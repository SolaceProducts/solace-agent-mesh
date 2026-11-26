// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"net/http"
)

type authHeaderFetcher func(ctx context.Context) (http.Header, error)

type bearerRoundTripper struct {
	fetch authHeaderFetcher
	base  http.RoundTripper
}

func (t *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	if req.Header.Get("Authorization") != "" {
		return base.RoundTrip(req)
	}

	if t.fetch == nil {
		return nil, fmt.Errorf("bearerRoundTripper: fetch is nil")
	}
	hdrs, err := t.fetch(req.Context())
	if err != nil {
		return nil, err
	}
	return base.RoundTrip(cloneSoRetriesDoNotAccumulateHeaders(req, hdrs))
}

func cloneSoRetriesDoNotAccumulateHeaders(req *http.Request, hdrs http.Header) *http.Request {
	clone := req.Clone(req.Context())
	for k, vs := range hdrs {
		for _, v := range vs {
			clone.Header.Add(k, v)
		}
	}
	return clone
}
