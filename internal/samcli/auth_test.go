// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

type fakeAuthCacheStore struct {
	entries map[string]string
	listErr error
	loadErr error
}

func (f *fakeAuthCacheStore) Load(name string) (*tokencache.Cached, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	url, ok := f.entries[name]
	if !ok {
		return nil, nil
	}
	return &tokencache.Cached{PlatformURL: url}, nil
}

func (f *fakeAuthCacheStore) List() ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]string, 0, len(f.entries))
	for k := range f.entries {
		out = append(out, k)
	}
	return out, nil
}

func TestParseAuthTarget(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		urlOverride    string
		targetOverride string
		store          *fakeAuthCacheStore
		wantTargetName string
		wantTargetURL  string
		wantErr        string
	}{
		{
			name:    "empty input, empty cache",
			store:   &fakeAuthCacheStore{},
			wantErr: "specify a hostname",
		},
		{
			name:           "empty input, single cache entry refreshes it",
			store:          &fakeAuthCacheStore{entries: map[string]string{"dev": "https://platform.dev.example.com"}},
			wantTargetName: "dev",
			wantTargetURL:  "https://platform.dev.example.com",
		},
		{
			name: "empty input, multiple cache entries errors with list",
			store: &fakeAuthCacheStore{entries: map[string]string{
				"dev":  "https://dev.example.com",
				"prod": "https://prod.example.com",
			}},
			wantErr: "multiple cached targets",
		},
		{
			name:           "empty input + --url alone uses URL host as cache name",
			urlOverride:    "https://platform.dev.example.com",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "https://platform.dev.example.com",
			wantTargetName: "platform.dev.example.com",
		},
		{
			name:    "http positional rejected (HTTPS-only)",
			input:   "http://insecure.example.com",
			store:   &fakeAuthCacheStore{},
			wantErr: "HTTPS",
		},
		{
			name:        "http override rejected (HTTPS-only)",
			input:       "dev",
			urlOverride: "http://insecure.example.com",
			store:       &fakeAuthCacheStore{},
			wantErr:     "HTTPS",
		},
		{
			name:           "https URL positional, name from host",
			input:          "https://platform.dev.example.com",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "https://platform.dev.example.com",
			wantTargetName: "platform.dev.example.com",
		},
		{
			name:           "https URL positional, --target override wins",
			input:          "https://platform.dev.example.com",
			targetOverride: "alt",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "https://platform.dev.example.com",
			wantTargetName: "alt",
		},
		{
			name:           "bare positional matching cache name refreshes it",
			input:          "dev",
			store:          &fakeAuthCacheStore{entries: map[string]string{"dev": "https://dev.example.com"}},
			wantTargetName: "dev",
			wantTargetURL:  "https://dev.example.com",
		},
		{
			name:           "hostname positional with dot derives https URL",
			input:          "platform.dev.example.com",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "https://platform.dev.example.com",
			wantTargetName: "platform.dev.example.com",
		},
		{
			name:           "hostname positional with explicit port preserved",
			input:          "platform.dev.example.com:8443",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "https://platform.dev.example.com:8443",
			wantTargetName: "platform.dev.example.com:8443",
		},
		{
			name:           "first-time named login: bare name + --url",
			input:          "dev",
			urlOverride:    "https://platform.dev.example.com",
			store:          &fakeAuthCacheStore{},
			wantTargetName: "dev",
			wantTargetURL:  "https://platform.dev.example.com",
		},
		{
			name:    "bare name not in cache and no --url errors",
			input:   "nope",
			store:   &fakeAuthCacheStore{},
			wantErr: "no cache entry",
		},
		{
			name:           "hostname with --target override",
			input:          "platform.dev.example.com",
			targetOverride: "dev",
			store:          &fakeAuthCacheStore{},
			wantTargetName: "dev",
			wantTargetURL:  "https://platform.dev.example.com",
		},
		{
			name:           "cache-name refresh ignores --target override",
			input:          "dev",
			targetOverride: "ignored",
			store:          &fakeAuthCacheStore{entries: map[string]string{"dev": "https://dev.example.com"}},
			wantTargetName: "dev",
			wantTargetURL:  "https://dev.example.com",
		},
		{
			name:           "uppercase HTTPS:// is accepted (case-insensitive)",
			input:          "HTTPS://platform.dev.example.com",
			store:          &fakeAuthCacheStore{},
			wantTargetURL:  "HTTPS://platform.dev.example.com",
			wantTargetName: "platform.dev.example.com",
		},
		{
			name:    "uppercase HTTP:// is rejected (case-insensitive)",
			input:   "HTTP://insecure.example.com",
			store:   &fakeAuthCacheStore{},
			wantErr: "HTTPS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg, err := parseAuthTarget(tc.input, tc.store, tc.urlOverride, tc.targetOverride)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got Target=%+v", tc.wantErr, tg)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tg.Name != tc.wantTargetName {
				t.Errorf("Name = %q; want %q", tg.Name, tc.wantTargetName)
			}
			if tg.URL != tc.wantTargetURL {
				t.Errorf("URL = %q; want %q", tg.URL, tc.wantTargetURL)
			}
		})
	}
}

func TestParseAuthTarget_ListError(t *testing.T) {
	store := &fakeAuthCacheStore{listErr: errors.New("disk full")}
	_, err := parseAuthTarget("", store, "", "")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsInteractive_NotTTYInTests(t *testing.T) {
	if isInteractive() {
		t.Errorf("isInteractive() = true under go test; want false")
	}
}

func TestIsInteractive_CIDisablesPromptEvenOnATTY(t *testing.T) {
	tty, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tty.Close() })
	origStdin, origStderr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = tty, tty
	t.Cleanup(func() { os.Stdin, os.Stderr = origStdin, origStderr })

	t.Setenv("CI", "")
	if !isInteractive() {
		t.Fatal("a terminal without CI should be interactive")
	}
	t.Setenv("CI", "true")
	if isInteractive() {
		t.Error("CI=true should disable the prompt even on a terminal")
	}
}

type fakeOAuthResolver struct {
	token string
	err   error
}

func (f fakeOAuthResolver) ResolveSAMToken(_ context.Context, _ target.Target) (string, error) {
	return f.token, f.err
}

type stubResolver struct {
	calls int
}

func (s *stubResolver) ResolveSAMToken(_ context.Context, _ target.Target) (string, error) {
	s.calls++
	if s.calls == 1 {
		return "", target.ErrNotLoggedIn
	}
	return "fresh-token", nil
}

func withInteractive(t *testing.T, v bool) {
	t.Helper()
	orig := isInteractive
	isInteractive = func() bool { return v }
	t.Cleanup(func() { isInteractive = orig })
}

func withInteractiveLogin(t *testing.T, handler func(ctx context.Context, tg target.Target, w io.Writer, in io.Reader) error) {
	t.Helper()
	orig := runInteractiveLogin
	runInteractiveLogin = handler
	t.Cleanup(func() { runInteractiveLogin = orig })
}

func TestPreflightAuth_AlreadyAuthenticated(t *testing.T) {
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	if err := preflightAuth(t.Context(), tg, fakeOAuthResolver{token: "tok"}, io.Discard, strings.NewReader(""), false); err != nil {
		t.Fatalf("preflightAuth: %v", err)
	}
}

func TestPreflightAuth_NoInteractiveFailsFast(t *testing.T) {
	withInteractive(t, true)
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	err := preflightAuth(t.Context(), tg, fakeOAuthResolver{err: target.ErrNotLoggedIn}, io.Discard, strings.NewReader(""), true)
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want ErrNotLoggedIn", err)
	}
}

func TestPreflightAuth_NonTTYFailsFast(t *testing.T) {
	withInteractive(t, false)
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	err := preflightAuth(t.Context(), tg, fakeOAuthResolver{err: target.ErrNotLoggedIn}, io.Discard, strings.NewReader("y\n"), false)
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want ErrNotLoggedIn", err)
	}
}

func TestPreflightAuth_TTYPromptYes(t *testing.T) {
	withInteractive(t, true)
	loginCalls := 0
	withInteractiveLogin(t, func(ctx context.Context, tg target.Target, w io.Writer, in io.Reader) error {
		loginCalls++
		return nil
	})
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	resolver := &stubResolver{}
	err := preflightAuth(t.Context(), tg, resolver, io.Discard, strings.NewReader("y\n"), false)
	if err != nil {
		t.Fatalf("preflightAuth: %v", err)
	}
	if loginCalls != 1 {
		t.Errorf("inline login fired %d times; want 1", loginCalls)
	}
	if resolver.calls != 2 {
		t.Errorf("resolver called %d times; want 2 (preflight + retry)", resolver.calls)
	}
}

func TestPreflightAuth_TTYPromptNo(t *testing.T) {
	withInteractive(t, true)
	loginCalls := 0
	withInteractiveLogin(t, func(ctx context.Context, tg target.Target, w io.Writer, in io.Reader) error {
		loginCalls++
		return defaultRunInteractiveLogin(ctx, tg, w, in)
	})
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	resolver := fakeOAuthResolver{err: target.ErrNotLoggedIn}
	err := preflightAuth(t.Context(), tg, resolver, io.Discard, strings.NewReader("n\n"), false)
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want ErrNotLoggedIn", err)
	}
	if loginCalls != 1 {
		t.Errorf("inline login fired %d times; want 1", loginCalls)
	}
}

func TestPreflightAuth_OtherErrorPropagates(t *testing.T) {
	withInteractive(t, true)
	custom := errors.New("network down")
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	err := preflightAuth(t.Context(), tg, fakeOAuthResolver{err: custom}, io.Discard, strings.NewReader(""), false)
	if !errors.Is(err, custom) {
		t.Fatalf("err = %v; want %v", err, custom)
	}
}

func TestAuthLogin_ManifestBackcompatWarning(t *testing.T) {
	dir := t.TempDir()
	mfPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(mfPath, []byte(`
kind: manifest
name: sample
target:
  name: dev
  url: https://platform.dev.example.com
resources: {}
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	f := &authLoginFlags{manifestPath: mfPath}
	store := &fakeAuthCacheStore{}
	cmd := newAuthLoginCmd()
	stderr := new(strings.Builder)
	cmd.SetOut(io.Discard)
	cmd.SetErr(stderr)
	tg, err := resolveLoginTarget(cmd, f, "", store)
	if err != nil {
		t.Fatalf("resolveLoginTarget: %v", err)
	}
	if tg.Name != "dev" {
		t.Errorf("Name = %q; want dev", tg.Name)
	}
	if tg.URL != "https://platform.dev.example.com" {
		t.Errorf("URL = %q", tg.URL)
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation warning in stderr; got %q", stderr.String())
	}
}

func TestAuthLogin_PositionalNoManifestRequired(t *testing.T) {
	f := &authLoginFlags{}
	store := &fakeAuthCacheStore{}
	cmd := newAuthLoginCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	tg, err := resolveLoginTarget(cmd, f, "platform.dev.example.com", store)
	if err != nil {
		t.Fatalf("resolveLoginTarget: %v", err)
	}
	if tg.URL != "https://platform.dev.example.com" {
		t.Errorf("URL = %q", tg.URL)
	}
	if tg.Name != "platform.dev.example.com" {
		t.Errorf("Name = %q", tg.Name)
	}
}

func TestAuthLogin_FlagsPreservedForLogoutStatus(t *testing.T) {
	f := &authFlags{platformURL: "https://platform.dev.example.com", target: "alt"}
	tg, err := f.resolveTarget()
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if tg.Name != "alt" {
		t.Errorf("Name = %q", tg.Name)
	}
	if tg.URL != "https://platform.dev.example.com" {
		t.Errorf("URL = %q", tg.URL)
	}
}

func TestAuthLogin_ManifestBackcompatRejectsHTTP(t *testing.T) {
	dir := t.TempDir()
	mfPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(mfPath, []byte(`
kind: manifest
name: insecure
target:
  name: dev
  url: http://insecure.example.com
resources: {}
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	f := &authLoginFlags{manifestPath: mfPath}
	cmd := newAuthLoginCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	_, err := resolveLoginTarget(cmd, f, "", &fakeAuthCacheStore{})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS-required error from manifest with http:// url, got %v", err)
	}
}

func TestRunInteractiveLogin_StdinClosedRefuses(t *testing.T) {
	tg := target.Target{Name: "dev", URL: "https://x", Auth: target.AuthSpec{Type: target.AuthOAuth}}
	err := defaultRunInteractiveLogin(t.Context(), tg, io.Discard, strings.NewReader(""))
	if !errors.Is(err, target.ErrNotLoggedIn) {
		t.Fatalf("err = %v; want ErrNotLoggedIn", err)
	}
	if !strings.Contains(err.Error(), "stdin closed") {
		t.Errorf("expected diagnostic about closed stdin; got %v", err)
	}
}
