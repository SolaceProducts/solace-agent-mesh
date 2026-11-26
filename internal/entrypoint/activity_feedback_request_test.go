// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestActivityFeedback_RejectsMalformedRequests(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	admin := &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}}
	tooMany := make([]string, maxFeedbackSubjects+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%q", fmt.Sprintf("agent-%d", i))
	}

	for name, tc := range map[string]struct {
		body, wantErr string
	}{
		"misspelled user filter": {`{"window":"7d","userId":"u1"}`, `unknown field "userId"`},
		"trailing data":          {`{"window":"7d"}{"window":"1d"}`, "invalid"},
		"too many subjects": {`{"window":"7d","subjects":[` + strings.Join(tooMany, ",") + `]}`,
			fmt.Sprintf("at most %d subjects", maxFeedbackSubjects)},
	} {
		t.Run(name, func(t *testing.T) {
			for _, report := range []string{"summary", "rows"} {
				_, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, report, tc.body, admin)
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "errorId") {
					t.Errorf("%s: err = %v, want a caller error containing %q", report, err, tc.wantErr)
				}
			}
		})
	}

	exactly := `{"window":"7d","subjects":[` + strings.Join(tooMany[:maxFeedbackSubjects], ",") + `]}`
	if _, err := callActivityFeedback[analytics.FeedbackRowsResponse](t, exe, "rows", exactly, admin); err != nil {
		t.Errorf("%d subjects: %v, want it accepted", maxFeedbackSubjects, err)
	}
}
