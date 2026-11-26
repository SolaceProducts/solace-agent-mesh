// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxConnectorSpecFileBytes = 10 * 1024 * 1024

type ConnectorSpecFile struct {
	Filename string
	Data     []byte
}

func ResolveConnectorSpecFile(values map[string]any, sourceFile string) (*ConnectorSpecFile, error) {
	if values == nil {
		return nil, nil
	}
	raw, ok := values["specification_file"]
	if !ok {
		return nil, nil
	}
	delete(values, "specification_file")

	relPath, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("specification_file must be a string, got %T", raw)
	}
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return nil, fmt.Errorf("specification_file must not be empty")
	}

	full := relPath
	if !filepath.IsAbs(full) {
		full = filepath.Join(filepath.Dir(sourceFile), relPath)
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return nil, fmt.Errorf("resolve specification_file %q: %w", relPath, err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.Size() > maxConnectorSpecFileBytes {
		return nil, fmt.Errorf("specification_file %q is %d bytes, exceeds %d-byte cap",
			relPath, info.Size(), maxConnectorSpecFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read specification_file %q: %w", relPath, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("specification_file %q is empty", relPath)
	}
	return &ConnectorSpecFile{Filename: filepath.Base(abs), Data: data}, nil
}
