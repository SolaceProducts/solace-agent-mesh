// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

const reportTemplateHTML = `<!doctype html>
<html><body>
<h1>@@report_title@@</h1>
<p>By @@prepared_by@@</p>
<table>
«««template_liquid: data="sales_data.json"
{% for row in items %}<tr><td>{{ row.month }}</td><td>{{ row.revenue }}</td></tr>
{% endfor %}»»»
</table>
<script>var t = "${i}";</script>
</body></html>`

const reportTemplateSidecar = `template:
  file: report.html
  output_filename: quarterly_report.html
  mime_type: text/html
  description: Quarterly sales report
substitutions:
  report_title:
    description: H1 title
    default: "Quarterly Report"
  prepared_by:
    description: Author attribution
    required: true
data_inputs:
  sales_rows:
    description: One row per month
    artifact: sales_data.json
    mime_type: application/json
    required: true
    schema:
      type: array
      items:
        type: object
        required: [month, revenue]
        properties:
          month: { type: string }
          revenue: { type: number }
`

const salesDataJSON = `[{"month":"Jan","revenue":100},{"month":"Feb","revenue":250}]`

func writeReportSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	samt, err := skillruntime.BuildTemplateZip("report.html", reportTemplateHTML, reportTemplateSidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "report.html.samt"), samt, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type integScopedStore struct {
	svc                  *artifact.ScopedService
	agent, user, session string
	logger               *slog.Logger
}

func (s *integScopedStore) Load(ctx context.Context, filename string, version *int) ([]byte, string, int, error) {
	loc := s.svc.Resolve(s.agent, s.user, s.session, filename)
	if version == nil {
		versions, err := s.svc.Inner.ListVersions(ctx, loc)
		if err != nil {
			return nil, "", 0, err
		}
		if len(versions) == 0 {
			return nil, "", 0, nil
		}
		version = &versions[len(versions)-1]
	}
	content, mimeType, err := s.svc.Inner.Load(ctx, loc, version)
	if err != nil || content == nil {
		return nil, "", 0, err
	}
	return content, mimeType, *version, nil
}

func (s *integScopedStore) Save(ctx context.Context, filename string, content []byte, mimeType, description string) (int, error) {
	res, err := artifact.SaveWithMetadata(ctx, s.svc, s.agent, s.user, s.session, filename, content, mimeType,
		artifact.MetadataOpts{Description: description}, s.logger)
	if err != nil {
		return 0, err
	}
	return res.DataVersion, nil
}

var serveMarkers = []string{"[Embed Error", "[Template Error", "[Truncated", "[Error: Max embed depth"}

func hasServeMarker(s string) bool {
	for _, m := range serveMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func newRealResolve(svc *artifact.ScopedService, userID, sessionID string, logger *slog.Logger) skillruntime.EmbedResolveChecker {
	check := newSkillEmbedResolveCheck(logger)
	return func(ctx context.Context, candidate string) []string {
		resolved, err := check(ctx, candidate, svc, "", userID, sessionID)
		if err != nil {
			return []string{err.Error()}
		}
		if hasServeMarker(resolved) {
			return []string{"resolution produced failure marker"}
		}
		return nil
	}
}

func TestInstantiateSkillAsset_Integration_RealResolveAndSave(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "default"}
	const userID, sessionID = "u1", "s1"
	store := &integScopedStore{svc: svc, agent: "", user: userID, session: sessionID, logger: logger}

	if _, err := store.Save(ctx, "sales_data.json", []byte(salesDataJSON), "application/json", ""); err != nil {
		t.Fatalf("seed data artifact: %v", err)
	}

	sc := skillruntime.SkillContext{Name: "quarterly_report", LocalPath: writeReportSkill(t)}

	res, findings, err := skillruntime.InstantiateSkillAsset(ctx, sc, "report.html.samt", "",
		map[string]string{"prepared_by": "Ed"},
		skillruntime.InstantiateDeps{Store: store, Resolve: newRealResolve(svc, userID, sessionID, logger)})
	if err != nil {
		t.Fatalf("instantiate errored: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean template produced findings: %+v", findings)
	}
	if res.Filename != "quarterly_report.html" {
		t.Fatalf("output filename = %q, want quarterly_report.html", res.Filename)
	}

	saved, _, _, err := store.Load(ctx, "quarterly_report.html", nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	savedStr := string(saved)
	if !strings.Contains(savedStr, "<h1>Quarterly Report</h1>") || !strings.Contains(savedStr, "By Ed") {
		t.Errorf("substitution not applied in saved artifact:\n%s", savedStr)
	}
	if !strings.Contains(savedStr, "template_liquid") {
		t.Errorf("Liquid block was resolved at instantiate time (should be live):\n%s", savedStr)
	}
	if !strings.Contains(savedStr, `"${i}"`) {
		t.Errorf("JS template literal was clobbered by substitution:\n%s", savedStr)
	}
	if strings.Contains(savedStr, "@@") {
		t.Errorf("residual @@ token left in saved artifact:\n%s", savedStr)
	}
	if !strings.Contains(savedStr, `data="sales_data.json:0"`) {
		t.Errorf("data reference not pinned to the validated version:\n%s", savedStr)
	}
}

func TestInstantiateSkillAsset_Integration_PinnedDataSurvivesLaterWrites(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "default"}
	const userID, sessionID = "u1", "s1"
	store := &integScopedStore{svc: svc, agent: "", user: userID, session: sessionID, logger: logger}
	sc := skillruntime.SkillContext{Name: "quarterly_report", LocalPath: writeReportSkill(t)}
	resolve := newRealResolve(svc, userID, sessionID, logger)

	instantiate := func(source, output string) string {
		t.Helper()
		res, findings, err := skillruntime.InstantiateSkillAsset(ctx, sc, "report.html.samt", output,
			map[string]string{"prepared_by": "Ed"},
			skillruntime.InstantiateDeps{Store: store, Resolve: resolve, DataInputs: map[string]string{"sales_rows": source}})
		if err != nil || len(findings) > 0 {
			t.Fatalf("instantiate from %s: err=%v findings=%+v", source, err, findings)
		}
		saved, _, _, err := store.Load(ctx, res.Filename, nil)
		if err != nil {
			t.Fatalf("load %s: %v", res.Filename, err)
		}
		return string(saved)
	}

	render := func(doc string) string {
		t.Helper()
		check := newSkillEmbedResolveCheck(logger)
		resolved, err := check(ctx, doc, svc, "", userID, sessionID)
		if err != nil {
			t.Fatalf("serve-resolve: %v", err)
		}
		if hasServeMarker(resolved) {
			t.Fatalf("serve-resolve produced a failure marker:\n%s", resolved)
		}
		return resolved
	}

	if _, err := store.Save(ctx, "daily.json", []byte(`[{"month":"Mon","revenue":1}]`), "application/json", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, "monthly.json", []byte(`[{"month":"Jun","revenue":999}]`), "application/json", ""); err != nil {
		t.Fatal(err)
	}

	dailyDoc := instantiate("daily.json", "daily_report.html")
	monthlyDoc := instantiate("monthly.json", "monthly_report.html")

	dailyOut, monthlyOut := render(dailyDoc), render(monthlyDoc)
	if !strings.Contains(dailyOut, "Mon") || strings.Contains(dailyOut, "Jun") {
		t.Errorf("daily report rendered the wrong data:\n%s", dailyOut)
	}
	if !strings.Contains(monthlyOut, "Jun") || strings.Contains(monthlyOut, "Mon") {
		t.Errorf("monthly report rendered the wrong data (aliasing):\n%s", monthlyOut)
	}

	if _, err := store.Save(ctx, "daily.json", []byte(`[{"month":"Tue","revenue":2}]`), "application/json", ""); err != nil {
		t.Fatal(err)
	}
	if rerendered := render(dailyDoc); !strings.Contains(rerendered, "Mon") || strings.Contains(rerendered, "Tue") {
		t.Errorf("earlier document changed after a later data write:\n%s", rerendered)
	}
}

func TestInstantiateSkillAsset_Integration_MissingDataFailsValidation(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "default"}
	const userID, sessionID = "u1", "s1"
	store := &integScopedStore{svc: svc, agent: "", user: userID, session: sessionID, logger: logger}
	sc := skillruntime.SkillContext{Name: "quarterly_report", LocalPath: writeReportSkill(t)}

	res, findings, err := skillruntime.InstantiateSkillAsset(ctx, sc, "report.html.samt", "",
		map[string]string{"prepared_by": "Ed"},
		skillruntime.InstantiateDeps{Store: store, Resolve: newRealResolve(svc, userID, sessionID, logger)})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected findings for a template whose required data artifact is missing, got none")
	}
	if res != nil {
		t.Fatalf("artifact should not be saved on failure, got %+v", res)
	}
	if content, _, _, _ := store.Load(ctx, "quarterly_report.html", nil); content != nil {
		t.Error("fail-closed violated: output artifact was written despite findings")
	}
}

func TestSkillEmbedResolveCheck_CapMatchesGatewayServe(t *testing.T) {
	cfg, err := entrypoint.ParseGatewayConfig(map[string]any{"namespace": "default", "gateway_id": "test"})
	if err != nil {
		t.Fatalf("parse gateway config: %v", err)
	}
	if serveArtifactSizeCap != cfg.GatewayArtifactContentLimitBytes {
		t.Fatalf("serveArtifactSizeCap (%d) != gateway GatewayArtifactContentLimitBytes default (%d): "+
			"instantiate-time validation no longer matches serve-time caps",
			serveArtifactSizeCap, cfg.GatewayArtifactContentLimitBytes)
	}
}
