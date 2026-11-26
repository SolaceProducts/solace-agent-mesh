// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestResolveVolumeServiceConfig(t *testing.T) {
	t.Parallel()

	appWithSvc := samapp.AppEntry{Name: "a", AppConfig: map[string]any{
		"volume_service": map[string]any{"type": "memory"},
	}}
	appNoSvc := samapp.AppEntry{Name: "b", AppConfig: map[string]any{}}

	tests := []struct {
		name           string
		rootCfg        map[string]any
		apps           []samapp.AppEntry
		volumesEnabled bool
		want           map[string]any
	}{
		{
			name: "first app volume_service wins (ungated)",
			rootCfg: map[string]any{
				"volume_service": map[string]any{"type": "filesystem", "root": "/ignored"},
				"volume_root":    "/also-ignored",
			},
			apps:           []samapp.AppEntry{appWithSvc},
			volumesEnabled: false,
			want:           map[string]any{"type": "memory"},
		},
		{
			name:           "top-level volume_service honored without the flag",
			rootCfg:        map[string]any{"volume_service": map[string]any{"type": "filesystem", "root": "/app/data/volumes"}},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: false,
			want:           map[string]any{"type": "filesystem", "root": "/app/data/volumes"},
		},
		{
			name:           "volume_root shorthand applied when volumes enabled",
			rootCfg:        map[string]any{"volume_root": "/app/data/volumes"},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           map[string]any{"type": "filesystem", "root": "/app/data/volumes"},
		},
		{
			name:           "volume_root shorthand carries the shared volume list database",
			rootCfg:        map[string]any{"volume_root": "/app/data/volumes", "volume_database_url": "postgres://db/orchestrator"},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "postgres://db/orchestrator"},
		},
		{
			name:           "an empty volume list database leaves the shorthand on the single-host file",
			rootCfg:        map[string]any{"volume_root": "/app/data/volumes", "volume_database_url": ""},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           map[string]any{"type": "filesystem", "root": "/app/data/volumes"},
		},
		{
			name:    "an app's volume_service block without a database takes the AWE's volume list database",
			rootCfg: map[string]any{"volume_database_url": "postgres://db/orchestrator"},
			apps: []samapp.AppEntry{{Name: "a", AppConfig: map[string]any{
				"volume_service": map[string]any{"type": "filesystem", "root": "/app/data/volumes"},
			}}},
			want: map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "postgres://db/orchestrator"},
		},
		{
			name: "a top-level volume_service block without a database takes the AWE's volume list database",
			rootCfg: map[string]any{
				"volume_service":      map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "  "},
				"volume_database_url": "postgres://db/orchestrator",
			},
			apps: []samapp.AppEntry{appNoSvc},
			want: map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "postgres://db/orchestrator"},
		},
		{
			name:    "a volume_service block's own database wins over the AWE's volume list database",
			rootCfg: map[string]any{"volume_database_url": "postgres://db/orchestrator"},
			apps: []samapp.AppEntry{{Name: "a", AppConfig: map[string]any{
				"volume_service": map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "postgres://db/volumes"},
			}}},
			want: map[string]any{"type": "filesystem", "root": "/app/data/volumes", "database_url": "postgres://db/volumes"},
		},
		{
			name:           "volume_root shorthand ignored when volumes disabled",
			rootCfg:        map[string]any{"volume_root": "/app/data/volumes"},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: false,
			want:           nil,
		},
		{
			name:           "volume_service block beats volume_root key",
			rootCfg:        map[string]any{"volume_service": map[string]any{"type": "memory"}, "volume_root": "/x"},
			apps:           nil,
			volumesEnabled: true,
			want:           map[string]any{"type": "memory"},
		},
		{
			name:           "empty volume_root is ignored",
			rootCfg:        map[string]any{"volume_root": ""},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           nil,
		},
		{
			name:           "nothing configured",
			rootCfg:        map[string]any{},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           nil,
		},
		{
			name: "app block with enabled:false is authoritative (wins over a root block)",
			rootCfg: map[string]any{
				"volume_service": map[string]any{"type": "filesystem", "root": "/from-root"},
			},
			apps:           []samapp.AppEntry{{Name: "a", AppConfig: map[string]any{"volume_service": map[string]any{"enabled": false, "type": "memory"}}}},
			volumesEnabled: false,
			want:           nil,
		},
		{
			name:           "top-level block with enabled:false yields nil",
			rootCfg:        map[string]any{"volume_service": map[string]any{"enabled": false, "type": "memory"}},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: false,
			want:           nil,
		},
		{
			name:           "enabled:false suppresses the volume_root shorthand even with the volumes flag on",
			rootCfg:        map[string]any{"volume_service": map[string]any{"enabled": false}, "volume_root": "/app/data/volumes"},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: true,
			want:           nil,
		},
		{
			name:           "block with enabled:true is honored",
			rootCfg:        map[string]any{"volume_service": map[string]any{"enabled": true, "type": "memory"}},
			apps:           []samapp.AppEntry{appNoSvc},
			volumesEnabled: false,
			want:           map[string]any{"enabled": true, "type": "memory"},
		},
		{
			name:    "first app's disabled block is authoritative over a later valid app",
			rootCfg: map[string]any{},
			apps: []samapp.AppEntry{
				{Name: "a", AppConfig: map[string]any{"volume_service": map[string]any{"enabled": false}}},
				appWithSvc,
			},
			volumesEnabled: false,
			want:           nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configAsLoaded := fmt.Sprint(tc.rootCfg, tc.apps)
			got := resolveVolumeServiceConfig(tc.rootCfg, tc.apps, tc.volumesEnabled)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveVolumeServiceConfig = %v, want %v", got, tc.want)
			}
			if after := fmt.Sprint(tc.rootCfg, tc.apps); after != configAsLoaded {
				t.Errorf("resolving rewrote the loaded config:\nbefore %s\nafter  %s", configAsLoaded, after)
			}
		})
	}
}
