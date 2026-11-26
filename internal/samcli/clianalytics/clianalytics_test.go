// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func TestMain(m *testing.M) {
	productAnalyticsAvailable = true
	os.Exit(runWithAnEmptySAMHome(m))
}

func runWithAnEmptySAMHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "clianalytics-sam-home-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	if err := os.Setenv("SAM_HOME", home); err != nil {
		panic(err)
	}
	return m.Run()
}

func forceProductAnalyticsUnavailable(t *testing.T) {
	t.Helper()
	productAnalyticsAvailable = false
	t.Cleanup(func() { productAnalyticsAvailable = true })
}

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *captureSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *captureSink) all() []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productanalytics.Event(nil), s.events...)
}

func testOptions(t *testing.T, sink productanalytics.Sink) Options {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	dir := t.TempDir()
	return Options{
		DatabaseURL: databaseURL(dir),
		DataDir:     dir,
		Sink:        sink,
	}
}

func openReporter(t *testing.T, opts Options) *Reporter {
	t.Helper()
	r, err := Open(t.Context(), opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func TestConsentStartsUnsetAndRoundTrips(t *testing.T) {
	ctx := t.Context()
	opts := testOptions(t, &captureSink{})
	r := openReporter(t, opts)

	if got := r.Consent(ctx); got != productanalytics.ConsentUnset {
		t.Fatalf("fresh install consent = %q, want unset", got)
	}
	for _, want := range []productanalytics.ConsentState{productanalytics.ConsentGranted, productanalytics.ConsentDenied} {
		if err := r.SetConsent(ctx, want); err != nil {
			t.Fatalf("SetConsent(%q): %v", want, err)
		}
		if got := r.Consent(ctx); got != want {
			t.Errorf("consent = %q, want %q", got, want)
		}
	}
}

func TestConsentPersistsAcrossProcesses(t *testing.T) {
	ctx := t.Context()
	opts := testOptions(t, &captureSink{})

	first := openReporter(t, opts)
	if err := first.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := openReporter(t, opts)
	if got := second.Consent(ctx); got != productanalytics.ConsentGranted {
		t.Errorf("consent after reopen = %q, want granted", got)
	}
}

func TestTrackRequiresConsent(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name    string
		consent productanalytics.ConsentState
		want    int
	}{
		{"unset collects", productanalytics.ConsentUnset, 1},
		{"denied collects nothing", productanalytics.ConsentDenied, 0},
		{"granted collects", productanalytics.ConsentGranted, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &captureSink{}
			r := openReporter(t, testOptions(t, sink))
			if tc.consent != productanalytics.ConsentUnset {
				if err := r.SetConsent(ctx, tc.consent); err != nil {
					t.Fatalf("SetConsent: %v", err)
				}
			}
			r.Track(ctx, productanalytics.EventCLICommandRun, map[string]any{
				productanalytics.PropCommand: "sam run",
				productanalytics.PropOutcome: productanalytics.OutcomeSuccess,
			})
			if err := r.Close(ctx); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if got := len(sink.all()); got != tc.want {
				t.Errorf("delivered %d events, want %d", got, tc.want)
			}
		})
	}
}

func TestTrackRequiresTheFeatureFlag(t *testing.T) {
	forceProductAnalyticsUnavailable(t)
	ctx := t.Context()
	sink := &captureSink{}
	r := openReporter(t, testOptions(t, sink))
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if r.Available() {
		t.Error("Available() is true in a build with no collection path")
	}
	r.Track(ctx, productanalytics.EventCLICommandRun, nil)
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(sink.all()); got != 0 {
		t.Errorf("delivered %d events with no collection path, want 0", got)
	}
}

func TestCLIEventsAreStampedWithTheCLISurface(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	r := openReporter(t, testOptions(t, sink))
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	r.Track(ctx, productanalytics.EventCLICommandRun, map[string]any{
		productanalytics.PropInterface: productanalytics.SurfaceUI,
		productanalytics.PropCommand:   "sam run",
	})
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceCLI {
		t.Errorf("interface = %v, want %q (a caller must not be able to claim another surface)", got, productanalytics.SurfaceCLI)
	}
	if got := events[0].Properties[productanalytics.PropCommand]; got != "sam run" {
		t.Errorf("command = %v, want %q", got, "sam run")
	}
	if events[0].DistinctID == "" {
		t.Error("granted consent should carry the anonymous install identifier")
	}
}

func TestCLIEventsCarryTheTimeEnvelope(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	r := openReporter(t, testOptions(t, sink))
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	before := time.Now().UTC().Truncate(time.Millisecond)
	r.Track(ctx, productanalytics.EventCLICommandRun, map[string]any{productanalytics.PropCommand: "sam run"})
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.OccurredAt.Before(before) {
		t.Errorf("occurredAt = %v, want an instant at or after %v", ev.OccurredAt, before)
	}
	if ev.ReceivedAt.IsZero() {
		t.Error("receivedAt is unset")
	}
	if ev.EventID == "" {
		t.Error("eventId is unset, so the vendor cannot deduplicate a resend")
	}
	if ev.Seq == 0 {
		t.Error("seq is unset, so a dropped CLI event is undetectable")
	}
}

func TestCLISessionIsStableAcrossReporters(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)

	for i := 0; i < 2; i++ {
		r, err := Open(ctx, opts)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
			t.Fatalf("SetConsent: %v", err)
		}
		r.Track(ctx, productanalytics.EventCLICommandRun, nil)
		if err := r.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	events := sink.all()
	if len(events) != 2 {
		t.Fatalf("delivered %d events, want 2", len(events))
	}
	if events[0].SessionID == "" {
		t.Fatal("sessionId is unset")
	}
	if events[0].SessionID != events[1].SessionID {
		t.Errorf("sessionId changed between reporters: %q then %q", events[0].SessionID, events[1].SessionID)
	}
}

func TestWithdrawingConsentDiscardsTheIdentifier(t *testing.T) {
	sink := &captureSink{}
	opts := testOptions(t, sink)

	first := trackOnePeriod(t, opts, sink)
	withdrawConsent(t, opts)
	second := trackOnePeriod(t, opts, sink)

	if first == "" || second == "" {
		t.Fatalf("missing install identifiers: %q then %q", first, second)
	}
	if first == second {
		t.Error("re-granting consent reused the previous identifier, so the two periods are linkable")
	}
}

func trackOnePeriod(t *testing.T, opts Options, sink *captureSink) string {
	t.Helper()
	ctx := t.Context()
	r, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent(granted): %v", err)
	}
	before := len(sink.all())
	r.Track(ctx, productanalytics.EventCLICommandRun, nil)
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events := sink.all()
	if len(events) != before+1 {
		t.Fatalf("delivered %d events, want %d", len(events), before+1)
	}
	return events[len(events)-1].DistinctID
}

func withdrawConsent(t *testing.T, opts Options) {
	t.Helper()
	ctx := t.Context()
	r, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.SetConsent(ctx, productanalytics.ConsentDenied); err != nil {
		t.Fatalf("SetConsent(denied): %v", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDisableReportsTheWithdrawalNotice(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)

	grant := openReporter(t, opts)
	if err := grant.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent(granted): %v", err)
	}

	disable := openReporter(t, opts)
	if _, err := disable.ConsentWithError(ctx); err != nil {
		t.Fatalf("ConsentWithError: %v", err)
	}
	if err := disable.SetConsent(ctx, productanalytics.ConsentDenied); err != nil {
		t.Fatalf("SetConsent(denied): %v", err)
	}
	if err := disable.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, ev := range sink.all() {
		if ev.Name != productanalytics.EventConsentChanged {
			continue
		}
		if got, want := ev.Properties[productanalytics.PropConsentPrevious], productanalytics.ConsentStateGranted; got != want {
			t.Errorf("consentPrevious = %v, want %q", got, want)
		}
		if got, want := ev.Properties[productanalytics.PropConsentNew], productanalytics.ConsentStateWithdrawn; got != want {
			t.Errorf("consentNew = %v, want %q", got, want)
		}
		return
	}
	t.Fatalf("no consent:changed event reached the sink: %+v", sink.all())
}

func TestNoticeShowsOnceWhileConsentIsUnset(t *testing.T) {
	ctx := t.Context()
	opts := testOptions(t, &captureSink{})
	r := openReporter(t, opts)

	var first, second bytes.Buffer
	r.ShowNoticeOnce(ctx, &first)
	r.ShowNoticeOnce(ctx, &second)

	if !strings.Contains(first.String(), "sam analytics enable") {
		t.Errorf("notice does not tell the user how to opt in:\n%s", first.String())
	}
	if second.Len() != 0 {
		t.Errorf("notice repeated on the second command:\n%s", second.String())
	}
	if _, err := os.Stat(filepath.Join(opts.DataDir, noticeMarkerFile)); err != nil {
		t.Errorf("notice marker not written: %v", err)
	}
}

func requireNoMarker(t *testing.T, dataDir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dataDir, noticeMarkerFile)); !os.IsNotExist(err) {
		t.Errorf("notice marker was consumed while the notice was suppressed (stat err = %v)", err)
	}
}

func TestNoticeIsSilentOnceAnswered(t *testing.T) {
	ctx := t.Context()
	for _, state := range []productanalytics.ConsentState{productanalytics.ConsentGranted, productanalytics.ConsentDenied} {
		t.Run(string(state), func(t *testing.T) {
			opts := testOptions(t, &captureSink{})
			r := openReporter(t, opts)
			if err := r.SetConsent(ctx, state); err != nil {
				t.Fatalf("SetConsent: %v", err)
			}
			var out bytes.Buffer
			r.ShowNoticeOnce(ctx, &out)
			if out.Len() != 0 {
				t.Errorf("notice shown after the user answered %q:\n%s", state, out.String())
			}
			requireNoMarker(t, opts.DataDir)
		})
	}
}

func TestNoticeIsSilentWhenCollectionIsOff(t *testing.T) {
	forceProductAnalyticsUnavailable(t)
	opts := testOptions(t, &captureSink{})
	r := openReporter(t, opts)
	var out bytes.Buffer
	r.ShowNoticeOnce(t.Context(), &out)
	if out.Len() != 0 {
		t.Errorf("notice shown in a build that collects nothing:\n%s", out.String())
	}
	requireNoMarker(t, opts.DataDir)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stderr is closed") }

func TestNoticeMarkerIsReleasedWhenThePrintFails(t *testing.T) {
	opts := testOptions(t, &captureSink{})
	r := openReporter(t, opts)

	r.ShowNoticeOnce(t.Context(), failingWriter{})
	requireNoMarker(t, opts.DataDir)

	var out bytes.Buffer
	r.ShowNoticeOnce(t.Context(), &out)
	if !strings.Contains(out.String(), "sam analytics enable") {
		t.Errorf("the notice never recovered from a failed print:\n%s", out.String())
	}
}

func TestNoticeShowsOnceUnderConcurrentCommands(t *testing.T) {
	opts := testOptions(t, &captureSink{})

	const commands = 8
	reporters := make([]*Reporter, commands)
	for i := range reporters {
		reporters[i] = openReporter(t, opts)
	}

	outs := make([]bytes.Buffer, commands)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range commands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reporters[i].ShowNoticeOnce(t.Context(), &outs[i])
		}()
	}
	close(start)
	wg.Wait()

	printed := 0
	for i := range outs {
		if outs[i].Len() != 0 {
			printed++
		}
	}
	if printed != 1 {
		t.Errorf("%d of %d concurrent commands printed the notice, want exactly 1", printed, commands)
	}
}

func TestTrackCommandAnnotatesAndReports(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)

	setup := openReporter(t, opts)
	if err := setup.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if err := setup.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	root := &cobra.Command{Use: "sam"}
	child := TrackCommand(&cobra.Command{Use: "run"})
	root.AddCommand(child)
	if got := EventFor(child); got != productanalytics.EventCLICommandRun {
		t.Fatalf("EventFor = %q, want %q", got, productanalytics.EventCLICommandRun)
	}

	Report(ctx, child, nil, opts)

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	if got := events[0].Name; got != productanalytics.EventCLICommandRun {
		t.Errorf("event = %q, want %q", got, productanalytics.EventCLICommandRun)
	}
	if got := events[0].Properties[productanalytics.PropCommand]; got != "sam run" {
		t.Errorf("command = %v, want %q", got, "sam run")
	}
	if got := events[0].Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeSuccess {
		t.Errorf("outcome = %v, want %q", got, productanalytics.OutcomeSuccess)
	}
}

func TestReportRecordsAFailedRun(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)
	grantConsent(t, opts)

	cmd := TrackCommand(&cobra.Command{Use: "run"})
	Report(ctx, cmd, context.Canceled, opts)

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	if got := events[0].Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeError {
		t.Errorf("outcome = %v, want %q", got, productanalytics.OutcomeError)
	}
	if got := events[0].Properties[productanalytics.PropCommand]; got != "run" {
		t.Errorf("command = %v, want the command tree's own path", got)
	}
}

func TestCommandJourneyIsOneSessionWithPerCommandClocks(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)
	grantConsent(t, opts)

	root := &cobra.Command{Use: "sam"}
	before := time.Now().UTC().Truncate(time.Millisecond)
	for _, use := range []string{"init", "run"} {
		cmd := TrackCommand(&cobra.Command{Use: use})
		cmd.SetErr(io.Discard)
		root.AddCommand(cmd)
		Report(ctx, cmd, nil, opts)
	}
	after := time.Now().UTC()

	events := sink.all()
	if len(events) != 2 {
		t.Fatalf("delivered %d events, want one per tracked command", len(events))
	}
	for i, want := range []string{"sam init", "sam run"} {
		if got := events[i].Properties[productanalytics.PropCommand]; got != want {
			t.Errorf("event %d command = %v, want %q", i, got, want)
		}
	}

	if events[0].SessionID == "" {
		t.Fatal("sessionId is unset, so two commands of one run cannot be joined")
	}
	if events[0].SessionID != events[1].SessionID {
		t.Errorf("sessionId changed between commands: %q then %q", events[0].SessionID, events[1].SessionID)
	}

	for i, ev := range events {
		if ev.OccurredAt.Before(before) || ev.OccurredAt.After(after) {
			t.Errorf("event %d occurredAt = %v, want the command's own clock within [%v, %v]", i, ev.OccurredAt, before, after)
		}
		if !ev.OccurredAt.Before(ev.ReceivedAt) {
			t.Errorf("event %d occurredAt %v is not before receivedAt %v, so the command stamped no clock of its own", i, ev.OccurredAt, ev.ReceivedAt)
		}
	}
	if events[1].OccurredAt.Before(events[0].OccurredAt) {
		t.Errorf("the second command's clock %v precedes the first's %v", events[1].OccurredAt, events[0].OccurredAt)
	}
}

func TestReportIgnoresUntrackedCommands(t *testing.T) {
	sink := &captureSink{}
	opts := testOptions(t, sink)
	grantConsent(t, opts)

	Report(t.Context(), &cobra.Command{Use: "run"}, nil, opts)
	if got := len(sink.all()); got != 0 {
		t.Errorf("delivered %d events for an untracked command, want 0", got)
	}
}

func TestReportSurvivesAnUnreachableStore(t *testing.T) {
	opts := Options{
		DatabaseURL: "mysql://not-a-supported-store",
		DataDir:     t.TempDir(),
		Sink:        &captureSink{},
	}
	if _, err := Open(t.Context(), opts); err == nil {
		t.Fatal("Open accepted an unsupported store URL, so this test proves nothing")
	}
	cmd := TrackCommand(&cobra.Command{Use: "run"})
	Report(t.Context(), cmd, nil, opts)
}

func TestReportIgnoresAHelpOnlyRun(t *testing.T) {
	sink := &captureSink{}
	opts := testOptions(t, sink)
	grantConsent(t, opts)

	cmd := TrackCommand(&cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--help"})

	executed, err := cmd.ExecuteC()
	if err != nil {
		t.Fatalf("--help should not error: %v", err)
	}
	Report(t.Context(), executed, err, opts)

	if got := len(sink.all()); got != 0 {
		t.Errorf("delivered %d events for a help-only run, want 0", got)
	}
}

func TestReportStillRecordsARealRunOfTheSameCommand(t *testing.T) {
	sink := &captureSink{}
	opts := testOptions(t, sink)
	grantConsent(t, opts)

	cmd := TrackCommand(&cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)

	executed, err := cmd.ExecuteC()
	if err != nil {
		t.Fatal(err)
	}
	Report(t.Context(), executed, err, opts)

	if got := len(sink.all()); got != 1 {
		t.Fatalf("delivered %d events for a real run, want 1", got)
	}
}

func grantConsent(t *testing.T, opts Options) {
	t.Helper()
	ctx := t.Context()
	r, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func denyConsent(t *testing.T, opts Options) {
	t.Helper()
	ctx := t.Context()
	r, err := Open(ctx, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := r.SetConsent(ctx, productanalytics.ConsentDenied); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestResolveDataDirWorksWithoutAHomeDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", dir)
	t.Setenv("SAM_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveDataDir()
	if err != nil {
		t.Fatalf("ResolveDataDir with no home: %v", err)
	}
	if got != dir {
		t.Errorf("data dir = %q, want the SAM_DATA_DIR value %q", got, dir)
	}
}

func TestResolveDataDirPrefersTheSettingsFileOverTheEnvironment(t *testing.T) {
	root := chdirToTempWithSettings(t)
	t.Setenv("SAM_DATA_DIR", t.TempDir())

	got, err := ResolveDataDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".sam", "data")
	if got != want {
		t.Errorf("data dir = %q, want the settings directory %q", got, want)
	}
}

func chdirToTempWithSettings(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sam"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sam", "settings.yaml"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestStoreURLMasksCredentials(t *testing.T) {
	cases := []struct {
		name, raw, mustNotContain, mustContain string
	}{
		{"postgres password", "postgres://sam:hunter2@db.example.com:5432/sam", "hunter2", "db.example.com"},
		{"sqlite is left alone", "sqlite:////var/lib/sam/sam.db", "", "sqlite:////var/lib/sam/sam.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (&Reporter{storeURL: tc.raw}).StoreURL()
			if tc.mustNotContain != "" && strings.Contains(got, tc.mustNotContain) {
				t.Errorf("StoreURL() = %q, must not contain %q", got, tc.mustNotContain)
			}
			if !strings.Contains(got, tc.mustContain) {
				t.Errorf("StoreURL() = %q, must contain %q", got, tc.mustContain)
			}
		})
	}
}

func TestOpenWithAnExplicitStoreNeedsNoHomeDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", "")
	t.Setenv("SAM_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	for _, key := range []string{"HOME", "SAM_HOME", "SAM_DATA_DIR", "XDG_CONFIG_HOME"} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(t.TempDir())

	if _, err := ResolveDataDir(); err == nil {
		t.Skip("this environment can still resolve a data dir, so the test proves nothing")
	}

	r, err := Open(t.Context(), Options{
		DatabaseURL: databaseURL(dir),
		Sink:        &captureSink{},
	})
	if err != nil {
		t.Fatalf("Open with an explicit store and no home: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })

	if got := r.Consent(t.Context()); got != productanalytics.ConsentUnset {
		t.Errorf("consent = %q, want unset", got)
	}

	var out bytes.Buffer
	r.ShowNoticeOnce(t.Context(), &out)
	if out.Len() != 0 {
		t.Errorf("notice printed with no place to record it:\n%s", out.String())
	}
	if _, statErr := os.Stat(noticeMarkerFile); !os.IsNotExist(statErr) {
		t.Errorf("a notice marker was written into the working directory (stat err = %v)", statErr)
	}
}

func TestBareRootReportsNothing(t *testing.T) {
	ctx := t.Context()
	sink := &captureSink{}
	opts := testOptions(t, sink)

	setup := openReporter(t, opts)
	if err := setup.SetConsent(ctx, productanalytics.ConsentGranted); err != nil {
		t.Fatalf("SetConsent: %v", err)
	}
	if err := setup.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	root := TrackCommand(&cobra.Command{Use: "sam"})
	child := &cobra.Command{Use: "run"}
	root.AddCommand(child)

	Report(ctx, root, nil, opts)
	if got := sink.all(); len(got) != 0 {
		t.Errorf("a bare root reported %d events, want none", len(got))
	}

	Report(ctx, child, nil, opts)
	if got := sink.all(); len(got) != 1 {
		t.Errorf("a real subcommand reported %d events, want one", len(got))
	}
}
