// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

type ResourceStore interface {
	Put(ctx context.Context, key string, r io.Reader) error
	PutWithMetadata(ctx context.Context, key string, r io.Reader, metadata map[string]string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Head(ctx context.Context, key string) (map[string]string, error)
	Delete(ctx context.Context, key string) error
}

const extractedZIPSuffix = ".extracted"

func getRuntimeZIP(ctx context.Context, rs ResourceStore, key string) (io.ReadCloser, error) {
	rc, err := rs.Get(ctx, key)
	if err == nil || !objectstore.IsNotFound(err) {
		return rc, err
	}
	extracted, extractedErr := rs.Get(ctx, key+extractedZIPSuffix)
	if extractedErr == nil {
		return extracted, nil
	}
	if !objectstore.IsNotFound(extractedErr) {
		return nil, extractedErr
	}
	return nil, err
}

func deleteRuntimeZIP(ctx context.Context, rs ResourceStore, key string) error {
	if err := rs.Delete(ctx, key); err != nil {
		return err
	}
	return rs.Delete(ctx, key+extractedZIPSuffix)
}
