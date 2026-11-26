// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func TestEmailCache_CacheHitAndExpiry(t *testing.T) {
	cache := &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     1 * time.Second,
	}

	cache.cacheProfile("U123", "alice@example.com", "")

	cache.mu.RLock()
	entry, ok := cache.entries["U123"]
	cache.mu.RUnlock()
	if !ok {
		t.Fatal("expected cache entry for U123")
	}
	if entry.email != "alice@example.com" {
		t.Errorf("cached email = %q, want %q", entry.email, "alice@example.com")
	}
	if time.Now().After(entry.expires) {
		t.Error("entry should not be expired yet")
	}
}

func TestEmailCache_NoEmailSentinel(t *testing.T) {
	cache := &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     1 * time.Hour,
	}

	cache.cacheProfile("U456", noEmailMarker, "")

	cache.mu.RLock()
	entry, ok := cache.entries["U456"]
	cache.mu.RUnlock()
	if !ok {
		t.Fatal("expected cache entry for U456")
	}
	if entry.email != noEmailMarker {
		t.Errorf("cached email = %q, want sentinel %q", entry.email, noEmailMarker)
	}
}

func TestEmailCache_ZeroTTLDisablesCache(t *testing.T) {
	cache := &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     0,
	}

	cache.cacheProfile("U789", "bob@example.com", "")

	cache.mu.RLock()
	_, ok := cache.entries["U789"]
	cache.mu.RUnlock()
	if ok {
		t.Error("zero TTL should disable caching")
	}
}

func TestResolveIdentity_WithEmail(t *testing.T) {
	cache := &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     1 * time.Hour,
	}
	cache.entries["U100"] = emailEntry{
		email:   "user@company.com",
		expires: time.Now().Add(1 * time.Hour),
	}

	id := cache.ResolveIdentity("U100", "T200")
	if id.UserID() != "user@company.com" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "user@company.com")
	}
	if id.Email() != "user@company.com" {
		t.Errorf("Email = %q, want %q", id.Email(), "user@company.com")
	}
	if id.AuthMethod() != "slack" {
		t.Errorf("AuthMethod = %q, want %q", id.AuthMethod(), "slack")
	}
}

func TestResolveIdentity_WithoutEmail(t *testing.T) {
	cache := &EmailCache{
		entries: make(map[string]emailEntry),
		ttl:     1 * time.Hour,
	}
	cache.entries["U100"] = emailEntry{
		email:   noEmailMarker,
		expires: time.Now().Add(1 * time.Hour),
	}

	id := cache.ResolveIdentity("U100", "T200")
	if id.UserID() != "slack:T200:U100" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "slack:T200:U100")
	}
	if id.Email() != "" {
		t.Errorf("Email = %q, want empty", id.Email())
	}
}

func newTestCache(t *testing.T) (*EmailCache, *slackapi.InMemorySlackAPI) {
	t.Helper()
	api := slackapi.NewInMemorySlackAPI()
	cache := NewEmailCache(api, 1*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return cache, api
}

func TestResolveMentions_Email(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U010URHCMU5", &goslack.UserProfile{Email: "mark@example.org", RealNameNormalized: "Mark B"})

	got := cache.ResolveMentions(t.Context(), "what is <@U010URHCMU5>'s email address")
	want := "what is mark@example.org's email address"
	if got != want {
		t.Errorf("ResolveMentions = %q, want %q", got, want)
	}
}

func TestResolveMentions_NameFallback(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U222", &goslack.UserProfile{RealNameNormalized: "Jane Doe"})

	got := cache.ResolveMentions(t.Context(), "ping <@U222> please")
	want := "ping Jane Doe please"
	if got != want {
		t.Errorf("ResolveMentions = %q, want %q", got, want)
	}
}

func TestResolveMentions_UnresolvedLeftIntact(t *testing.T) {
	cache, _ := newTestCache(t)
	got := cache.ResolveMentions(t.Context(), "hi <@U999>")
	if got != "hi <@U999>" {
		t.Errorf("ResolveMentions = %q, want token left intact", got)
	}
}

func TestResolveMentions_MultipleAndLabelledForm(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U111", &goslack.UserProfile{Email: "a@example.org"})
	api.SeedUserProfile("U222", &goslack.UserProfile{Email: "b@example.org"})

	got := cache.ResolveMentions(t.Context(), "cc <@U111> and <@U222|bob>, also <@U111>")
	want := "cc a@example.org and b@example.org, also a@example.org"
	if got != want {
		t.Errorf("ResolveMentions = %q, want %q", got, want)
	}
}

func TestResolveMentions_NoMentionsNoAPICall(t *testing.T) {
	cache, api := newTestCache(t)
	got := cache.ResolveMentions(t.Context(), "plain text with no mentions")
	if got != "plain text with no mentions" {
		t.Errorf("ResolveMentions = %q, want unchanged", got)
	}
	for _, c := range api.Calls() {
		if c.Method == "GetUserProfile" {
			t.Errorf("unexpected GetUserProfile call for mention-free text")
		}
	}
}

func TestResolveMentions_DedupesLookups(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U111", &goslack.UserProfile{Email: "a@example.org"})

	cache.ResolveMentions(t.Context(), "<@U111> <@U111> <@U111>")

	var calls int
	for _, c := range api.Calls() {
		if c.Method == "GetUserProfile" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("GetUserProfile called %d times, want 1 (deduped within one call)", calls)
	}
}

func TestResolveMentions_CrossCallCacheHit(t *testing.T) {
	cache, api := newTestCache(t)
	api.SeedUserProfile("U111", &goslack.UserProfile{Email: "a@example.org"})

	cache.ResolveMentions(t.Context(), "first <@U111>")
	cache.ResolveMentions(t.Context(), "second <@U111>")

	var calls int
	for _, c := range api.Calls() {
		if c.Method == "GetUserProfile" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("GetUserProfile called %d times across two calls, want 1 (cache hit)", calls)
	}
}

func TestResolveMentions_ErrorLeavesTokenIntact(t *testing.T) {
	cache, api := newTestCache(t)
	api.SetNextError("GetUserProfile", fmt.Errorf("rate_limited"))

	got := cache.ResolveMentions(t.Context(), "hey <@U333>")
	if got != "hey <@U333>" {
		t.Errorf("ResolveMentions = %q, want token left intact on API error", got)
	}
}
