// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestServeArtifact_ResolvesInstantiatedTemplate(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)

	ctx := context.Background()
	sess := idgen.New()

	dataLoc := artSvc.Resolve("", "sam_dev_user", sess, "sales_data.json")
	salesJSON := `[{"month":"Jan","revenue":100},{"month":"Feb","revenue":250}]`
	if _, err := artSvc.Inner.Save(ctx, dataLoc, []byte(salesJSON), "application/json"); err != nil {
		t.Fatalf("save data artifact: %v", err)
	}

	report := `<!doctype html><html><body>
<h1>Quarterly Report</h1>
<p>By Ed</p>
<table>
«««template_liquid: data="sales_data.json"
{% for row in items %}<tr><td>{{ row.month }}</td><td>{{ row.revenue }}</td></tr>
{% endfor %}»»»
</table>
<script>var t = "${i}";</script>
</body></html>`
	reportLoc := artSvc.Resolve("", "sam_dev_user", sess, "quarterly_report.html")
	if _, err := artSvc.Inner.Save(ctx, reportLoc, []byte(report), "text/html"); err != nil {
		t.Fatalf("save report: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}/{filename}", h.handleGetLatest)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/artifacts/"+sess+"/quarterly_report.html", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	got := string(body)

	for _, want := range []string{"Jan", "100", "Feb", "250"} {
		if !strings.Contains(got, want) {
			t.Errorf("served report missing %q from resolved Liquid block:\n%s", want, got)
		}
	}
	if strings.Contains(got, "«««") {
		t.Errorf("Liquid block left unresolved at serve time:\n%s", got)
	}
	for _, marker := range []string{"[Template Error", "[Embed Error", "[Truncated"} {
		if strings.Contains(got, marker) {
			t.Errorf("served report contains resolution failure marker %q:\n%s", marker, got)
		}
	}
	if !strings.Contains(got, `"${i}"`) {
		t.Errorf("JS template literal was altered by serve-time resolution:\n%s", got)
	}
}
