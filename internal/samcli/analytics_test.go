// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package samcli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

func requireProductAnalytics(t *testing.T) {
	t.Helper()
	if !buildedition.ProductAnalytics {
		t.Skip("requires -tags product_analytics")
	}
}

func runAnalytics(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	cmd := newAnalyticsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), err
}

func decodeDecision(t *testing.T, stdout string) analyticsDecisionJSON {
	t.Helper()
	var got analyticsDecisionJSON
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode --format json output %q: %v", stdout, err)
	}
	return got
}

func consentInStore(t *testing.T, databaseURL string) productanalytics.ConsentState {
	t.Helper()
	ctx := context.Background()
	reporter, err := clianalytics.Open(ctx, clianalytics.Options{DatabaseURL: databaseURL})
	if err != nil {
		t.Fatalf("open consent store %q: %v", databaseURL, err)
	}
	defer func() {
		if err := reporter.Close(ctx); err != nil {
			t.Errorf("close reporter: %v", err)
		}
	}()
	return reporter.Consent(ctx)
}

func runAnalyticsIn(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newAnalyticsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func installIDInStore(t *testing.T, databaseURL string) string {
	t.Helper()
	ctx := context.Background()
	if databaseURL == "" {
		databaseURL = localAnalyticsStoreURL(t)
	}
	state, closeStore, err := bootstrap.NewProductAnalyticsState(ctx, databaseURL, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open consent store %q: %v", databaseURL, err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			t.Errorf("close consent store: %v", err)
		}
	}()
	return state.Identity(ctx).Marker
}

func localAnalyticsStoreURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	reporter, err := clianalytics.Open(ctx, clianalytics.Options{})
	if err != nil {
		t.Fatalf("resolve the local analytics store: %v", err)
	}
	defer func() {
		if err := reporter.Close(ctx); err != nil {
			t.Errorf("close reporter: %v", err)
		}
	}()
	return reporter.StoreURL()
}

func TestAnalyticsEnableAndDisableRecordTheDecision(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	run := func(args ...string) analyticsDecisionJSON {
		t.Helper()
		cmd := newAnalyticsCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sam analytics %s: %v", strings.Join(args, " "), err)
		}
		return decodeDecision(t, out.String())
	}

	if got := run("enable", "--format", "json").Consent; got != string(productanalytics.ConsentGranted) {
		t.Errorf("consent after enable = %q, want granted", got)
	}
	if got := consentInStore(t, ""); got != productanalytics.ConsentGranted {
		t.Errorf("consent read back = %q, want granted", got)
	}
	if got := run("disable", "--format", "json").Consent; got != string(productanalytics.ConsentDenied) {
		t.Errorf("consent after disable = %q, want denied", got)
	}
	if got := consentInStore(t, ""); got != productanalytics.ConsentDenied {
		t.Errorf("consent read back = %q, want denied", got)
	}
}

func TestAnalyticsStatusReportsWithoutChangingTheDecision(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	run := func(args ...string) analyticsDecisionJSON {
		t.Helper()
		cmd := newAnalyticsCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("sam analytics %s: %v", strings.Join(args, " "), err)
		}
		return decodeDecision(t, out.String())
	}

	if got := run("status", "--format", "json").Consent; got != string(productanalytics.ConsentUnset) {
		t.Errorf("consent before any decision = %q, want unset", got)
	}
	if got := consentInStore(t, ""); got != productanalytics.ConsentUnset {
		t.Errorf("status recorded a decision: store holds %q, want unset", got)
	}

	run("enable", "--format", "json")
	if got := run("status", "--format", "json").Consent; got != string(productanalytics.ConsentGranted) {
		t.Errorf("consent after enable = %q, want granted", got)
	}
	if got := consentInStore(t, ""); got != productanalytics.ConsentGranted {
		t.Errorf("status changed the recorded decision to %q", got)
	}
}

func TestAnalyticsStatusLeavesTheIdentifierAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)
	t.Setenv("SAM_DATA_DIR", "")

	if _, err := runAnalyticsIn(t, "enable"); err != nil {
		t.Fatal(err)
	}
	before := installIDInStore(t, "")
	if before == "" {
		t.Fatal("precondition: opting in should issue an identifier")
	}

	if _, err := runAnalyticsIn(t, "status"); err != nil {
		t.Fatal(err)
	}

	if after := installIDInStore(t, ""); after != before {
		t.Errorf("identifier after status = %q, want the unchanged %q", after, before)
	}
}

func TestAnalyticsEnableTwiceKeepsTheIdentifier(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	if _, err := runAnalyticsIn(t, "enable"); err != nil {
		t.Fatal(err)
	}
	first := installIDInStore(t, "")
	if first == "" {
		t.Fatal("precondition: opting in should issue an identifier")
	}

	if _, err := runAnalyticsIn(t, "enable"); err != nil {
		t.Fatal(err)
	}

	if second := installIDInStore(t, ""); second != first {
		t.Errorf("identifier after a second enable = %q, want the existing %q", second, first)
	}
}

func TestAnalyticsEnableAfterDisableIssuesANewIdentifier(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	if _, err := runAnalyticsIn(t, "enable"); err != nil {
		t.Fatal(err)
	}
	first := installIDInStore(t, "")
	for _, args := range [][]string{{"disable"}, {"enable"}} {
		if _, err := runAnalyticsIn(t, args...); err != nil {
			t.Fatal(err)
		}
	}

	second := installIDInStore(t, "")
	if second == "" || second == first {
		t.Errorf("identifier after opting out and back in = %q, want a fresh one (was %q)", second, first)
	}
}

func TestAnalyticsStatusFailsOnAnUnreadableStore(t *testing.T) {
	stdout, err := runAnalytics(t, "status", "--database-url", unreadableStore(t))
	if err == nil {
		t.Fatalf("status succeeded against an unusable store, printing:\n%s", stdout)
	}
	if strings.Contains(stdout, "Consent:") {
		t.Errorf("status rendered a decision it could not read:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "cannot read the analytics decision") {
		t.Errorf("error does not say the read failed: %v", err)
	}
}

func TestAnalyticsUnreachableStoreNamesItsRemediation(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{
			name:        "explicit store",
			args:        []string{"status", "--database-url", "postgres://sam@127.0.0.1:1/sam"},
			wantMessage: "--database-url is reachable",
		},
		{
			name:        "local install",
			args:        []string{"status"},
			wantMessage: "Agent Mesh home directory is writable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocker := filepath.Join(t.TempDir(), "not-a-dir.txt")
			if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SAM_HOME", filepath.Join(blocker, "home"))
			t.Setenv("SAM_DATA_DIR", "")

			cmd := newAnalyticsCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an unreachable store to fail the command")
			}
			if !strings.Contains(err.Error(), "cannot reach the analytics store") &&
				!strings.Contains(err.Error(), "cannot read the analytics decision") {
				t.Fatalf("error does not name the store problem: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantMessage) {
				t.Errorf("error carries no remediation for this store choice: %v", err)
			}
		})
	}
}

func TestAnalyticsGrantAloneDoesNotCollect(t *testing.T) {
	if buildedition.ProductAnalytics {
		t.Skip("only meaningful without -tags product_analytics")
	}
	out, err := runAnalytics(t, "enable", "--format", "json")
	if err != nil {
		t.Fatalf("sam analytics enable: %v", err)
	}
	got := decodeDecision(t, out)
	if got.Consent != string(productanalytics.ConsentGranted) {
		t.Fatalf("consent = %q, want granted", got.Consent)
	}
	if got.Available || got.Collecting {
		t.Errorf("an enterprise build carries no collection path, so available/collecting should be false, got %+v", got)
	}
}

func TestAnalyticsTextOutputNamesTheDecision(t *testing.T) {
	out, err := runAnalytics(t, "disable")
	if err != nil {
		t.Fatalf("sam analytics disable: %v", err)
	}
	for _, want := range []string{"Consent:    Denied", "Collecting: No (No information is shared from this installation)"} {
		if !strings.Contains(out, want) {
			t.Errorf("disable output missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyticsStatusOnUnsetInstallWithFeatureOnReportsCollecting(t *testing.T) {
	requireProductAnalytics(t)
	out, err := runAnalytics(t, "status")
	if err != nil {
		t.Fatalf("sam analytics status: %v", err)
	}
	for _, want := range []string{"Collecting: Yes", "Consent:    Not recorded"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyticsRejectsAnUnknownFormat(t *testing.T) {
	if _, err := runAnalytics(t, "enable", "--format", "yaml"); err == nil {
		t.Error("--format yaml was accepted; only text and json are supported")
	}
}

func TestAnalyticsDecisionIsMachineReadableOnStdout(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())
	t.Setenv("SAM_DATA_DIR", "")

	cmd := newAnalyticsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"enable", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sam analytics enable: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("enable wrote to stderr, which breaks piping:\n%s", errOut.String())
	}
	decodeDecision(t, out.String())
}

func TestAnalyticsSubcommandsAreDiscoverable(t *testing.T) {
	want := map[string]bool{"status": false, "enable": false, "disable": false}
	for _, c := range newAnalyticsCmd().Commands() {
		found, expected := want[c.Name()]
		if !expected {
			t.Errorf("sam analytics %s is exposed but not part of the surface", c.Name())
			continue
		}
		if found {
			t.Errorf("sam analytics %s is registered twice", c.Name())
		}
		want[c.Name()] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("sam analytics %s is missing", name)
		}
	}
}

func TestAnalyticsCommandIsRegisteredWhenCompiledIn(t *testing.T) {
	found, _, err := newRootCmd().Find([]string{"analytics"})
	if err != nil {
		t.Fatalf("sam analytics not found in a build that carries the collection path: %v", err)
	}
	if found.Name() != "analytics" {
		t.Errorf("found command %q, want analytics", found.Name())
	}
}

func TestTrackedCommandsUseCatalogEvents(t *testing.T) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if event := clianalytics.EventFor(cmd); event != "" && !productanalytics.IsCatalogEvent(event) {
			t.Errorf("%q is tracked as %q, which is not in the product analytics catalog", cmd.CommandPath(), event)
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd())
}

func TestTheCommandTreeIsTracked(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{{"run"}, {"config", "apply"}, {"task", "send"}, {"api"}, {"eval"}} {
		found, _, err := root.Find(path)
		if err != nil {
			t.Fatalf("sam %v not found in the command tree: %v", path, err)
		}
		if got := clianalytics.EventFor(found); got != productanalytics.EventCLICommandRun {
			t.Errorf("sam %v tracked as %q, want %q", path, got, productanalytics.EventCLICommandRun)
		}
	}
}

func TestAnalyticsDescriptionWithCollectionEnabledDistinguishesNoConsentFromFlagOff(t *testing.T) {
	requireProductAnalytics(t)

	stdout, err := runAnalytics(t, "disable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "No (No information is shared from this installation)") {
		t.Errorf("a withdrawal in a build with the collection path should still collect nothing, got %q", stdout)
	}

	stdout, err = runAnalytics(t, "enable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Yes (Shared from this installation)") {
		t.Errorf("granted consent in a build with the collection path should collect, got %q", stdout)
	}
}

func TestAnalyticsJSONSeparatesFlagFromConsent(t *testing.T) {
	requireProductAnalytics(t)

	stdout, err := runAnalytics(t, "enable", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeDecision(t, stdout)
	if !got.Available {
		t.Error("available = false in a build with the collection path")
	}
	if !got.Collecting {
		t.Error("collecting = false with the path available and consent granted")
	}

	stdout, err = runAnalytics(t, "disable", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	got = decodeDecision(t, stdout)
	if !got.Available {
		t.Error("available should track the build, not the decision")
	}
	if got.Collecting {
		t.Error("collecting = true after opting out")
	}
}

func TestAnalyticsNamesAnExplicitStoreOnly(t *testing.T) {
	stdout, err := runAnalytics(t, "disable")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "Store:") {
		t.Errorf("text output names the store for a local install, which the user cannot choose:\n%s", stdout)
	}

	elsewhere := "sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "elsewhere.db"))
	stdout, err = runAnalytics(t, "disable", "--database-url", elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Store:      "+elsewhere) {
		t.Errorf("text output does not name the store it was pointed at:\n%s", stdout)
	}
}

func TestAnalyticsNamesAStoreFromTheEnvironment(t *testing.T) {
	envStore := "sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "from-env.db"))
	t.Setenv("SAM_ANALYTICS_DATABASE_URL", envStore)

	stdout, err := runAnalytics(t, "disable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Store:      "+envStore) {
		t.Errorf("text output does not name the store the environment set:\n%s", stdout)
	}
}

func TestAnalyticsJSONAlwaysReportsTheStore(t *testing.T) {
	stdout, err := runAnalytics(t, "enable", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeDecision(t, stdout).Store; !strings.HasPrefix(got, "sqlite://") {
		t.Errorf("store = %q, want the resolved sqlite URL", got)
	}
}

func TestAnalyticsTextSeparatesAGrantFromTheFlag(t *testing.T) {
	if buildedition.ProductAnalytics {
		t.Skip("only meaningful without -tags product_analytics")
	}
	stdout, err := runAnalytics(t, "enable")
	if err != nil {
		t.Fatal(err)
	}
	want := "Collecting: No (Product analytics is not available in this build)"
	if !strings.Contains(stdout, want) {
		t.Errorf("output missing %q:\n%s", want, stdout)
	}
	if !strings.Contains(stdout, "Consent:    Granted") {
		t.Errorf("output does not report the grant it stored:\n%s", stdout)
	}
}

func TestAnalyticsHonorsAnExplicitStore(t *testing.T) {
	elsewhere := "sqlite:///" + t.TempDir() + "/elsewhere.db"

	stdout, err := runAnalytics(t, "enable", "--database-url", elsewhere, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	got := decodeDecision(t, stdout)
	if got.Store != elsewhere {
		t.Errorf("store = %q, want the explicit %q", got.Store, elsewhere)
	}
	if got.Consent != string(productanalytics.ConsentGranted) {
		t.Errorf("consent = %q, want granted", got.Consent)
	}
}

func unreadableStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unreadable.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	stmts := []string{
		`CREATE TABLE product_analytics_goose_version (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL,
			tstamp TIMESTAMP DEFAULT (datetime('now'))
		)`,
		`INSERT INTO product_analytics_goose_version (version_id, is_applied) VALUES (0, 1)`,
		`INSERT INTO product_analytics_goose_version (version_id, is_applied) VALUES (1, 1)`,
		`INSERT INTO product_analytics_goose_version (version_id, is_applied) VALUES (2, 1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return "sqlite:///" + filepath.ToSlash(path)
}

func TestAnalyticsFailsOnAnUnwritableStore(t *testing.T) {
	url := unreadableStore(t)

	stdout, err := runAnalytics(t, "disable", "--database-url", url)
	if err == nil {
		t.Fatalf("disable succeeded against an unusable store, printing:\n%s", stdout)
	}
	if strings.Contains(stdout, string(productanalytics.ConsentDenied)) {
		t.Errorf("disable rendered a decision it could not store:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "cannot record the analytics decision") {
		t.Errorf("error does not say the write failed: %v", err)
	}
	if !strings.Contains(err.Error(), "--database-url") {
		t.Errorf("error carries no remediation for an explicit store: %v", err)
	}
}

func TestAnalyticsJSONFailsOnAnUnwritableStore(t *testing.T) {
	stdout, err := runAnalytics(t, "disable", "--database-url", unreadableStore(t), "--format", "json")
	if err == nil {
		t.Fatalf("disable --format json succeeded against an unusable store:\n%s", stdout)
	}
	if strings.Contains(stdout, `"consent"`) {
		t.Errorf("disable emitted a JSON decision it could not store:\n%s", stdout)
	}
}

func TestAnalyticsStoreComesFromTheEnvironment(t *testing.T) {
	envStore := "sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "from-env.db"))
	t.Setenv("SAM_ANALYTICS_DATABASE_URL", envStore)

	stdout, err := runAnalytics(t, "enable", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeDecision(t, stdout).Store; got != envStore {
		t.Errorf("store = %q, want the environment's %q", got, envStore)
	}
}

func TestAnalyticsStoreFlagBeatsTheEnvironment(t *testing.T) {
	flagStore := "sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "from-flag.db"))
	t.Setenv("SAM_ANALYTICS_DATABASE_URL", "sqlite:///"+filepath.ToSlash(filepath.Join(t.TempDir(), "from-env.db")))

	stdout, err := runAnalytics(t, "enable", "--database-url", flagStore, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeDecision(t, stdout).Store; got != flagStore {
		t.Errorf("store = %q, want the flag's %q", got, flagStore)
	}
}

type countingSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *countingSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *countingSink) all() []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productanalytics.Event(nil), s.events...)
}

func TestExecuteTreeReportsTheRun(t *testing.T) {
	requireProductAnalytics(t)
	cases := []struct {
		name        string
		runErr      error
		wantOutcome string
	}{
		{"successful run", nil, productanalytics.OutcomeSuccess},
		{"failed run", errors.New("boom"), productanalytics.OutcomeError},
		{"plan with changes pending", errChangesPending, productanalytics.OutcomeSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("SAM_HOME", home)
			t.Setenv("SAM_DATA_DIR", "")

			grantConsentAt(t, home)

			sink := &countingSink{}
			root := &cobra.Command{Use: "sam", SilenceUsage: true, SilenceErrors: true}
			tracked := clianalytics.TrackCommand(&cobra.Command{
				Use:  "run",
				RunE: func(*cobra.Command, []string) error { return tc.runErr },
			})
			root.AddCommand(tracked)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"run"})

			err := executeTree(root, clianalytics.Options{Sink: sink})
			if !errors.Is(err, tc.runErr) {
				t.Fatalf("executeTree error = %v, want %v", err, tc.runErr)
			}

			events := sink.all()
			if len(events) != 1 {
				t.Fatalf("delivered %d events, want 1", len(events))
			}
			if got := events[0].Name; got != productanalytics.EventCLICommandRun {
				t.Errorf("event = %q, want %q", got, productanalytics.EventCLICommandRun)
			}
			if got := events[0].Properties[productanalytics.PropCommand]; got != "sam run" {
				t.Errorf("command = %v, want %q (the executed leaf, not the root)", got, "sam run")
			}
			if got := events[0].Properties[productanalytics.PropOutcome]; got != tc.wantOutcome {
				t.Errorf("outcome = %v, want %q", got, tc.wantOutcome)
			}
		})
	}
}

func grantConsentAt(t *testing.T, home string) {
	t.Helper()
	cmd := newAnalyticsCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"enable"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("granting consent in %s: %v", home, err)
	}
}

func TestEveryCommandPathIsAStaticName(t *testing.T) {
	const maxCommandLen = 64

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		path := cmd.CommandPath()
		if len(path) > maxCommandLen {
			t.Errorf("command path %q is %d bytes; the catalog drops it over %d, so the run would report no command at all", path, len(path), maxCommandLen)
		}
		for _, r := range path {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != ' ' {
				t.Errorf("command path %q contains %q; a path is joined command names, so anything else means a name came from somewhere other than a literal", path, r)
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(newRootCmd())
}
