// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"io"
)

type ReaderOpener interface {
	OpenReader(ctx context.Context, loc Locator, version *int) (rc io.ReadCloser, mimeType string, size int64, err error)
}
