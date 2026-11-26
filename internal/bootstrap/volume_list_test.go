// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type listInASharedDatabase struct {
	volume.Service
}

func (listInASharedDatabase) ListLocation() string       { return "db.internal:5432/orchestrator" }
func (listInASharedDatabase) ListIsSingleHostFile() bool { return false }

func TestTheVolumeListIsReportedAtStartOnlyWhileVolumesAreOnAndWarnedAboutOnlyAsASingleHostFile(t *testing.T) {
	t.Setenv("SAM_FEATURE_VOLUMES", "")
	root := t.TempDir()
	singleHostFile, err := volume.NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = singleHostFile.Close() })
	volumesFlag := func(on bool) *features.Registry {
		flags := features.NewRegistry()
		flags.Register(features.Definition{Key: volumesFeatureKey, Default: on})
		return flags
	}

	for _, tc := range []struct {
		name      string
		volumes   volume.Service
		flags     *features.Registry
		wantNamed string
		wantWarn  bool
	}{
		{name: "list file under the volume root", volumes: singleHostFile, flags: volumesFlag(true), wantNamed: filepath.Join(root, "volumes.db"), wantWarn: true},
		{name: "list in a shared database", volumes: listInASharedDatabase{}, flags: volumesFlag(true), wantNamed: "db.internal:5432/orchestrator"},
		{name: "list file under the volume root while volumes are off", volumes: singleHostFile, flags: volumesFlag(false)},
		{name: "list file under the volume root without a flag registry", volumes: singleHostFile},
		{name: "in-memory list", volumes: volume.NewMemoryStore(), flags: volumesFlag(true)},
		{name: "no volume service", flags: volumesFlag(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			ReportVolumeList(tc.volumes, tc.flags, slog.New(slog.NewJSONHandler(&logs, nil)))

			named, warned := "", false
			for line := range strings.Lines(logs.String()) {
				var record struct {
					Level      string `json:"level"`
					Msg        string `json:"msg"`
					VolumeList string `json:"volumeList"`
				}
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("log line %q: %v", line, err)
				}
				if record.Level == "INFO" {
					named = record.VolumeList
				}
				if record.Level == "WARN" && strings.Contains(record.Msg, "volume_database_url") {
					warned = true
				}
			}
			if named != tc.wantNamed || warned != tc.wantWarn {
				t.Fatalf("named %q, warned=%v; want named %q, warned=%v; logs: %s", named, warned, tc.wantNamed, tc.wantWarn, logs.String())
			}
		})
	}
}
