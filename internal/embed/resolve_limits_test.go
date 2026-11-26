// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
)

func shortenRenderDeadline(t *testing.T, d time.Duration) {
	t.Helper()
	prev := liquidsafe.DefaultRenderDeadline
	liquidsafe.DefaultRenderDeadline = d
	t.Cleanup(func() { liquidsafe.DefaultRenderDeadline = prev })
}

func TestSingleArtifactExceedsPerArtifactCap_ReturnsMarker(t *testing.T) {
	store := newMemArtifactStore()
	const oversize = 200 * 1024 * 1024
	saveArtifact(store, "big.txt", make([]byte, oversize), "text/plain")

	rctx := newResolveContext(store)
	out, signals, err := ResolveEmbedsInString(context.Background(),
		"prefix «artifact_content:big.txt» suffix", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, truncationMarkerArtifactSize) {
		t.Errorf("expected per-artifact truncation marker; got: %q", truncate(out, 200))
	}
	if !strings.Contains(out, "prefix ") || !strings.Contains(out, " suffix") {
		t.Errorf("surrounding text lost: %q", truncate(out, 200))
	}
	if len(out) > 1024 {
		t.Errorf("response should be small after truncation; got %d bytes", len(out))
	}
	if len(signals) != 0 {
		t.Errorf("expected no signals on truncation; got %d", len(signals))
	}
}

func TestCumulativeCap_TruncatesAtThreshold(t *testing.T) {
	store := newMemArtifactStore()
	const each = 30 * 1024 * 1024
	for i := range 10 {
		name := "blob_" + string(rune('a'+i)) + ".txt"
		saveArtifact(store, name, make([]byte, each), "text/plain")
	}

	rctx := newResolveContext(store)
	var b strings.Builder
	for i := range 10 {
		name := "blob_" + string(rune('a'+i)) + ".txt"
		b.WriteString("«artifact_content:")
		b.WriteString(name)
		b.WriteString("»\n")
	}

	out, _, err := ResolveEmbedsInString(context.Background(), b.String(), ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Count(out, truncationMarkerTotalSize)
	if got != 5 {
		t.Errorf("expected exactly 5 cumulative truncation markers, got %d in %d bytes", got, len(out))
	}
	if int64(len(out)) > 151*1024*1024 {
		t.Errorf("response exceeded cumulative cap envelope; got %d bytes", len(out))
	}
	if int64(len(out)) < 150*1024*1024 {
		t.Errorf("response smaller than expected resolved bytes; got %d bytes", len(out))
	}
}

func TestPerArtifactTruncated_DoesNotConsumeCumulativeBudget(t *testing.T) {
	store := newMemArtifactStore()
	const oversize = 200 * 1024 * 1024
	const each = 30 * 1024 * 1024
	saveArtifact(store, "huge.txt", make([]byte, oversize), "text/plain")
	for i := range 5 {
		name := "fits_" + string(rune('a'+i)) + ".txt"
		saveArtifact(store, name, make([]byte, each), "text/plain")
	}

	rctx := newResolveContext(store)
	var b strings.Builder
	b.WriteString("«artifact_content:huge.txt»\n")
	for i := range 5 {
		name := "fits_" + string(rune('a'+i)) + ".txt"
		b.WriteString("«artifact_content:")
		b.WriteString(name)
		b.WriteString("»\n")
	}

	out, _, err := ResolveEmbedsInString(context.Background(), b.String(), ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Count(out, truncationMarkerArtifactSize); got != 1 {
		t.Errorf("expected 1 per-artifact truncation marker; got %d", got)
	}
	if strings.Contains(out, truncationMarkerTotalSize) {
		t.Errorf("oversize artifact should not consume cumulative budget; got cumulative marker")
	}
}

func TestEmbedUnderCap_ResolvesCleanly(t *testing.T) {
	store := newMemArtifactStore()
	const small = 1024
	saveArtifact(store, "small.txt", []byte(strings.Repeat("x", small)), "text/plain")

	rctx := newResolveContext(store)
	out, _, err := ResolveEmbedsInString(context.Background(),
		"«artifact_content:small.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "[Truncated:") {
		t.Errorf("under-cap artifact should not be truncated; got: %q", truncate(out, 200))
	}
	if len(out) != small {
		t.Errorf("expected %d bytes, got %d", small, len(out))
	}
}

func TestCumulativeCap_NotTriggeredBelowThreshold(t *testing.T) {
	store := newMemArtifactStore()
	const each = 30 * 1024 * 1024
	for i := range 3 {
		name := "blob_" + string(rune('a'+i)) + ".txt"
		saveArtifact(store, name, make([]byte, each), "text/plain")
	}

	rctx := newResolveContext(store)
	var b strings.Builder
	for i := range 3 {
		name := "blob_" + string(rune('a'+i)) + ".txt"
		b.WriteString("«artifact_content:")
		b.WriteString(name)
		b.WriteString("»\n")
	}

	out, _, err := ResolveEmbedsInString(context.Background(), b.String(), ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "[Truncated:") {
		t.Errorf("under-cap cumulative resolution should not truncate; got marker in %d bytes", len(out))
	}
}

func TestPathologicalForLoop_AbortsOnDeadline(t *testing.T) {
	shortenRenderDeadline(t, 50*time.Millisecond)

	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"x": 1}`), "application/json")

	const pathological = `«««template_liquid: data="data.json"
{% for n in (1..10000000) %}{{ n }},{% endfor %}»»»`

	svc := newTestArtifactSvc(store)
	start := time.Now()
	out, err := ResolveTemplateBlocks(context.Background(), pathological,
		svc, "test-agent", "user1", "sess1", nil, slog.Default())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ResolveTemplateBlocks should not error on deadline; got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("render exceeded deadline window: elapsed=%s", elapsed)
	}
	if !strings.Contains(out, TruncationMarkerRenderDeadline) {
		t.Errorf("expected render-deadline truncation marker; got %d bytes", len(out))
	}
}

func TestApplyToTemplate_DeadlineExceeded_ReturnsMarker(t *testing.T) {
	shortenRenderDeadline(t, 50*time.Millisecond)

	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"x": 1}`), "application/json")
	saveArtifact(store, "tmpl.liquid",
		[]byte(`{% for n in (1..10000000) %}{{ n }},{% endfor %}`), "text/plain")

	rctx := newResolveContext(store)
	start := time.Now()
	out, _, err := ResolveEmbedsInString(context.Background(),
		"«artifact_content:data.json >>> apply_to_template:tmpl.liquid»",
		ModeA2AMessageToUser, rctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("apply_to_template deadline did not abort promptly: elapsed=%s", elapsed)
	}
	if !strings.Contains(out, TruncationMarkerRenderDeadline) {
		t.Errorf("expected render-deadline truncation marker; got %d bytes", len(out))
	}
}

func TestNormalTemplate_RendersUnaffected(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"x": 1}`), "application/json")

	const normal = `«««template_liquid: data="data.json"
{% for n in (1..10) %}{{ n }},{% endfor %}»»»`

	svc := newTestArtifactSvc(store)
	out, err := ResolveTemplateBlocks(context.Background(), normal,
		svc, "test-agent", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, TruncationMarkerRenderDeadline) {
		t.Errorf("normal template should not trip deadline; got: %q", truncate(out, 200))
	}
	if !strings.Contains(out, "1,2,3,4,5,6,7,8,9,10,") {
		t.Errorf("expected normal Liquid output; got: %q", truncate(out, 200))
	}
}

func TestNoGoroutineLeak_OnDeadlineTrip(t *testing.T) {
	shortenRenderDeadline(t, 25*time.Millisecond)

	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"x": 1}`), "application/json")

	const pathological = `«««template_liquid: data="data.json"
{% for n in (1..10000000) %}{{ n }},{% endfor %}»»»`

	svc := newTestArtifactSvc(store)

	runtime.GC()
	before := runtime.NumGoroutine()

	_, err := ResolveTemplateBlocks(context.Background(), pathological,
		svc, "test-agent", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine leak after deadline trip: before=%d after=%d", before, got)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
