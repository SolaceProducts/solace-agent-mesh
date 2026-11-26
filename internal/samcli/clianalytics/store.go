// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const consentDatabaseFile = "sam.db"

func ResolveDataDir() (string, error) {
	_, path, settingsErr := settings.Load()
	if settingsErr == nil && path != "" {
		return absDir(filepath.Join(settings.SettingsDir(path), "data"))
	}
	if dir := os.Getenv("SAM_DATA_DIR"); dir != "" {
		return absDir(dir)
	}
	if settingsErr != nil {
		return "", fmt.Errorf("load Agent Mesh settings: %w", settingsErr)
	}
	home, homeErr := settings.ResolveHomeDir()
	if homeErr != nil {
		return "", fmt.Errorf("resolve Agent Mesh home directory: %w", homeErr)
	}
	return absDir(filepath.Join(home, "data"))
}

func absDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory %q: %w", dir, err)
	}
	return abs, nil
}

func databaseURL(dataDir string) string {
	return "sqlite:///" + filepath.ToSlash(filepath.Join(dataDir, consentDatabaseFile))
}
