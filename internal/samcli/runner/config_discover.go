// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DiscoverConfigFiles(paths []string, skipFiles []string) ([]string, error) {
	var files []string

	if len(paths) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
		configsDir := filepath.Join(cwd, "configs")

		info, err := os.Stat(configsDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf(
				"configuration directory '%s' not found; "+
					"provide specific config files or run from a project root with configs/", configsDir)
		}

		files, err = walkYAML(configsDir)
		if err != nil {
			return nil, err
		}
	} else {
		seen := make(map[string]bool)
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, fmt.Errorf("resolving path %q: %w", p, err)
			}

			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("path %q: %w", p, err)
			}

			if info.IsDir() {
				found, err := walkYAML(abs)
				if err != nil {
					return nil, err
				}
				for _, f := range found {
					if !seen[f] {
						seen[f] = true
						files = append(files, f)
					}
				}
			} else if isYAML(abs) {
				if !seen[abs] {
					seen[abs] = true
					files = append(files, abs)
				}
			}
		}
	}

	if len(skipFiles) > 0 {
		skipSet := make(map[string]bool, len(skipFiles))
		for _, s := range skipFiles {
			skipSet[filepath.Base(s)] = true
		}
		filtered := files[:0]
		for _, f := range files {
			if !skipSet[filepath.Base(f)] {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	return files, nil
}

func walkYAML(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if filepath.Base(path) == "examples" && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !isYAML(path) {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_") ||
			strings.HasPrefix(base, "shared_config") ||
			base == "manifest.yaml" {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		files = append(files, abs)
		return nil
	})
	return files, err
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}
