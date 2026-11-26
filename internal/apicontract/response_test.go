// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/validator/v10"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

func TestWriteResource_WrapsInData(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteResource(rec, http.StatusCreated, struct {
		ID string `json:"id"`
	}{ID: "a1"})

	if rec.Code != http.StatusCreated || rec.Body.String() != `{"data":{"id":"a1"}}`+"\n" {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteList_NeverSendsNullData(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteList[string](rec, nil, Page{Number: 1, Size: 20}, 0)

	want := `{"data":[],"meta":{"pagination":{"pageNumber":1,"count":0,"pageSize":20,"nextPage":null,"totalPages":0}}}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("got  %s\nwant %s", rec.Body.String(), want)
	}
}

func TestWriteServiceError(t *testing.T) {
	const failure = "Unable to create widget"
	for _, tc := range []struct {
		name        string
		err         error
		wantStatus  int
		wantDetails map[string][]string
	}{
		{"validation with details", &apierr.ValidationError{Message: "name is required", Details: map[string][]string{"name": {"is required"}}},
			http.StatusUnprocessableEntity, map[string][]string{"name": {"is required"}}},
		{"validation with field", &apierr.ValidationError{Message: "must be unique", Field: "name"},
			http.StatusUnprocessableEntity, map[string][]string{"name": {"must be unique"}}},
		{"validation built from an upstream error", &apierr.ValidationError{Message: "failed to fetch agent card: dial tcp 10.0.0.5:443: connect: connection refused"},
			http.StatusUnprocessableEntity, nil},
		{"business rule", fmt.Errorf("delete: %w", &apierr.BusinessRuleViolationError{Code: "HAS_RUNS", Message: "Dataset has active runs"}),
			http.StatusUnprocessableEntity, nil},
		{"not found", &apierr.NotFoundError{Resource: "agent", ID: "a1"}, http.StatusNotFound, nil},
		{"conflict", &apierr.ConflictError{Message: "Name already in use"}, http.StatusConflict, nil},
		{"forbidden", &apierr.ForbiddenError{Message: "RBAC management is off"}, http.StatusForbidden, nil},
		{"external", &apierr.ExternalServiceError{Service: "llm", Message: "The model provider rejected the key", Cause: errors.New("401 from https://x?key=secret")},
			http.StatusBadGateway, nil},
		{"unavailable", &apierr.UnavailableError{Message: "Roles are still loading"}, http.StatusServiceUnavailable, nil},
		{"untyped", fmt.Errorf("insert agent: %w", errors.New(`pq: duplicate key value violates "agents_pkey"`)),
			http.StatusInternalServerError, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteServiceError(rec, tc.err, failure)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			body := decodeError(t, rec)
			if body.Message != failure {
				t.Errorf("message = %q, want %q", body.Message, failure)
			}
			if fmt.Sprint(body.ValidationDetails) != fmt.Sprint(tc.wantDetails) {
				t.Errorf("validationDetails = %v, want %v", body.ValidationDetails, tc.wantDetails)
			}
		})
	}
}

func TestWriteErrorWithCause_LogsARetryableCauseAtWarn(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	WriteErrorWithCause(httptest.NewRecorder(), http.StatusServiceUnavailable, "busy", nil, fmt.Errorf("%w: shed", ErrRetryable))

	var rec map[string]any
	if err := json.Unmarshal(logs.Bytes(), &rec); err != nil {
		t.Fatalf("log line: %v: %s", err, logs.String())
	}
	if rec["level"] != "WARN" || rec["msg"] != MsgHTTPErrorResponse {
		t.Errorf("record = %v, want a WARN %q", rec, MsgHTTPErrorResponse)
	}
}

func isolateRules(t *testing.T) {
	t.Helper()
	rulesMu.Lock()
	savedRules, savedBuilt := rules, validatorBuilt
	rules, validatorBuilt = map[string]Rule{}, false
	rulesMu.Unlock()
	t.Cleanup(func() {
		rulesMu.Lock()
		rules, validatorBuilt = savedRules, savedBuilt
		rulesMu.Unlock()
	})
}

func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s must panic", what)
		}
	}()
	fn()
}

func TestRegisterRule_PanicsOnADuplicateTag(t *testing.T) {
	isolateRules(t)
	rule := Rule{Tag: "dupe_test_rule", Check: func(validator.FieldLevel) bool { return true }}
	RegisterRule(rule)
	mustPanic(t, "a second registration of the same tag", func() { RegisterRule(rule) })
}

func TestRegisterRule_PanicsOnceTheValidatorIsBuilt(t *testing.T) {
	isolateRules(t)
	rulesMu.Lock()
	validatorBuilt = true
	rulesMu.Unlock()
	mustPanic(t, "a registration after the validator was built", func() {
		RegisterRule(Rule{Tag: "late_test_rule", Check: func(validator.FieldLevel) bool { return true }})
	})
}

func TestRegisterFieldReader_PanicsOnADuplicateType(t *testing.T) {
	type reader struct{}
	read := func(http.ResponseWriter, *http.Request) (reader, bool) { return reader{}, true }
	RegisterFieldReader(read)
	mustPanic(t, "a second reader for the same type", func() { RegisterFieldReader(read) })
}
