// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"log/slog"
	"os"
	"strings"
)

type ExampleArtifactConfig struct {
	MaxBytes                int64
	MaxTotalBytesPerDataset int64
	AllowedMIMETypes        []string
}

func DefaultExampleArtifactConfig() ExampleArtifactConfig {
	return ExampleArtifactConfig{
		MaxBytes:                10_000_000,
		MaxTotalBytesPerDataset: 5_000_000_000,
	}
}

func LoadExampleArtifactConfigFromEnv(logger *slog.Logger) ExampleArtifactConfig {
	cfg := DefaultExampleArtifactConfig()
	cfg.MaxBytes = envIntOrDefault("EVAL_EXAMPLE_ARTIFACT_MAX_BYTES", cfg.MaxBytes, logger)
	cfg.MaxTotalBytesPerDataset = envIntOrDefault(
		"EVAL_DATASET_ARTIFACT_MAX_TOTAL_BYTES", cfg.MaxTotalBytesPerDataset, logger)
	cfg.AllowedMIMETypes = envCSVList("EVAL_EXAMPLE_ARTIFACT_ALLOWED_MIME")
	return cfg
}

func envCSVList(name string) []string {
	var out []string
	for _, part := range strings.Split(os.Getenv(name), ",") {
		if trimmed := strings.ToLower(strings.TrimSpace(part)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
