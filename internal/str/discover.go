// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"os"
	"path/filepath"
)

func EnumerateToolManifests(toolsDir string, logger *slog.Logger) []string {
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("tool discovery failed", "dir", toolsDir, "error", err)
		}
		return nil
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(toolsDir, entry.Name(), manifestFileName)
		if fileExists(manifestPath) {
			paths = append(paths, manifestPath)
		}
	}
	return paths
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
