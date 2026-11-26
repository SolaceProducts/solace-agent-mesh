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

func TestActivityReportOverflowCount(t *testing.T) {
	cases := []struct {
		rows int
		want int
	}{
		{0, 0},
		{1, 0},
		{activityReportPeekRows, 0},
		{activityReportPeekRows + 1, 1},
		{activityReportPeekRows + 3, 3},
	}
	for _, c := range cases {
		if got := activityReportOverflowCount(c.rows); got != c.want {
			t.Errorf("activityReportOverflowCount(%d) = %d, want %d", c.rows, got, c.want)
		}
	}
}

func TestActivityReport_PeekAndShowMoreButton(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}

	rowCount := activityReportPeekRows + 3
	var feedback []FeedbackRow
	for i := 0; i < rowCount; i++ {
		feedback = append(feedback, FeedbackRow{TaskID: "t", Rating: "up", SubjectName: "Agent", EntrypointType: "webui"})
	}

	report := &ActivityReport{Window: Window{Label: "t"}, Feedback: feedback}
	html, _, err := renderActivityReport(context.Background(), toolCtx, report, activityFilters{}, time.Now(), "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(html)

	if got := strings.Count(s, "<td class=\"mono\">t</td>"); got != rowCount {
		t.Errorf("rendered %d feedback rows, want all %d present (peek only hides via CSS, never server-side)", got, rowCount)
	}
	if !strings.Contains(s, `class="table-card peek"`) {
		t.Error("Feedback table missing the .peek class that drives the CSS row-hiding")
	}
	want := "Show 3 more…"
	if !strings.Contains(s, want) {
		t.Errorf("missing %q show-more button; got:\n%s", want, s)
	}
}

func TestActivityReport_NoShowMoreButtonUnderPeekThreshold(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	toolCtx := &tool.Context{ArtifactService: svc, AgentName: "a", UserID: "u", SessionID: "s"}

	feedback := []FeedbackRow{{TaskID: "t", Rating: "up", SubjectName: "Agent", EntrypointType: "webui"}}
	report := &ActivityReport{Window: Window{Label: "t"}, Feedback: feedback}
	html, _, err := renderActivityReport(context.Background(), toolCtx, report, activityFilters{}, time.Now(), "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(string(html), "<button") {
		t.Error("show-more button rendered with only one row — nothing to hide")
	}
}
