// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

func LoadDotEnv(logger *slog.Logger) string {
	return loadNearestDotEnv(logger, godotenv.Overload)
}

func LoadDotEnvKeepingEnv(logger *slog.Logger) string {
	return loadNearestDotEnv(logger, godotenv.Load)
}

func loadNearestDotEnv(logger *slog.Logger, load func(...string) error) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return loadDotEnvFile(candidate, logger, load)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func LoadDotEnvFile(path string, logger *slog.Logger) string {
	return loadDotEnvFile(path, logger, godotenv.Overload)
}

func loadDotEnvFile(path string, logger *slog.Logger, load func(...string) error) string {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("failed to stat .env", "path", path, "error", err)
		}
		return ""
	}
	if err := load(path); err != nil {
		logger.Warn("failed to load .env", "path", path, "error", err)
		return ""
	}
	logger.Debug("loaded .env", "path", path)
	return path
}
