// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestInlineChartJS_ReplacesMarkerWithVendoredLib(t *testing.T) {
	out, err := inlineChartJS(activityReportTemplate)
	if err != nil {
		t.Fatalf("inlineChartJS: %v", err)
	}
	if strings.Contains(out, chartJSInlineMarker) {
		t.Error("marker still present — Chart.js was not inlined")
	}
	if strings.Contains(out, "cdn.jsdelivr.net") || strings.Contains(out, "unpkg.com") {
		t.Error("a CDN reference remains in the inlined template")
	}
	if strings.Contains(out, "{% raw %}") || strings.Contains(out, "{% endraw %}") {
		t.Error("inlined Chart.js is wrapped in a Liquid raw block — the renderer leaks it literally and breaks the JS")
	}
	if strings.Contains(chartUMDMinJS, "{{") || strings.Contains(chartUMDMinJS, "{%") {
		t.Error("vendored Chart.js contains a Liquid opener — bare inlining would be corrupted at render")
	}
	if !strings.Contains(out, "Chart.js") {
		t.Error("vendored Chart.js library content missing from output")
	}
}

func TestActivityReport_InlinedChartJSValidInRenderedHTML(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}

	html, _, err := renderActivityReport(context.Background(), toolCtx,
		&ActivityReport{Window: Window{Label: "t"}}, activityFilters{}, time.Now(), "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)
	if strings.Contains(s, "{% raw %}") || strings.Contains(s, "{% endraw %}") {
		t.Error("rendered report contains a literal Liquid raw tag — inlined Chart.js will throw a JS syntax error at load")
	}
	if !strings.Contains(s, "Chart.js v") {
		t.Error("Chart.js library banner missing from rendered report — not inlined")
	}
}

func TestInlineChartJS_MissingMarkerErrors(t *testing.T) {
	if _, err := inlineChartJS("<html>no marker here</html>"); err == nil {
		t.Error("expected error when the inline marker is absent, got nil")
	}
}
