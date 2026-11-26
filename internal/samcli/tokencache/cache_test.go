// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokencache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
)

func newTempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Root: filepath.Join(dir, "auth")}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	s := newTempStore(t)

	c := Cached{
		PlatformURL:    "https://x.example.com",
		Issuer:         "https://idp.example.com",
		AccessToken:    "ax",
		SAMAccessToken: "sam",
		RefreshToken:   "rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour).UTC().Round(time.Second),
		ObtainedAt:     time.Now().UTC().Round(time.Second),
	}
	if err := s.Save("dev", c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Load("dev")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil; expected the saved value")
	}
	if got.SAMAccessToken != "sam" || got.RefreshToken != "rt" || got.PlatformURL != c.PlatformURL {
		t.Errorf("Load = %+v", got)
	}
	if !got.ExpiresAt.Equal(c.ExpiresAt) {
		t.Errorf("ExpiresAt = %v; want %v", got.ExpiresAt, c.ExpiresAt)
	}
}

func TestLoad_CacheMissReturnsNilNoError(t *testing.T) {
	s := newTempStore(t)
	got, err := s.Load("never-saved")
	if err != nil {
		t.Errorf("err = %v; want nil for cache miss", err)
	}
	if got != nil {
		t.Errorf("got = %+v; want nil for cache miss", got)
	}
}

func TestSave_FileMode0600AndDir0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are POSIX-only")
	}
	s := newTempStore(t)
	if err := s.Save("dev", Cached{SAMAccessToken: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rootInfo, err := os.Stat(s.Root)
	if err != nil {
		t.Fatalf("stat root: %v", err)
	}
	if got := rootInfo.Mode() & 0o777; got != 0o700 {
		t.Errorf("root mode = %#o; want 0700", got)
	}

	fileInfo, err := os.Stat(s.Path("dev"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fileInfo.Mode() & 0o777; got != 0o600 {
		t.Errorf("file mode = %#o; want 0600", got)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	s := newTempStore(t)
	if err := s.Delete("ghost"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
	if err := s.Save("dev", Cached{SAMAccessToken: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete("dev"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Load("dev")
	if got != nil {
		t.Errorf("post-delete Load = %+v; want nil", got)
	}
}

func TestList_OnlyJSONFiles(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("alpha", Cached{SAMAccessToken: "a"}); err != nil {
		t.Fatalf("Save alpha: %v", err)
	}
	if err := s.Save("beta", Cached{SAMAccessToken: "b"}); err != nil {
		t.Fatalf("Save beta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".DS_Store"), []byte("..."), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}

	names, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]bool{"alpha": true, "beta": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected entry %q", n)
		}
		delete(want, n)
	}
	if len(want) != 0 {
		t.Errorf("missing entries: %v", want)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dev", "dev"},
		{"Prod_01", "prod_01"},
		{"weird name with spaces", "weird-name-with-spaces"},
		{"../escape", "..-escape"},
		{"", "default"},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in); got != c.want {
			t.Errorf("sanitizeName(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

type fakeRefreshClient struct {
	called            bool
	presentedSAMToken string
	tokens            oauth.Tokens
	err               error
}

func (f *fakeRefreshClient) Refresh(_ context.Context, _ oauth.Config, _, samAccessToken string) (oauth.Tokens, error) {
	f.called = true
	f.presentedSAMToken = samAccessToken
	return f.tokens, f.err
}

func TestResolve_FastPath_TokenStillValid(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "still-good",
		RefreshToken:   "rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{}
	r := NewResolver(s, rc)
	tok, err := r.ResolveSAMToken(context.Background(), target.Target{Name: "dev", URL: "https://x.example.com"})
	if err != nil {
		t.Fatalf("ResolveSAMToken: %v", err)
	}
	if tok != "still-good" {
		t.Errorf("token = %q; want still-good", tok)
	}
	if rc.called {
		t.Error("Refresh was called even though the token is far from expiry")
	}
}

func TestResolve_RefreshNearExpiry(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "old-sam",
		RefreshToken:   "rt",
		ExpiresAt:      time.Now().Add(1 * time.Minute),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{tokens: oauth.Tokens{
		AccessToken:    "new-access",
		SAMAccessToken: "new-sam",
		RefreshToken:   "new-rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		ObtainedAt:     time.Now(),
	}}
	r := NewResolver(s, rc)
	tok, err := r.ResolveSAMToken(context.Background(), target.Target{Name: "dev", URL: "https://x.example.com"})
	if err != nil {
		t.Fatalf("ResolveSAMToken: %v", err)
	}
	if tok != "new-sam" {
		t.Errorf("token = %q; want new-sam", tok)
	}
	if !rc.called {
		t.Error("Refresh was not called near expiry")
	}
	if rc.presentedSAMToken != "old-sam" {
		t.Errorf("Refresh presented SAM token %q; want the cached old-sam so the platform can apply its idle timeout", rc.presentedSAMToken)
	}
	cached, _ := s.Load("dev")
	if cached == nil || cached.SAMAccessToken != "new-sam" || cached.RefreshToken != "new-rt" {
		t.Errorf("post-refresh cache = %+v", cached)
	}
}

func TestResolve_RefreshDeniedClearsCache(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "sam",
		RefreshToken:   "expired-rt",
		ExpiresAt:      time.Now().Add(30 * time.Second),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{err: oauth.ErrRefreshDenied}
	r := NewResolver(s, rc)

	_, err := r.ResolveSAMToken(context.Background(), target.Target{Name: "dev", URL: "https://x.example.com"})
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want target.ErrNotLoggedIn", err)
	}
	got, _ := s.Load("dev")
	if got != nil {
		t.Errorf("cache survived a denied refresh: %+v", got)
	}
}

func TestResolve_NoCache_ReturnsErrNotLoggedIn(t *testing.T) {
	s := newTempStore(t)
	r := NewResolver(s, nil)
	_, err := r.ResolveSAMToken(context.Background(), target.Target{Name: "dev", URL: "https://x.example.com"})
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want target.ErrNotLoggedIn", err)
	}
}

func TestForceRefresh_RotatesTokenIgnoringExpiry(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "old-sam",
		RefreshToken:   "rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{tokens: oauth.Tokens{
		AccessToken:    "new-access",
		SAMAccessToken: "new-sam",
		RefreshToken:   "new-rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		ObtainedAt:     time.Now(),
	}}
	r := NewResolver(s, rc)
	tok, err := r.ForceRefresh(context.Background(), target.Target{Name: "dev", URL: "https://x.example.com"})
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if tok != "new-sam" {
		t.Errorf("token = %q; want new-sam", tok)
	}
	if !rc.called {
		t.Error("Refresh was not called by ForceRefresh")
	}
	cached, _ := s.Load("dev")
	if cached == nil || cached.SAMAccessToken != "new-sam" || cached.RefreshToken != "new-rt" {
		t.Errorf("post-refresh cache = %+v", cached)
	}
}

func TestForceRefresh_PreservesRefreshTokenWhenIdpOmitsRotation(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "old-sam",
		RefreshToken:   "keeper-rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{tokens: oauth.Tokens{
		AccessToken:    "new-access",
		SAMAccessToken: "new-sam",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		ObtainedAt:     time.Now(),
	}}
	r := NewResolver(s, rc)
	if _, err := r.ForceRefresh(context.Background(), target.Target{Name: "dev"}); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	cached, _ := s.Load("dev")
	if cached == nil || cached.RefreshToken != "keeper-rt" {
		t.Errorf("expected refresh token preserved; got %+v", cached)
	}
}

func TestForceRefresh_NoCacheReturnsErrNotLoggedIn(t *testing.T) {
	s := newTempStore(t)
	r := NewResolver(s, &fakeRefreshClient{})
	_, err := r.ForceRefresh(context.Background(), target.Target{Name: "dev"})
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want target.ErrNotLoggedIn", err)
	}
}

func TestForceRefresh_DeniedClearsCache(t *testing.T) {
	s := newTempStore(t)
	if err := s.Save("dev", Cached{
		PlatformURL:    "https://x.example.com",
		SAMAccessToken: "sam",
		RefreshToken:   "expired-rt",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc := &fakeRefreshClient{err: oauth.ErrRefreshDenied}
	r := NewResolver(s, rc)
	_, err := r.ForceRefresh(context.Background(), target.Target{Name: "dev"})
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want target.ErrNotLoggedIn", err)
	}
	got, _ := s.Load("dev")
	if got != nil {
		t.Errorf("cache survived a denied refresh: %+v", got)
	}
}
