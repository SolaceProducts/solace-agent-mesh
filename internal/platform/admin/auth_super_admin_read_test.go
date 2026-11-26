// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

var otherResourceReadRefusal = readRefusal{
	logMessage: "other resource read refused",
	message:    "Reading the other resource requires a super administrator",
}

func TestRequireSuperAdminToRead(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		refusal    readRefusal
		identity   *auth.Subject
		wantStatus int
		wantRefuse bool
	}{
		"no identity is unauthenticated": {
			refusal: apiKeyReadRefusal, wantStatus: http.StatusUnauthorized,
		},
		"a narrow person is refused with the key inventory's wording": {
			refusal: apiKeyReadRefusal, identity: interactiveIdentity("budget:*:read"),
			wantStatus: http.StatusForbidden, wantRefuse: true,
		},
		"a narrow person is refused with another resource's wording": {
			refusal: otherResourceReadRefusal, identity: interactiveIdentity("budget:*:read"),
			wantStatus: http.StatusForbidden, wantRefuse: true,
		},
		"the universal grant reads": {
			refusal: otherResourceReadRefusal, identity: interactiveIdentity("*:*:*"),
			wantStatus: http.StatusOK,
		},
		"a key holding the universal grant reads": {
			refusal: otherResourceReadRefusal, identity: keyIdentity("*:*:*"),
			wantStatus: http.StatusOK,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			logs := &syncedBuffer{}
			guarded := requireSuperAdminToRead(slog.New(slog.NewJSONHandler(logs, nil)), tc.refusal,
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/resource", nil)
			if tc.identity != nil {
				req = req.WithContext(auth.ContextWithSubject(req.Context(), tc.identity))
			}
			rr := httptest.NewRecorder()
			guarded(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			refusals := readLogLines(t, logs, tc.refusal.logMessage)
			if !tc.wantRefuse {
				if len(refusals) != 0 {
					t.Errorf("logged %d refusal lines, want none", len(refusals))
				}
				return
			}
			var body struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Message != tc.refusal.message {
				t.Errorf("message = %q, want %q", body.Message, tc.refusal.message)
			}
			if len(refusals) != 1 {
				t.Fatalf("got %d %q lines, want 1", len(refusals), tc.refusal.logMessage)
			}
			got := refusals[0]
			if got["level"] != "WARN" || got["reason"] != "not a super administrator" ||
				got["method"] != http.MethodGet || got["actor"] != tc.identity.UserID() {
				t.Errorf("refusal line = %v, want WARN with reason, method GET and the actor's subject", got)
			}
			if line := logs.String(); strings.Contains(line, tc.identity.Email()) {
				t.Errorf("refusal line = %s, want no email: logging.md keeps it out of logs", line)
			}
		})
	}
}

func readLogLines(t *testing.T, logs *syncedBuffer, msg string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == msg {
			lines = append(lines, rec)
		}
	}
	return lines
}
