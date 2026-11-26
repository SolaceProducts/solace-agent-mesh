// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestLoadExampleArtifactConfigFromEnv(t *testing.T) {
	def := DefaultExampleArtifactConfig()

	t.Run("no env vars yields the defaults", func(t *testing.T) {
		cfg := LoadExampleArtifactConfigFromEnv(nil)
		if cfg.MaxBytes != def.MaxBytes {
			t.Errorf("maxBytes = %d, want %d", cfg.MaxBytes, def.MaxBytes)
		}
		if cfg.MaxTotalBytesPerDataset != def.MaxTotalBytesPerDataset {
			t.Errorf("maxTotalBytesPerDataset = %d, want %d",
				cfg.MaxTotalBytesPerDataset, def.MaxTotalBytesPerDataset)
		}
		if len(cfg.AllowedMIMETypes) != 0 {
			t.Errorf("allowedMIMETypes = %v, want empty — no allowlist by default", cfg.AllowedMIMETypes)
		}
	})

	t.Run("valid overrides are applied", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_ARTIFACT_MAX_BYTES", "4096")
		t.Setenv("EVAL_DATASET_ARTIFACT_MAX_TOTAL_BYTES", "8192")
		t.Setenv("EVAL_EXAMPLE_ARTIFACT_ALLOWED_MIME", " Text/Plain , text/csv ,, ")

		cfg := LoadExampleArtifactConfigFromEnv(nil)
		if cfg.MaxBytes != 4096 {
			t.Errorf("maxBytes = %d, want 4096", cfg.MaxBytes)
		}
		if cfg.MaxTotalBytesPerDataset != 8192 {
			t.Errorf("maxTotalBytesPerDataset = %d, want 8192", cfg.MaxTotalBytesPerDataset)
		}
		if want := []string{"text/plain", "text/csv"}; !slices.Equal(cfg.AllowedMIMETypes, want) {
			t.Errorf("allowedMIMETypes = %v, want %v", cfg.AllowedMIMETypes, want)
		}
	})

	t.Run("invalid numbers fall back to the default and warn", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_ARTIFACT_MAX_BYTES", "not-a-number")
		t.Setenv("EVAL_DATASET_ARTIFACT_MAX_TOTAL_BYTES", "0")

		var buf strings.Builder
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		cfg := LoadExampleArtifactConfigFromEnv(logger)

		if cfg.MaxBytes != def.MaxBytes {
			t.Errorf("maxBytes = %d, want the default %d", cfg.MaxBytes, def.MaxBytes)
		}
		if cfg.MaxTotalBytesPerDataset != def.MaxTotalBytesPerDataset {
			t.Errorf("maxTotalBytesPerDataset = %d, want the default %d",
				cfg.MaxTotalBytesPerDataset, def.MaxTotalBytesPerDataset)
		}
		if got := strings.Count(buf.String(), "ignoring invalid env override"); got != 2 {
			t.Errorf("warnings = %d, want one per rejected value:\n%s", got, buf.String())
		}
	})

	t.Run("a separators-only allowlist keeps the default", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_ARTIFACT_ALLOWED_MIME", " , , ")
		if got := LoadExampleArtifactConfigFromEnv(nil).AllowedMIMETypes; len(got) != 0 {
			t.Errorf("allowedMIMETypes = %v, want empty", got)
		}
	})
}
