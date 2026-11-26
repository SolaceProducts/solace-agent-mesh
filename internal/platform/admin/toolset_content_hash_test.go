// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestToolsetService_Upload_PersistsContentHash(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "hash_pkg")

	zipBytes := buildValidZIP(t, map[string][]byte{"tool.py": []byte("x = 1")})
	sum := sha256.Sum256(zipBytes)
	want := hex.EncodeToString(sum[:])

	if err := f.svc.Upload(ctx, pkg.ID, "p.zip", zipBytes, "alice"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got, err := f.repo.GetByID(ctx, pkg.ID)
	if err != nil || got == nil {
		t.Fatalf("re-read package: pkg=%v err=%v", got, err)
	}
	if got.ContentHash == nil || *got.ContentHash != want {
		t.Errorf("stored content_hash = %v, want %q", got.ContentHash, want)
	}

	resp, err := f.svc.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ContentHash != want {
		t.Errorf("ToolsetResponse.ContentHash = %q, want %q", resp.ContentHash, want)
	}
}

func TestToolsetService_GetByID_NoHashIsEmptyString(t *testing.T) {
	t.Parallel()
	f := newOutboxFixture(t)
	ctx := t.Context()

	pkg := f.createPkg(ctx, t, "no_hash_pkg")

	resp, err := f.svc.GetByID(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ContentHash != "" {
		t.Errorf("ContentHash for never-uploaded toolset = %q, want empty", resp.ContentHash)
	}
}
