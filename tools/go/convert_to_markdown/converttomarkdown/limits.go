// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"archive/zip"
	"fmt"
	"io"
)

var (
	maxUncompressedBytesPerEntry int64 = 100 * 1024 * 1024
	maxUncompressedBytesTotal    int64 = 500 * 1024 * 1024
)

type zipBudget struct {
	remaining int64
}

func newZipBudget() *zipBudget {
	return &zipBudget{remaining: maxUncompressedBytesTotal}
}

func readCappedZipEntry(f *zip.File, budget *zipBudget) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	limit := maxUncompressedBytesPerEntry
	if budget != nil && budget.remaining < limit {
		limit = budget.remaining
	}

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("read %s: uncompressed content exceeds limit (%d bytes)", f.Name, limit)
	}
	if budget != nil {
		budget.remaining -= int64(len(data))
	}
	return data, nil
}
