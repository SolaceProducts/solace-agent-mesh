// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type verbatimAgentResolver struct{}

func (verbatimAgentResolver) ResolveAgentRoutingName(_ context.Context, name string) (string, error) {
	return name, nil
}

type fakeToolAuthControl struct {
	response json.RawMessage
}

func (f *fakeToolAuthControl) Custom(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
	return f.response, nil
}

func TestMapToolAuthControlError_UnwrapsWrappedClientErrors(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("control call: %w", &control.ClientError{Code: control.ErrNotFound, Message: "app not found: awe"})
	h := &evalHandler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var unavailable *UnavailableError
	if err := h.mapToolAuthControlError(wrapped, "Solver", ""); !errors.As(err, &unavailable) {
		t.Errorf("wrapped app-not-found mapped to %T, want *UnavailableError", err)
	}
}

func TestMapToolAuthControlError_SeparatesTheThreeMeaningsOf32004(t *testing.T) {
	t.Parallel()
	const upgradeHint = "upgrade its runtime"
	cases := []struct {
		name         string
		message      string
		wantValidate bool
		wantUpgrade  bool
		wantClass    string
		wantInBody   string
	}{
		{
			name:        "an AWE predating the endpoints",
			message:     "unsupported: post instances/Solver/toolAuth/begin",
			wantUpgrade: true,
			wantClass:   "outdatedRuntime",
		},
		{
			name:         "an instance that cannot manage tool auth",
			message:      `instance "Solver" does not support tool auth management`,
			wantValidate: true,
			wantClass:    "instanceCannotManage",
			wantInBody:   "does not support tool auth management",
		},
		{
			name:      "an unrecognized AWE refusal keeps upstream text out of the response and log",
			message:   `oauth2 discovery failed for https://tools.internal.example/mcp?access_token=s3cr3t: 500 Internal Server Error`,
			wantClass: "agentReported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logged bytes.Buffer
			h := &evalHandler{logger: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))}

			err := h.mapToolAuthControlError(&control.ClientError{Code: -32004, Message: tc.message}, "Solver", "sheets")

			var validation *ValidationError
			var unavailable *UnavailableError
			switch {
			case tc.wantValidate && !errors.As(err, &validation):
				t.Fatalf("mapped to %T, want *ValidationError: a permanent refusal must not be a retryable 503", err)
			case !tc.wantValidate && !errors.As(err, &unavailable):
				t.Fatalf("mapped to %T, want *UnavailableError", err)
			}
			if got := strings.Contains(err.Error(), upgradeHint); got != tc.wantUpgrade {
				t.Errorf("upgrade advice present = %v, want %v: %v", got, tc.wantUpgrade, err)
			}
			if tc.wantInBody != "" && !strings.Contains(err.Error(), tc.wantInBody) {
				t.Errorf("the AWE's own reason never reaches the operator: %v", err)
			}
			out := logged.String()
			if !strings.Contains(out, "controlCode=-32004") {
				t.Errorf("control code absent from logs: %q", out)
			}
			if !strings.Contains(out, "refusalClass="+tc.wantClass) {
				t.Errorf("refusalClass=%s absent from logs: %q", tc.wantClass, out)
			}
			if tc.wantInBody == "" {
				for _, taint := range []string{"tools.internal.example", "access_token", "s3cr3t"} {
					if strings.Contains(err.Error(), taint) {
						t.Errorf("upstream text %q reached the response: %v", taint, err)
					}
					if strings.Contains(out, taint) {
						t.Errorf("upstream text %q reached the log: %q", taint, out)
					}
				}
			}
		})
	}
}

func TestRequireWebSchemeRefusesSchemesTheBrowserWouldExecute(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{"https://idp.example/auth?state=s", "http://localhost:8080/auth?state=s"} {
		if err := requireWebScheme(uri); err != nil {
			t.Errorf("requireWebScheme(%q) = %v, want nil", uri, err)
		}
	}
	for _, uri := range []string{"javascript:alert(1)", "data:text/html,<script>", "file:///etc/passwd", "ftp://host/x"} {
		if err := requireWebScheme(uri); err == nil {
			t.Errorf("requireWebScheme(%q) = nil, want rejection", uri)
		}
	}
}

func TestEvalToolAuthService_IsUnavailableWithoutAControlPlane(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	logger := slog.New(slog.DiscardHandler)
	experimentRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "ta-nocontrol", TargetAgent: "Solver", CreatedBy: "t", UpdatedBy: "t"}
	if err := experimentRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
	h := &evalHandler{
		experiments: eval.NewExperimentService(experimentRepo, store.NewEvalDatasetRepo(db),
			store.NewEvalEvaluatorRepo(db), store.NewEvalRunRepo(db), logger),
		logger:        logger,
		agentResolver: verbatimAgentResolver{},
		namespace:     "testns",
		db:            db,
	}
	svc := &EvalToolAuthService{h: h}

	var unavailable *UnavailableError
	if _, err := svc.Checklist(t.Context(), exp.ID); !errors.As(err, &unavailable) {
		t.Errorf("checklist without a control client = %v, want *UnavailableError", err)
	}
	if _, err := svc.CandidateAgents(t.Context(), exp.ID); !errors.As(err, &unavailable) {
		t.Errorf("candidate agents without a control client = %v, want *UnavailableError", err)
	}

	h.toolAuthControl = &fakeToolAuthControl{response: json.RawMessage(`{"auth_uri":"https://idp/auth?state=s"}`)}
	begun, err := svc.Begin(t.Context(), exp.ID, "Solver", "gmail", "tester")
	if !errors.As(err, &unavailable) || begun.AuthURI != "" {
		t.Errorf("begin without a broker = %+v, %v; want *UnavailableError and no authorization link", begun, err)
	}
}
