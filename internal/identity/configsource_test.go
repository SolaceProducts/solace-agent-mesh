// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type configRepoStub struct {
	row *store.PlatformIdentityConfig
	err error
}

func (s configRepoStub) Get(_ context.Context) (*store.PlatformIdentityConfig, error) {
	return s.row, s.err
}

func repoConfigSourceFromStub(stub configRepoStub) *RepoConfigSource {
	return &RepoConfigSource{repo: stub}
}

func TestRepoConfigSource_Get_Configured(t *testing.T) {
	t.Parallel()
	want := "pkg-123"
	src := repoConfigSourceFromStub(configRepoStub{row: &store.PlatformIdentityConfig{
		ToolsetID: &want,
		LookupKey: "email",
	}})
	id, key, configured, err := src.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !configured {
		t.Errorf("configured = false, want true")
	}
	if id != want || key != "email" {
		t.Errorf("Get returned (%q, %q), want (%q, %q)", id, key, want, "email")
	}
}

func TestRepoConfigSource_Get_NilRow(t *testing.T) {
	t.Parallel()
	src := repoConfigSourceFromStub(configRepoStub{row: nil})
	id, key, configured, err := src.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if configured || id != "" || key != "" {
		t.Errorf("expected unconfigured zero-result; got (%q, %q, %v)", id, key, configured)
	}
}

func TestRepoConfigSource_Get_NullToolsetID(t *testing.T) {
	t.Parallel()
	src := repoConfigSourceFromStub(configRepoStub{row: &store.PlatformIdentityConfig{
		ToolsetID: nil,
		LookupKey: "email",
	}})
	_, _, configured, err := src.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if configured {
		t.Errorf("configured = true with nil ToolsetID; want false")
	}
}

func TestRepoConfigSource_Get_EmptyToolsetID(t *testing.T) {
	t.Parallel()
	empty := ""
	src := repoConfigSourceFromStub(configRepoStub{row: &store.PlatformIdentityConfig{
		ToolsetID: &empty,
		LookupKey: "email",
	}})
	_, _, configured, err := src.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if configured {
		t.Errorf("configured = true with empty ToolsetID; want false")
	}
}

func TestRepoConfigSource_Get_RepoError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("db down")
	src := repoConfigSourceFromStub(configRepoStub{err: wantErr})
	_, _, configured, err := src.Get(t.Context())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want wrapped %v", err, wantErr)
	}
	if configured {
		t.Errorf("configured = true on error; want false")
	}
}
