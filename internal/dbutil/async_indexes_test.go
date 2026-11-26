// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pressly/goose/v3/lock"
)

func TestAsyncIndexBackoff(t *testing.T) {
	t.Parallel()
	want := map[int]time.Duration{
		1:                     time.Minute,
		2:                     2 * time.Minute,
		4:                     8 * time.Minute,
		7:                     time.Hour,
		MaxAsyncIndexAttempts: time.Hour,
	}
	for attempts, d := range want {
		if got := asyncIndexBackoff(attempts); got != d {
			t.Errorf("asyncIndexBackoff(%d) = %v, want %v", attempts, got, d)
		}
	}
}

func TestDueAsyncIndexes(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1_000_000)
	at := func(d time.Duration) sql.NullInt64 {
		return sql.NullInt64{Int64: now.Add(d).UnixMilli(), Valid: true}
	}
	pending := []asyncIndex{
		{name: "never_tried"},
		{name: "backoff_elapsed", nextAttemptAt: at(-time.Millisecond)},
		{name: "waiting_long", nextAttemptAt: at(5 * time.Second)},
		{name: "waiting_short", nextAttemptAt: at(time.Second)},
	}
	due, retryAt := dueAsyncIndexes(pending, now)
	var names []string
	for _, idx := range due {
		names = append(names, idx.name)
	}
	if got := strings.Join(names, ","); got != "never_tried,backoff_elapsed" {
		t.Errorf("due = %s, want never_tried,backoff_elapsed", got)
	}
	if want := now.Add(time.Second); !retryAt.Equal(want) {
		t.Errorf("retryAt = %v, want %v, the earliest row still waiting", retryAt, want)
	}
	if _, retryAt := dueAsyncIndexes(pending[:2], now); !retryAt.IsZero() {
		t.Errorf("retryAt with no row waiting = %v, want zero", retryAt)
	}
}

func TestJitterAsyncIndexWait(t *testing.T) {
	t.Parallel()
	for range 200 {
		if got := jitterAsyncIndexWait(time.Minute); got < time.Minute || got >= time.Minute+15*time.Second {
			t.Fatalf("jittered wait = %v, want within [1m, 1m15s)", got)
		}
	}
}

func TestAsyncIndexCreatePattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		definition string
		want       string
	}{
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t (a)", "idx_a"},
		{"create index concurrently if not exists idx_a on evidence.t using gin (a)", "idx_a"},
		{"  CREATE INDEX\n  CONCURRENTLY IF NOT EXISTS idx_a\n  ON t (a) WHERE a IS NOT NULL", "idx_a"},
		{"CREATE INDEX idx_a ON t (a)", ""},
		{"CREATE INDEX CONCURRENTLY idx_a ON t (a)", ""},
		{"CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t (a)", ""},
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS public.idx_a ON t (a)", ""},
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t (a);", "idx_a"},
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t (a); DROP TABLE t", ""},
	}
	for _, tc := range cases {
		if got := patternName(AsyncIndexCreatePattern, tc.definition); got != tc.want {
			t.Errorf("create pattern on %q = %q, want %q", tc.definition, got, tc.want)
		}
	}
}

func TestAsyncIndexDropPattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		definition string
		want       string
	}{
		{"DROP INDEX CONCURRENTLY IF EXISTS idx_a", "idx_a"},
		{"drop index concurrently if exists idx_a;", "idx_a"},
		{"DROP INDEX idx_a", ""},
		{"DROP INDEX CONCURRENTLY idx_a", ""},
		{"DROP INDEX CONCURRENTLY IF EXISTS idx_a CASCADE", ""},
		{"DROP INDEX CONCURRENTLY IF EXISTS idx_a; DROP TABLE t", ""},
		{"DROP INDEX CONCURRENTLY IF EXISTS public.idx_a", ""},
	}
	for _, tc := range cases {
		if got := patternName(AsyncIndexDropPattern, tc.definition); got != tc.want {
			t.Errorf("drop pattern on %q = %q, want %q", tc.definition, got, tc.want)
		}
	}
}

func TestAsyncIndexTableSchema(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON evidence.traces (a)":      "evidence",
		"create index concurrently if not exists idx_a on only Evidence.traces (a)": "evidence",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON traces (a)":               "",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON traces (evidence.a)":      "",
		"DROP INDEX CONCURRENTLY IF EXISTS idx_a":                                   "",
	}
	for definition, want := range cases {
		if got := AsyncIndexTableSchema(definition); got != want {
			t.Errorf("AsyncIndexTableSchema(%q) = %q, want %q", definition, got, want)
		}
	}
}

func patternName(p *regexp.Regexp, definition string) string {
	if m := p.FindStringSubmatch(definition); len(m) > 1 {
		return m[1]
	}
	return ""
}

func TestAsyncIndexLockID(t *testing.T) {
	t.Parallel()
	gateway := AsyncIndexLockID("gateway_async_indexes")
	if gateway != AsyncIndexLockID("gateway_async_indexes") {
		t.Fatal("lock id is not stable for one queue")
	}
	if gateway == AsyncIndexLockID("evidence.evidence_async_indexes") {
		t.Fatal("two queues share a lock id, so one would wait on the other's builds")
	}
	if gateway == lock.DefaultLockID {
		t.Fatal("the queue lock is goose's migration lock, so migrations would wait behind a build")
	}
}

func TestTruncateAsyncIndexError(t *testing.T) {
	t.Parallel()
	if got := truncateAsyncIndexError("short"); got != "short" {
		t.Fatalf("short message = %q, want it unchanged", got)
	}
	if got := truncateAsyncIndexError(strings.Repeat("a", 5000)); len(got) != maxAsyncIndexErrorLen {
		t.Fatalf("long message has %d bytes, want %d", len(got), maxAsyncIndexErrorLen)
	}
	multibyte := strings.Repeat("a", maxAsyncIndexErrorLen-1) + "é" + "tail"
	got := truncateAsyncIndexError(multibyte)
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a UTF-8 sequence, which Postgres rejects as TEXT")
	}
	if len(got) != maxAsyncIndexErrorLen-1 {
		t.Fatalf("truncated to %d bytes, want %d", len(got), maxAsyncIndexErrorLen-1)
	}
}

func TestBuildAsyncIndexes_RefusesNamesItWouldInterpolate(t *testing.T) {
	t.Parallel()
	for _, cfg := range []AsyncIndexConfig{
		{QueueTable: "gateway_async_indexes; DROP TABLE tasks"},
		{QueueTable: "Gateway_Async_Indexes"},
		{Schema: "evidence; DROP TABLE tasks", QueueTable: "evidence_async_indexes"},
	} {
		if err := BuildAsyncIndexes(t.Context(), cfg); err == nil {
			t.Errorf("BuildAsyncIndexes(%+v) = nil, want it refused before touching the database", cfg)
		}
	}
}
