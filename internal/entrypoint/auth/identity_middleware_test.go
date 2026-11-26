// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestIdentityMiddleware_CrossUserProfileLeakDetection(t *testing.T) {
	const self = "blake.whitman@example.org"
	cases := []struct {
		name    string
		profile *identity.Profile
		wantLog bool
	}{
		{
			name:    "leaked profile is another user's email",
			profile: &identity.Profile{ID: "grace.miller@example.org", WorkEmail: "grace.miller@example.org", DisplayName: "Grace Miller"},
			wantLog: true,
		},
		{
			name:    "correct profile matches the authenticated user",
			profile: &identity.Profile{ID: self, WorkEmail: self, DisplayName: "Blake Whitman"},
			wantLog: false,
		},
		{
			name:    "opaque non-email id is not flagged",
			profile: &identity.Profile{ID: "u-12345", DisplayName: "Blake Whitman"},
			wantLog: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			cache := newPrimedCache(self, CachedProfile{Profile: tc.profile, EnrichmentStatus: identity.EnrichmentOK})
			mw := IdentityMiddleware(cache, logger)

			id := &Subject{userID: self, email: self, authenticated: true}
			r := httptest.NewRequest("GET", "/api/v1/user", nil).
				WithContext(ContextWithSubject(context.Background(), id))
			mw((&servingNext{}).handler()).ServeHTTP(httptest.NewRecorder(), r)

			gotLog := strings.Contains(buf.String(), "cross-user profile leak detected")
			if gotLog != tc.wantLog {
				t.Fatalf("leak logged = %v, want %v; logs:\n%s", gotLog, tc.wantLog, buf.String())
			}
		})
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type servingNext struct {
	seen   *Subject
	called atomic.Int64
}

func (s *servingNext) handler() http.HandlerFunc {
	return func(_ http.ResponseWriter, r *http.Request) {
		if id, ok := SubjectFromContext(r.Context()); ok {
			s.seen = id
		}
		s.called.Add(1)
	}
}

func newPrimedCache(userID string, entry CachedProfile) *ProfileCache {
	c := NewProfileCache(time.Hour)
	c.Put(userID, entry)
	return c
}

func TestIdentityMiddleware_CacheHit_PopulatesProfileAndStatus(t *testing.T) {
	cache := newPrimedCache("alice", CachedProfile{
		Profile:          &identity.Profile{ID: "u-1", DisplayName: "Alice"},
		EnrichmentStatus: identity.EnrichmentOK,
	})
	mw := IdentityMiddleware(cache, discardLogger())

	id := &Subject{userID: "alice", email: "alice@example.com", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if next.called.Load() != 1 {
		t.Fatalf("next called %d times, want 1", next.called.Load())
	}
	if next.seen == nil || next.seen.enrichmentStatus != identity.EnrichmentOK {
		t.Fatalf("EnrichmentStatus = %v, want ok (seen=%+v)", next.seen.enrichmentStatus, next.seen)
	}
	if next.seen.profile == nil || next.seen.profile.ID != "u-1" {
		t.Errorf("Profile not populated: %+v", next.seen.profile)
	}
}

func TestIdentityMiddleware_CacheHit_UnavailableStatusPassesThrough(t *testing.T) {
	cache := newPrimedCache("alice", CachedProfile{
		Profile:          nil,
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "profile lookup timed out at mint",
	})
	mw := IdentityMiddleware(cache, discardLogger())

	id := &Subject{userID: "alice", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degraded enrichment must not block)", rec.Code)
	}
	if next.called.Load() != 1 {
		t.Fatalf("next called %d times, want 1", next.called.Load())
	}
	if next.seen.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want unavailable", next.seen.enrichmentStatus)
	}
	if next.seen.enrichmentReason != "profile lookup timed out at mint" {
		t.Errorf("EnrichmentReason = %q", next.seen.enrichmentReason)
	}
	if next.seen.profile != nil {
		t.Errorf("Profile should be nil when unavailable, got %+v", next.seen.profile)
	}
}

func TestIdentityMiddleware_CacheMiss_StampsUnavailable(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	mw := IdentityMiddleware(cache, discardLogger())

	id := &Subject{userID: "alice", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (cache miss must not 503 — the user just won't have profile until token refresh)", rec.Code)
	}
	if next.seen.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want unavailable", next.seen.enrichmentStatus)
	}
	if next.seen.enrichmentReason != "cache miss" {
		t.Errorf("EnrichmentReason = %q, want \"cache miss\"", next.seen.enrichmentReason)
	}
	if next.seen.profile != nil {
		t.Errorf("Profile must be nil on cache miss, got %+v", next.seen.profile)
	}
}

func TestIdentityMiddleware_NilCache_StampsNotConfigured(t *testing.T) {
	mw := IdentityMiddleware(nil, discardLogger())
	id := &Subject{userID: "alice", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if id.enrichmentStatus != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %q, want not_configured", id.enrichmentStatus)
	}
}

func TestIdentityMiddleware_NoIdentity_Passthrough(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	mw := IdentityMiddleware(cache, discardLogger())

	r := httptest.NewRequest("GET", "/api/v1/anything", nil)
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if next.called.Load() != 1 {
		t.Errorf("downstream handler called %d times, want 1 (skip-auth pass-through)", next.called.Load())
	}
}

func TestIdentityMiddleware_AlreadyStamped_Skip(t *testing.T) {
	cache := newPrimedCache("alice", CachedProfile{
		Profile:          &identity.Profile{ID: "from-cache"},
		EnrichmentStatus: identity.EnrichmentOK,
	})
	mw := IdentityMiddleware(cache, discardLogger())

	preStamped := &identity.Profile{ID: "from-upstream"}
	id := &Subject{
		userID:           "alice",
		authenticated:    true,
		profile:          preStamped,
		enrichmentStatus: identity.EnrichmentOK,
	}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if next.seen.profile != preStamped {
		t.Errorf("Profile overwritten by cache; want pre-stamped value preserved")
	}
}

func TestIdentityMiddleware_CacheMissLogDedup(t *testing.T) {
	cache := NewProfileCache(time.Hour)

	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mw := IdentityMiddleware(cache, logger, WithUnavailableLogWindow(time.Hour))

	const requests = 50
	for range requests {
		id := &Subject{userID: "alice", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)
	}

	dec := json.NewDecoder(strings.NewReader(buf.String()))
	emitted := 0
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			break
		}
		if rec["msg"] == "profile enrichment degraded" {
			emitted++
		}
	}
	if emitted != 1 {
		t.Errorf("log dedup not enforced: emitted %d WARN entries for %d requests (want exactly 1)", emitted, requests)
	}
}

func TestIdentityMiddleware_LazyLookup_PopulatesCacheOnMiss(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	var calls atomic.Int64
	lookup := func(_ context.Context, id *Subject) (CachedProfile, error) {
		calls.Add(1)
		return CachedProfile{
			Profile:          &identity.Profile{ID: "u-1", DisplayName: id.email},
			EnrichmentStatus: identity.EnrichmentOK,
		}, nil
	}
	mw := IdentityMiddleware(cache, discardLogger(), WithLazyEnrichmentLookup(lookup))

	serve := func() *Subject {
		id := &Subject{userID: "alice", email: "alice@example.com", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		next := &servingNext{}
		mw(next.handler()).ServeHTTP(httptest.NewRecorder(), r)
		return next.seen
	}

	first := serve()
	if first == nil || first.profile == nil || first.profile.ID != "u-1" {
		t.Fatalf("first request: Profile not stamped from lazy lookup: %+v", first)
	}
	if first.enrichmentStatus != identity.EnrichmentOK {
		t.Errorf("first request: EnrichmentStatus = %q", first.enrichmentStatus)
	}

	second := serve()
	if second == nil || second.profile == nil || second.profile.ID != "u-1" {
		t.Errorf("second request: cache miss despite first lookup: %+v", second)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("lazy lookup fired %d times across 2 requests, want 1 (second must hit cache)", got)
	}
}

func TestIdentityMiddleware_LazyLookup_ErrorStampsUnavailable(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	lookup := func(context.Context, *Subject) (CachedProfile, error) {
		return CachedProfile{}, errLookup
	}
	mw := IdentityMiddleware(cache, discardLogger(), WithLazyEnrichmentLookup(lookup))

	id := &Subject{userID: "alice", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	rec := httptest.NewRecorder()

	next := &servingNext{}
	mw(next.handler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (lazy lookup failure must not block)", rec.Code)
	}
	if next.seen.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want unavailable", next.seen.enrichmentStatus)
	}
	if !strings.Contains(next.seen.enrichmentReason, "lazy lookup failed") {
		t.Errorf("EnrichmentReason = %q, want contains \"lazy lookup failed\"", next.seen.enrichmentReason)
	}
	if next.seen.profile != nil {
		t.Errorf("Profile must be nil on lazy lookup failure, got %+v", next.seen.profile)
	}
	if _, ok := cache.Get("alice"); ok {
		t.Error("failed lazy lookup wrote a cache entry; expected no caching of error path")
	}
}

func TestIdentityMiddleware_LazyLookup_DegradedResultNotCached(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	calls := 0
	lookup := func(context.Context, *Subject) (CachedProfile, error) {
		calls++
		return CachedProfile{
			EnrichmentStatus: identity.EnrichmentUnavailable,
			EnrichmentReason: "upstream BambooHR 503",
		}, nil
	}
	mw := IdentityMiddleware(cache, discardLogger(), WithLazyEnrichmentLookup(lookup))

	serve := func() *Subject {
		id := &Subject{userID: "alice", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		next := &servingNext{}
		mw(next.handler()).ServeHTTP(httptest.NewRecorder(), r)
		return next.seen
	}

	first := serve()
	if first.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Fatalf("first request: status = %q, want unavailable", first.enrichmentStatus)
	}
	if _, ok := cache.Get("alice"); ok {
		t.Error("unavailable result wrote a cache entry; expected no caching")
	}
	_ = serve()
	if calls != 2 {
		t.Errorf("lazy lookup fired %d times; want 2 (each request retries while unavailable)", calls)
	}
}

func TestIdentityMiddleware_LazyLookup_ErrorBacksOff(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	calls := 0
	fail := true
	lookup := func(context.Context, *Subject) (CachedProfile, error) {
		calls++
		if fail {
			return CachedProfile{}, errLookup
		}
		return CachedProfile{EnrichmentStatus: identity.EnrichmentOK}, nil
	}
	mw := IdentityMiddleware(cache, discardLogger(),
		WithLazyEnrichmentLookup(lookup),
		WithLazyLookupBackoff(50*time.Millisecond))

	serve := func() *Subject {
		id := &Subject{userID: "alice", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		next := &servingNext{}
		mw(next.handler()).ServeHTTP(httptest.NewRecorder(), r)
		return next.seen
	}

	if got := serve(); got.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Fatalf("first request: status = %q, want unavailable", got.enrichmentStatus)
	}
	got := serve()
	if got.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("backed-off request: status = %q, want unavailable", got.enrichmentStatus)
	}
	if calls != 1 {
		t.Fatalf("lazy lookup fired %d times inside backoff window; want 1", calls)
	}

	time.Sleep(60 * time.Millisecond)
	fail = false
	if got := serve(); got.enrichmentStatus != identity.EnrichmentOK {
		t.Errorf("post-backoff request: status = %q, want ok", got.enrichmentStatus)
	}
	if calls != 2 {
		t.Errorf("lazy lookup fired %d times; want 2 (retry after window)", calls)
	}
	if _, ok := cache.Get("alice"); !ok {
		t.Error("successful lookup was not cached after backoff cleared")
	}
}

func TestIdentityMiddleware_LazyLookup_NotConfiguredIsCached(t *testing.T) {
	cache := NewProfileCache(time.Hour)
	calls := 0
	lookup := func(context.Context, *Subject) (CachedProfile, error) {
		calls++
		return CachedProfile{EnrichmentStatus: identity.EnrichmentNotConfigured}, nil
	}
	mw := IdentityMiddleware(cache, discardLogger(), WithLazyEnrichmentLookup(lookup))

	serve := func() {
		id := &Subject{userID: "alice", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)
	}
	serve()
	serve()
	if calls != 1 {
		t.Errorf("lazy lookup fired %d times; want 1 (not_configured is terminal — must be cached)", calls)
	}
}

func TestIdentityMiddleware_UnknownStatus_LogsWarn(t *testing.T) {
	cache := newPrimedCache("alice", CachedProfile{
		EnrichmentStatus: identity.EnrichmentStatus("totally-made-up"),
		EnrichmentReason: "from the future",
	})
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mw := IdentityMiddleware(cache, logger)

	id := &Subject{userID: "alice", authenticated: true}
	r := httptest.NewRequest("GET", "/api/v1/anything", nil).
		WithContext(ContextWithSubject(context.Background(), id))
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)

	if !strings.Contains(buf.String(), "unknown enrichment status") {
		t.Errorf("expected unknown-status WARN; logs = %q", buf.String())
	}
}

var errLookup = lookupError("simulated broker failure")

type lookupError string

func (e lookupError) Error() string { return string(e) }

func TestIdentityMiddleware_LogDedupResetsAfterWindow(t *testing.T) {
	cache := NewProfileCache(time.Hour)

	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mw := IdentityMiddleware(cache, logger, WithUnavailableLogWindow(10*time.Millisecond))

	serve := func() {
		id := &Subject{userID: "alice", authenticated: true}
		r := httptest.NewRequest("GET", "/api/v1/anything", nil).
			WithContext(ContextWithSubject(context.Background(), id))
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), r)
	}

	for range 5 {
		serve()
	}
	time.Sleep(20 * time.Millisecond)
	serve()

	dec := json.NewDecoder(strings.NewReader(buf.String()))
	var records []map[string]any
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			break
		}
		if rec["msg"] == "profile enrichment degraded" {
			records = append(records, rec)
		}
	}
	if len(records) != 2 {
		t.Fatalf("got %d WARN logs, want 2 (one per window)", len(records))
	}
	if got := records[1]["suppressedSinceLast"]; got != float64(4) {
		t.Errorf("suppressedSinceLast = %v, want 4", got)
	}
}

func TestIdentityMiddleware_APIKeyIdentitySkipsTheLazyLookup(t *testing.T) {
	var calls atomic.Int64
	lookup := func(context.Context, *Subject) (CachedProfile, error) {
		calls.Add(1)
		return CachedProfile{EnrichmentStatus: identity.EnrichmentUnavailable}, nil
	}
	mw := IdentityMiddleware(NewProfileCache(time.Hour), discardLogger(), WithLazyEnrichmentLookup(lookup))

	keyIdentity, rejection := ResolveAPIKey(t.Context(), &fakeKeyAuthority{resolution: apikey.Resolution{
		KeyID: "key-1", OwnerSub: "system:ci-deployer", Scopes: []string{"agent:*:invoke"},
	}}, "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	if rejection != nil {
		t.Fatalf("ResolveAPIKey: %+v", rejection)
	}
	r := httptest.NewRequest("GET", "/api/v1/sessions", nil).
		WithContext(ContextWithSubject(t.Context(), keyIdentity))
	next := &servingNext{}
	mw(next.handler()).ServeHTTP(httptest.NewRecorder(), r)

	if got := calls.Load(); got != 0 {
		t.Errorf("lazy lookup ran %d times for a key identity, want 0", got)
	}
	if next.seen == nil || next.seen.EnrichmentStatus() != identity.EnrichmentNotConfigured {
		t.Errorf("EnrichmentStatus = %+v, want not_configured", next.seen)
	}
}
