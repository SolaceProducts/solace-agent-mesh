// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package bootstrap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	_ "modernc.org/sqlite"
)

type logLine struct {
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	LoggerName string `json:"logger_name"`
}

func captureEditionLog(t *testing.T, edition productAnalyticsEdition) []logLine {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logProductAnalyticsEditionFor(edition, logger)

	var lines []logLine
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestEditionLogIsSilentWhenCompiledOut(t *testing.T) {
	lines := captureEditionLog(t, productAnalyticsEdition{compiledIn: false, tokenConfigured: false})

	if len(lines) != 0 {
		t.Errorf("logged %d line(s), want none: %+v", len(lines), lines)
	}
}

func TestEditionLogWarnsWhenCompiledInWithoutAToken(t *testing.T) {
	lines := captureEditionLog(t, productAnalyticsEdition{compiledIn: true, tokenConfigured: false})

	if len(lines) != 1 {
		t.Fatalf("logged %d line(s), want 1: %+v", len(lines), lines)
	}
	if lines[0].Level != slog.LevelWarn.String() {
		t.Errorf("level = %s, want %s", lines[0].Level, slog.LevelWarn)
	}
	if !strings.Contains(lines[0].Msg, "no vendor token") {
		t.Errorf("msg = %q, want it to name the missing token", lines[0].Msg)
	}
	if lines[0].LoggerName != "product-analytics" {
		t.Errorf("logger_name = %q, want product-analytics", lines[0].LoggerName)
	}
}

func TestEditionLogReportsAConfiguredFreeBuild(t *testing.T) {
	lines := captureEditionLog(t, productAnalyticsEdition{compiledIn: true, tokenConfigured: true})

	if len(lines) != 1 {
		t.Fatalf("logged %d line(s), want 1: %+v", len(lines), lines)
	}
	if lines[0].Level != slog.LevelInfo.String() {
		t.Errorf("level = %s, want %s", lines[0].Level, slog.LevelInfo)
	}
	if lines[0].LoggerName != "product-analytics" {
		t.Errorf("logger_name = %q, want product-analytics", lines[0].LoggerName)
	}
}

func TestEditionLogFallsBackToTheDefaultLogger(t *testing.T) {
	logProductAnalyticsEditionFor(productAnalyticsEdition{compiledIn: true, tokenConfigured: true}, nil)
}

func TestBuiltEditionMirrorsTheBuild(t *testing.T) {
	edition := builtProductAnalyticsEdition()

	if edition.compiledIn != buildedition.ProductAnalytics {
		t.Errorf("compiledIn = %v, want %v", edition.compiledIn, buildedition.ProductAnalytics)
	}
	if edition.tokenConfigured != productanalytics.TokenConfigured() {
		t.Errorf("tokenConfigured = %v, want %v", edition.tokenConfigured, productanalytics.TokenConfigured())
	}

	logProductAnalyticsEdition(slog.New(slog.DiscardHandler))
}

func forceProductAnalyticsAvailable(t *testing.T) {
	t.Helper()
	original := productAnalyticsAvailable
	productAnalyticsAvailable = true
	t.Cleanup(func() { productAnalyticsAvailable = original })
}

func forceProductAnalyticsUnavailable(t *testing.T) {
	t.Helper()
	original := productAnalyticsAvailable
	productAnalyticsAvailable = false
	t.Cleanup(func() { productAnalyticsAvailable = original })
}

func assertCollectionStaysOff(t *testing.T, svc *productanalytics.Service, sinkLog *bytes.Buffer) {
	t.Helper()
	ctx := context.Background()
	svc.Enqueue(ctx, productanalytics.SurfaceUI, []productanalytics.RawEvent{{Name: "app:launched"}})
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Contains(sinkLog.String(), "batch accepted") {
		t.Error("an event reached the sink with no consent store configured, so collection is not off")
	}
}

func TestNewProductAnalyticsWithoutADatabaseRecordsNoConsent(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := NewProductAnalytics(context.Background(), "", nil, logger)
	if svc == nil {
		t.Fatal("expected a service when this build carries the collection path, even with no database")
	}
	if svc.ConsentManager() != nil {
		t.Error("no database means consent cannot be recorded, so there must be no manager")
	}
	if !strings.Contains(buf.String(), "no database configured") {
		t.Errorf("expected a warning naming the missing database, got %q", buf.String())
	}
	assertCollectionStaysOff(t, svc, &buf)
}

func TestNewProductAnalyticsWithAnUnopenableStoreRecordsNoConsent(t *testing.T) {
	forceProductAnalyticsAvailable(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := NewProductAnalytics(context.Background(), "mysql://user@host:3306/db", nil, logger)
	if svc == nil {
		t.Fatal("expected a service even when the store cannot be opened")
	}
	if svc.ConsentManager() != nil {
		t.Error("an unopenable store must yield no consent manager")
	}
	if !strings.Contains(buf.String(), "store unavailable") {
		t.Errorf("expected a warning naming the unavailable store, got %q", buf.String())
	}
	assertCollectionStaysOff(t, svc, &buf)
}

func TestNewProductAnalyticsStateFailsWhenTheSchemaCannotBeCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := db.Exec(`CREATE TABLE product_analytics_goose_version (unexpected TEXT)`); execErr != nil {
		t.Fatal(execErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	state, closeStore, err := NewProductAnalyticsState(
		t.Context(), "sqlite:///"+filepath.ToSlash(path), nil)
	if closeStore != nil {
		if closeErr := closeStore(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}
	if err == nil {
		t.Fatal("opening a store whose schema cannot be created returned no error")
	}
	if state != nil {
		t.Error("a usable state was returned for a store with an unusable schema")
	}
	if !strings.Contains(err.Error(), "migrate product analytics store") {
		t.Errorf("error does not name the migration step: %v", err)
	}
}

func TestNewProductAnalyticsStateWithNoURL(t *testing.T) {
	state, closeStore, err := NewProductAnalyticsState(t.Context(), "", nil)
	if err != nil {
		t.Fatalf("empty URL should not be an error: %v", err)
	}
	if state != nil || closeStore != nil {
		t.Error("empty URL should yield no state and no release")
	}
}

func TestProductAnalyticsSinkFallsBackToBatchCounts(t *testing.T) {
	sink, release := NewProductAnalyticsSink(slog.New(slog.DiscardHandler))
	defer release()

	if _, ok := sink.(productanalytics.LoggingSink); !ok {
		t.Errorf("sink is %T, want the batch-counting sink", sink)
	}
}

func TestProductAnalyticsSinkUsesTheVendorWhenLinked(t *testing.T) {
	vendor := productanalytics.NoopSink{}
	original := newVendorSink
	newVendorSink = func(*slog.Logger) (productanalytics.Sink, bool) { return vendor, true }
	defer func() { newVendorSink = original }()

	sink, release := NewProductAnalyticsSink(slog.New(slog.DiscardHandler))
	defer release()

	if sink != productanalytics.Sink(vendor) {
		t.Errorf("sink = %T, want the vendor sink the constructor returned", sink)
	}
}

func TestProductAnalyticsSinkPrefersTheDebugSink(t *testing.T) {
	t.Setenv(productanalytics.EnvDebugSinkFile, filepath.Join(t.TempDir(), "events.ndjson"))

	sink, release := NewProductAnalyticsSink(slog.New(slog.DiscardHandler))
	defer release()

	if _, ok := sink.(*productanalytics.DebugSink); !ok {
		t.Errorf("sink is %T, want the debug sink", sink)
	}
}

func TestProductAnalyticsSinkFallsBackWhenTheDebugToggleIsUnreadable(t *testing.T) {
	t.Setenv(productanalytics.EnvDebugSink, "not-a-boolean")

	sink, release := NewProductAnalyticsSink(slog.New(slog.DiscardHandler))
	defer release()

	if sink == nil {
		t.Fatal("an unreadable debug toggle left no sink at all")
	}
	if _, ok := sink.(productanalytics.LoggingSink); !ok {
		t.Errorf("sink is %T, want the batch-counting sink", sink)
	}
}
