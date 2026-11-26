// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"path/filepath"
	"testing"
)

func TestResolveEmbeddedVolumeRoot(t *testing.T) {
	tests := []struct {
		name       string
		volumeRoot string
		dataDir    string
	}{
		{
			name:       "SAM_VOLUME_ROOT wins verbatim",
			volumeRoot: "/explicit/vol",
			dataDir:    "/data",
		},
		{
			name:    "derives from data dir when SAM_VOLUME_ROOT unset",
			dataDir: "/data",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_VOLUME_ROOT", tc.volumeRoot)
			t.Setenv("SAM_DATA_DIR", tc.dataDir)

			want := tc.volumeRoot
			if want == "" {
				want = filepath.Join(tc.dataDir, "volumes")
			}
			if got := resolveEmbeddedVolumeRoot(); got != want {
				t.Errorf("resolveEmbeddedVolumeRoot() = %q, want %q", got, want)
			}
		})
	}
}

func TestEmbeddedVolumeRootDefault(t *testing.T) {
	t.Run("returns empty when SAM_VOLUME_ROOT already set", func(t *testing.T) {
		t.Setenv("SAM_VOLUME_ROOT", "/explicit/vol")
		t.Setenv("SAM_DATA_DIR", "/data")
		if got := embeddedVolumeRootDefault(); got != "" {
			t.Errorf("embeddedVolumeRootDefault() = %q, want \"\" (operator value must not be overridden)", got)
		}
	})

	t.Run("derives a host path when SAM_VOLUME_ROOT unset", func(t *testing.T) {
		dataDir := "/data"
		t.Setenv("SAM_VOLUME_ROOT", "")
		t.Setenv("SAM_DATA_DIR", dataDir)
		if got, want := embeddedVolumeRootDefault(), filepath.Join(dataDir, "volumes"); got != want {
			t.Errorf("embeddedVolumeRootDefault() = %q, want %q", got, want)
		}
	})
}

func TestShouldDefaultEmbeddedDevMode(t *testing.T) {
	t.Run("both unset → default the dev broker on", func(t *testing.T) {
		t.Setenv("SOLACE_DEV_MODE", "")
		t.Setenv("SOLACE_BROKER_URL", "")
		if !shouldDefaultEmbeddedDevMode() {
			t.Error("shouldDefaultEmbeddedDevMode() = false, want true when no broker is configured")
		}
	})

	t.Run("explicit SOLACE_DEV_MODE is honored", func(t *testing.T) {
		t.Setenv("SOLACE_DEV_MODE", "false")
		t.Setenv("SOLACE_BROKER_URL", "")
		if shouldDefaultEmbeddedDevMode() {
			t.Error("shouldDefaultEmbeddedDevMode() = true, want false when SOLACE_DEV_MODE is set")
		}
	})

	t.Run("explicit SOLACE_BROKER_URL is honored", func(t *testing.T) {
		t.Setenv("SOLACE_DEV_MODE", "")
		t.Setenv("SOLACE_BROKER_URL", "tcps://broker.example:55443")
		if shouldDefaultEmbeddedDevMode() {
			t.Error("shouldDefaultEmbeddedDevMode() = true, want false when SOLACE_BROKER_URL is set")
		}
	})
}
