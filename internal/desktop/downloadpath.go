// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func createDownloadFile(dir, filename string) (*os.File, error) {
	name := filepath.Base(filepath.FromSlash(strings.TrimSpace(filename)))
	switch name {
	case "", ".", "..", string(filepath.Separator):
		return nil, fmt.Errorf("unsafe download filename %q", filename)
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}

	for n := 0; n < 1000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create download file: %w", err)
		}
	}
	return nil, fmt.Errorf("no free filename for %q", name)
}
