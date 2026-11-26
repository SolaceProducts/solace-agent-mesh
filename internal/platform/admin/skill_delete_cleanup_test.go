// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

type failingDeleteStore struct{ ResourceStore }

func (failingDeleteStore) Delete(context.Context, string) error {
	return errors.New("object store unavailable")
}

func TestSkillService_DeleteWithCleanup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		storeFails  bool
		wantZIPGone bool
	}{
		{name: "removes the record, runtime ZIP and directory", wantZIPGone: true},
		{name: "a storage failure leaves the delete done", storeFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSkillOutboxFixture(t)
			memory := objectstore.NewStore(objectstore.NewMemoryClient())
			var store ResourceStore = memory
			if tc.storeFails {
				store = failingDeleteStore{memory}
			}
			f.svc.WithStorage(store, "test-ns")
			skillsDir := t.TempDir()
			f.svc.WithSkillsDir(skillsDir)
			ctx := t.Context()

			sk := f.seedReady(ctx, t, "cleanup_skill", skillSchemaSecretRequired)
			zipData := buildSkillZIPWithContent(t, "---\nname: cleanup_skill\ndescription: test\n---\n")
			if err := f.svc.UploadZIP(ctx, sk.ID, sk.Name, zipData, "alice"); err != nil {
				t.Fatalf("UploadZIP: %v", err)
			}
			unpacked := filepath.Join(skillsDir, sk.Name)
			if err := os.MkdirAll(unpacked, 0o700); err != nil {
				t.Fatalf("seed unpacked dir: %v", err)
			}

			if err := f.svc.DeleteWithCleanup(ctx, sk.ID); err != nil {
				t.Fatalf("DeleteWithCleanup: %v", err)
			}

			if _, err := f.svc.GetByID(ctx, sk.ID); !errors.As(err, new(*NotFoundError)) {
				t.Errorf("GetByID after delete = %v, want NotFoundError", err)
			}
			_, getErr := memory.Get(ctx, skillRuntimeKey("test-ns", sk.Name))
			if zipGone := getErr != nil; zipGone != tc.wantZIPGone {
				t.Errorf("runtime ZIP gone = %v (Get error %v), want %v", zipGone, getErr, tc.wantZIPGone)
			}
			if _, err := os.Stat(unpacked); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("unpacked dir stat = %v, want it removed", err)
			}
		})
	}
}
