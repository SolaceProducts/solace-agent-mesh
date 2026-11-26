// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestProvider(t *testing.T, handler http.Handler) (*provider, *httptest.Server) {
	t.Helper()
	return newTestProviderWithLogger(t, handler, slog.New(slog.DiscardHandler))
}

func newTestProviderWithLogger(t *testing.T, handler http.Handler, logger *slog.Logger) (*provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := &bamboohrConfig{
		Subdomain:      "acme",
		APIKey:         "test-key",
		LookupKey:      defaultLookupKey,
		CacheTTL:       5 * time.Minute,
		BaseURL:        srv.URL,
		RequestTimeout: 2 * time.Second,
	}
	p := newProvider(cfg, logger)
	p.client.retryBackoff = 5 * time.Millisecond
	return p, srv
}

const directoryPayload = `{
    "employees":[
        {"id":"10","displayName":"Jane Smith","preferredName":"Jane","lastName":"Smith","workEmail":"Jane.Smith@acme.co","jobTitle":"Engineer","department":"R&D","supervisor":"Alice Boss","photoUrl":"https://example.test/photos/jane.jpg"},
        {"id":"20","displayName":"Alice Boss","preferredName":"Alice","lastName":"Boss","workEmail":"alice.boss@acme.co","jobTitle":"Director"},
        {"id":"30","displayName":"Bob Broke","workEmail":""}
    ]
}`

func TestProvider_EnrichByLookupValue_Success(t *testing.T) {
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	profile, err := p.enrichByLookupValue(t.Context(), "JANE.smith@acme.co")
	if err != nil {
		t.Fatalf("enrichByLookupValue: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile, got nil")
	}
	if profile.WorkEmail != "jane.smith@acme.co" {
		t.Errorf("WorkEmail = %q", profile.WorkEmail)
	}
	if profile.DisplayName != "Jane Smith" {
		t.Errorf("DisplayName = %q", profile.DisplayName)
	}
	if profile.SupervisorID != "alice.boss@acme.co" {
		t.Errorf("SupervisorID = %q (expected backfill from display name)", profile.SupervisorID)
	}
	if profile.JobTitle != "Engineer" {
		t.Errorf("JobTitle = %q", profile.JobTitle)
	}
	if hits.Load() != 1 {
		t.Errorf("BambooHR hits = %d, want 1", hits.Load())
	}
}

func TestProvider_EnrichByLookupValue_Caches(t *testing.T) {
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	for i := range 3 {
		if _, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co"); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("BambooHR hits = %d, want 1 (cache should absorb repeat calls)", got)
	}
}

func TestProvider_EnrichByLookupValue_Empty(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("should not reach BambooHR")
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	profile, err := p.enrichByLookupValue(t.Context(), "")
	if err != nil {
		t.Fatalf("enrichByLookupValue: %v", err)
	}
	if profile != nil {
		t.Errorf("expected nil profile for empty lookup, got %+v", profile)
	}
}

func TestProvider_EnrichByLookupValue_NotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	profile, err := p.enrichByLookupValue(t.Context(), "nobody@acme.co")
	if err != nil {
		t.Fatalf("enrichByLookupValue: %v", err)
	}
	if profile != nil {
		t.Error("expected nil profile for unknown user")
	}
}

func TestProvider_EnrichByLookupValue_401(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	p, _ := newTestProvider(t, handler)

	_, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProvider_SearchUsers(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	results, err := p.searchUsers(t.Context(), "jane", 10)
	if err != nil {
		t.Fatalf("searchUsers: %v", err)
	}
	if len(results) != 1 || results[0].WorkEmail != "jane.smith@acme.co" {
		t.Errorf("results = %+v", results)
	}

	empty, err := p.searchUsers(t.Context(), "j", 10)
	if err != nil {
		t.Errorf("short query returned error: %v", err)
	}
	if empty == nil {
		t.Error("short query should return empty slice, not nil")
	}
	if len(empty) != 0 {
		t.Errorf("short query returned %d results, want 0", len(empty))
	}
}

func TestProvider_SearchUsers_LimitRespected(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	results, err := p.searchUsers(t.Context(), "acme", 1)
	if err != nil {
		t.Fatalf("searchUsers: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected limit=1 to cap results, got %d", len(results))
	}
}

func TestProvider_SupervisorEmailLowercased(t *testing.T) {
	payload := `{"employees":[
        {"id":"1","workEmail":"a@acme.co","displayName":"A","supervisorEmail":"BOSS@acme.co"},
        {"id":"2","workEmail":"boss@acme.co","displayName":"Boss"}
    ]}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	})
	p, _ := newTestProvider(t, handler)

	profile, err := p.getUserProfile(t.Context(), "a@acme.co")
	if err != nil {
		t.Fatalf("getUserProfile: %v", err)
	}
	if got := profile["supervisorEmail"]; got != "boss@acme.co" {
		t.Errorf("raw supervisorEmail = %v, want lowercased", got)
	}
}

func TestProvider_GetUserProfile(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	profile, err := p.getUserProfile(t.Context(), "jane.smith@acme.co")
	if err != nil {
		t.Fatalf("getUserProfile: %v", err)
	}
	if profile["id"] != "10" {
		t.Errorf("id = %v", profile["id"])
	}
	if profile["photoUrl"] != "https://example.test/photos/jane.jpg" {
		t.Errorf("photoUrl = %v", profile["photoUrl"])
	}
}

func TestProvider_GetTimeOff(t *testing.T) {
	var whosOutQuery url.Values
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			whosOutQuery = r.URL.Query()
			_, _ = io.WriteString(w, `[
                {"employeeId":10,"start":"2026-04-20","end":"2026-04-25","type":"Vacation"},
                {"employeeId":20,"start":"2026-04-20","end":"2026-04-20","type":"Sick"}
            ]`)
		default:
			http.NotFound(w, r)
		}
	})
	p, _ := newTestProvider(t, handler)

	entries, err := p.getTimeOff(t.Context(), "jane.smith@acme.co", "2026-04-16", "2026-04-30")
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Type != "Vacation" || entries[0].Amount != "full_day" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
	if got := whosOutQuery.Get("start"); got != "2026-04-16" {
		t.Errorf("start = %q", got)
	}
}

func TestProvider_GetTimeOff_UnknownEmployee(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	entries, err := p.getTimeOff(t.Context(), "ghost@acme.co", "", "")
	if err != nil {
		t.Fatalf("getTimeOff: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil, got %+v", entries)
	}
}

func TestProvider_GetTimeOffMulti(t *testing.T) {
	var whosOutCalls int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			whosOutCalls++
			_, _ = io.WriteString(w, `[
                {"employeeId":10,"start":"2026-04-20","end":"2026-04-25","type":"Vacation"},
                {"employeeId":20,"start":"2026-04-20","end":"2026-04-20","type":"Sick"}
            ]`)
		default:
			http.NotFound(w, r)
		}
	})
	p, _ := newTestProvider(t, handler)

	results, err := p.getTimeOffMulti(t.Context(),
		[]string{"jane.smith@acme.co", "alice.boss@acme.co", "ghost@acme.co"},
		"2026-04-16", "2026-04-30")
	if err != nil {
		t.Fatalf("getTimeOffMulti: %v", err)
	}

	if whosOutCalls != 1 {
		t.Errorf("whos_out fetched %d times, want 1", whosOutCalls)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	if results[0].UserID != "jane.smith@acme.co" || len(results[0].Entries) != 1 || results[0].Entries[0].Type != "Vacation" {
		t.Errorf("jane group = %+v", results[0])
	}
	if results[1].UserID != "alice.boss@acme.co" || len(results[1].Entries) != 1 || results[1].Entries[0].Type != "Sick" {
		t.Errorf("alice group = %+v", results[1])
	}
	if results[2].UserID != "ghost@acme.co" || len(results[2].Entries) != 0 {
		t.Errorf("ghost group = %+v (want empty, non-nil)", results[2])
	}
	if results[2].Entries == nil {
		t.Error("unresolved user's Entries must be non-nil (empty slice) for clean JSON")
	}
}

func TestProvider_GetTimeOffMulti_SoftFailOnFetchError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/v1/employees/directory":
			_, _ = io.WriteString(w, directoryPayload)
		case "/acme/v1/time_off/whos_out":
			http.Error(w, "nope", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, _ := newTestProvider(t, handler)

	results, err := p.getTimeOffMulti(t.Context(),
		[]string{"jane.smith@acme.co", "alice.boss@acme.co"}, "", "")
	if err != nil {
		t.Fatalf("getTimeOffMulti must soft-fail on fetch error, got %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, r := range results {
		if r.Entries == nil {
			t.Errorf("%s Entries must be non-nil empty on soft-fail", r.UserID)
		}
		if len(r.Entries) != 0 {
			t.Errorf("%s should have no entries after fetch error, got %+v", r.UserID, r.Entries)
		}
	}
}

func TestProvider_GetUserProfilePicture(t *testing.T) {
	photoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGDATA"))
	}))
	defer photoSrv.Close()

	payload := fmt.Sprintf(`{"employees":[{"id":"10","workEmail":"jane@acme.co","photoUrl":%q}]}`, photoSrv.URL+"/jane.jpg")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	})
	p, _ := newTestProvider(t, handler)

	body, ct, err := p.getUserProfilePicture(t.Context(), "jane@acme.co")
	if err != nil {
		t.Fatalf("getUserProfilePicture: %v", err)
	}
	if string(body) != "JPEGDATA" {
		t.Errorf("body = %q", body)
	}
	if ct != "image/jpeg" {
		t.Errorf("content type = %q", ct)
	}
}

func TestProvider_GetUserProfilePicture_NoPhoto(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	body, ct, err := p.getUserProfilePicture(t.Context(), "alice.boss@acme.co")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if body != nil || ct != "" {
		t.Errorf("expected nil body/ct, got body=%d ct=%q", len(body), ct)
	}
}

func TestProvider_GetUserProfilePicture_SoftFailOnError(t *testing.T) {
	photoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer photoSrv.Close()
	payload := fmt.Sprintf(`{"employees":[{"id":"10","workEmail":"jane@acme.co","photoUrl":%q}]}`, photoSrv.URL+"/jane.jpg")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	})
	p, _ := newTestProvider(t, handler)
	p.client.retryAttempts = 1

	body, ct, err := p.getUserProfilePicture(t.Context(), "jane@acme.co")
	if err != nil {
		t.Fatalf("expected soft-fail, got err = %v", err)
	}
	if body != nil || ct != "" {
		t.Errorf("expected empty on error, got body=%d ct=%q", len(body), ct)
	}
}

func TestProvider_CacheExpiry(t *testing.T) {
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)
	p.cacheTTL = 50 * time.Millisecond

	if _, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := p.enrichByLookupValue(t.Context(), "jane.smith@acme.co"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("expected 2 BambooHR calls across TTL boundary, got %d", got)
	}
}

func TestProvider_ConcurrentRefreshSingleflight(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		_, _ = io.WriteString(w, directoryPayload)
	})
	p, _ := newTestProvider(t, handler)

	ctx := t.Context()
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := p.enrichByLookupValue(ctx, "jane.smith@acme.co"); err != nil {
				t.Errorf("enrichByLookupValue: %v", err)
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 BambooHR request with singleflight, got %d", got)
	}
}
