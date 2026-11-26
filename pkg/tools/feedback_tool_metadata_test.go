// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"sort"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestFeedbackToolMetadata_RequiredScopesStayNilBecauseGatewayResolvesAccess(t *testing.T) {
	type feedbackTool interface {
		Name() string
		Description() string
		Parameters() *tool.JSONSchema
		RequiredScopes() []string
	}
	cases := []struct {
		tool   feedbackTool
		name   string
		params []string
	}{
		{NewSummarizeFeedbackTool(), "summarize_feedback", []string{"endDate", "sortBy", "startDate", "subjectType", "subjects", "userId", "window"}},
		{NewListFeedbackTool(), "list_feedback", []string{"endDate", "feedbackId", "rating", "startDate", "subjectType", "subjects", "userId", "window"}},
		{NewExplainFeedbackTool(), "explain_feedback", []string{"feedbackId", "taskId"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tool.Name(); got != c.name {
				t.Errorf("Name() = %q, want %q", got, c.name)
			}
			if !strings.Contains(c.tool.Description(), "«artifact_content»") {
				t.Errorf("Description() does not tell the model to echo the artifact embed")
			}
			if s := c.tool.RequiredScopes(); s != nil {
				t.Errorf("RequiredScopes() = %v, want nil", s)
			}
			p := c.tool.Parameters()
			if p == nil || p.Type != "object" {
				t.Fatalf("Parameters() = %+v, want an object schema", p)
			}
			got := make([]string, 0, len(p.Properties))
			for k := range p.Properties {
				got = append(got, k)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(c.params, ",") {
				t.Errorf("parameters = %v, want %v", got, c.params)
			}
		})
	}
}
