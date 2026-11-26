// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"context"
)

type PDFExtractor interface {
	Extract(ctx context.Context, filename string, content []byte) (*Result, error)
}

type InProcessPDFExtractor struct{}

func NewInProcessPDFExtractor() *InProcessPDFExtractor { return &InProcessPDFExtractor{} }

func (InProcessPDFExtractor) Extract(ctx context.Context, _ string, content []byte) (*Result, error) {
	type result struct {
		pages []pageEntry
		err   error
	}
	done := make(chan result, 1)
	go func() {
		pages, err := extractPDFPages(content)
		done <- result{pages: pages, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.pages) == 0 {
			return &Result{Text: "", CitationType: "page"}, nil
		}
		return resultFromPages(r.pages), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
