// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var volumeListTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_VOLUME_DB_URL"),
		fmt.Sprintf("volume_list_%d", os.Getpid()),
		func(u string) error {
			db, err := dbutil.Open(u, nil)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			return dbutil.MigrateSet(context.Background(), db, migrations)
		},
	)
})

func cloneVolumeListDatabase(t *testing.T) string {
	t.Helper()
	tmpl, err := volumeListTemplate()
	if err != nil {
		t.Fatalf("volume list database template: %v", err)
	}
	return tmpl.Clone(t)
}

func processesSharingOneVolumeList(t *testing.T, processes int) []*FilesystemStore {
	t.Helper()
	databaseURL := cloneVolumeListDatabase(t)
	root := t.TempDir()
	stores := make([]*FilesystemStore, processes)
	for i := range processes {
		store, err := OpenFilesystemStore(t.Context(), FilesystemConfig{Root: root, DatabaseURL: databaseURL})
		if err != nil {
			t.Fatalf("open volume store %d: %v", i, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		stores[i] = store
	}
	return stores
}
