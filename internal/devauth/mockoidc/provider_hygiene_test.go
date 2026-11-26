// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockoidc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUsersBySub_LookupCorrectness(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{
			{Sub: "alice", Email: "alice@x", Name: "Alice"},
			{Sub: "bob", Email: "bob@x", Name: "Bob"},
			{Sub: "alice", Email: "alice-2@x", Name: "Alice Two"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	if !p.knowsUser("alice") || !p.knowsUser("bob") {
		t.Error("known users must resolve")
	}
	if p.knowsUser("carol") || p.knowsUser("") {
		t.Error("unknown/empty sub must not resolve")
	}
	if got := p.lookupUser("alice"); got == nil || got.Email != "alice@x" {
		t.Errorf("duplicate sub must resolve to first entry, got %+v", got)
	}
	if p.lookupUser("carol") != nil {
		t.Error("unknown sub must look up to nil")
	}
}

func TestEvictStale_RemovesStaleKeepsFresh(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice", Email: "alice@x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	now := time.Now()
	p.mu.Lock()
	p.authCodes["stale-code"] = &oidcAuthCodeEntry{createdAt: now.Add(-authCodeTTL - time.Minute)}
	p.authCodes["fresh-code"] = &oidcAuthCodeEntry{createdAt: now.Add(-time.Minute)}
	p.accessTokens["stale-at"] = &oidcAccessTokenEntry{lastUsed: now.Add(-tokenIdleTTL - time.Minute)}
	p.accessTokens["fresh-at"] = &oidcAccessTokenEntry{lastUsed: now.Add(-time.Minute)}
	p.refreshTokens["stale-rt"] = &oidcRefreshTokenEntry{lastUsed: now.Add(-tokenIdleTTL - time.Minute)}
	p.refreshTokens["fresh-rt"] = &oidcRefreshTokenEntry{lastUsed: now.Add(-time.Minute)}
	p.mu.Unlock()

	p.evictStale(now)

	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.authCodes["stale-code"]; ok {
		t.Error("stale auth code should be evicted")
	}
	if _, ok := p.authCodes["fresh-code"]; !ok {
		t.Error("fresh auth code should be kept")
	}
	if _, ok := p.accessTokens["stale-at"]; ok {
		t.Error("idle access token should be evicted")
	}
	if _, ok := p.accessTokens["fresh-at"]; !ok {
		t.Error("fresh access token should be kept")
	}
	if _, ok := p.refreshTokens["stale-rt"]; ok {
		t.Error("idle refresh token should be evicted")
	}
	if _, ok := p.refreshTokens["fresh-rt"]; !ok {
		t.Error("fresh refresh token should be kept")
	}
}

type recordingHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Message)
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) messages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.msgs)
}

func TestProviderLogging(t *testing.T) {
	rh := &recordingHandler{}
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users:  []FakeOIDCUser{{Sub: "alice", Email: "alice@x", Name: "Alice"}},
		Logger: slog.New(rh),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	authorizeAndExchange(t, p, "alice")

	msgs := rh.messages()
	authorized := slices.Contains(msgs, "user authorized")
	issued := slices.Contains(msgs, "token issued")
	if !authorized || !issued {
		t.Fatalf("want both 'user authorized' and 'token issued', got %v", msgs)
	}
	if got := countOccurrences(msgs, "user authorized"); got != 1 {
		t.Errorf("want 1 'user authorized', got %d", got)
	}
	if got := countOccurrences(msgs, "token issued"); got != 1 {
		t.Errorf("want 1 'token issued', got %d", got)
	}

	silent, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "bob", Email: "bob@x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	silent.logEvent("user authorized", "sub", "bob")
	authorizeAndExchange(t, silent, "bob")
}

func countOccurrences(s []string, want string) int {
	n := 0
	for _, v := range s {
		if v == want {
			n++
		}
	}
	return n
}

func TestPickerGating(t *testing.T) {
	get := func(t *testing.T, p *MockOIDCProvider) string {
		t.Helper()
		u := fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s",
			p.URL(), url.QueryEscape("http://cb.example/cb"))
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("GET authorize: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read authorize body: %v", err)
		}
		return string(body)
	}

	small := makePickerProvider(t, 2)
	html := get(t, small)
	if !strings.Contains(html, `class="u"`) {
		t.Errorf("<=%d users should render clickable links: %s", pickerUserLimit, html)
	}
	if strings.Contains(html, `name="fake_user_sub"`) {
		t.Errorf("<=%d users should not render the text-input form", pickerUserLimit)
	}

	large := makePickerProvider(t, pickerUserLimit+1)
	html = get(t, large)
	if !strings.Contains(html, `<form`) || !strings.Contains(html, `name="fake_user_sub"`) {
		t.Errorf(">%d users should render the text-input form: %s", pickerUserLimit, html)
	}
	if strings.Contains(html, `class="u"`) {
		t.Errorf(">%d users should not enumerate clickable links", pickerUserLimit)
	}
}

func makePickerProvider(t *testing.T, n int) *MockOIDCProvider {
	t.Helper()
	users := make([]FakeOIDCUser, 0, n)
	for i := range n {
		users = append(users, FakeOIDCUser{Sub: fmt.Sprintf("u-%d", i), Email: fmt.Sprintf("u-%d@x", i)})
	}
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", EnablePicker: true, Users: users,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}
