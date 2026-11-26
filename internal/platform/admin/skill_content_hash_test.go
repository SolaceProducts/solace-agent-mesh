// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

func TestSkillService_UploadZIP_PersistsContentHash(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	f.svc.WithStorage(objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns")
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "hash_skill", skillSchemaSecretRequired)

	skillMD := "---\nname: hash_skill\ndescription: test\n---\n\nContent here.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)
	sum := sha256.Sum256(zipData)
	want := hex.EncodeToString(sum[:])

	if err := f.svc.UploadZIP(ctx, sk.ID, sk.Name, zipData, "alice"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}

	got, err := f.repo.GetByName(ctx, sk.Name)
	if err != nil || got == nil {
		t.Fatalf("re-read skill: skill=%v err=%v", got, err)
	}
	if got.ContentHash == nil || *got.ContentHash != want {
		t.Errorf("stored content_hash = %v, want %q", got.ContentHash, want)
	}

	resp, err := f.svc.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ContentHash != want {
		t.Errorf("SkillDetailResponse.ContentHash = %q, want %q", resp.ContentHash, want)
	}
}

func TestSkillService_GetByID_NoHashIsEmptyString(t *testing.T) {
	t.Parallel()
	f := newSkillOutboxFixture(t)
	ctx := t.Context()

	sk := f.seedReady(ctx, t, "no_hash_skill", skillSchemaSecretRequired)

	resp, err := f.svc.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ContentHash != "" {
		t.Errorf("ContentHash for never-uploaded skill = %q, want empty", resp.ContentHash)
	}
}
