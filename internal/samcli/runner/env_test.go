// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvFile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("absent file returns empty", func(t *testing.T) {
		if got := LoadDotEnvFile(filepath.Join(t.TempDir(), ".env"), logger); got != "" {
			t.Errorf("LoadDotEnvFile() = %q, want empty", got)
		}
	})

	t.Run("loads file and overrides existing env", func(t *testing.T) {
		t.Setenv("SAM_TEST_DOTENV_VAR", "from-process-env")
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("SAM_TEST_DOTENV_VAR=from-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		if got := LoadDotEnvFile(path, logger); got != path {
			t.Fatalf("LoadDotEnvFile() = %q, want %q", got, path)
		}
		if v := os.Getenv("SAM_TEST_DOTENV_VAR"); v != "from-file" {
			t.Errorf("SAM_TEST_DOTENV_VAR = %q, want %q (file must override process env)", v, "from-file")
		}
	})

	t.Run("malformed file returns empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("THIS LINE HAS NO SEPARATOR\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadDotEnvFile(path, logger); got != "" {
			t.Errorf("LoadDotEnvFile() = %q, want empty for malformed file", got)
		}
	})
}
