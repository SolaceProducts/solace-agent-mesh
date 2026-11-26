// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON_RejectsUnknownFields(t *testing.T) {
	type target struct {
		Name string `json:"name"`
	}

	tests := []struct {
		name          string
		body          string
		wantErrSubstr string
	}{
		{
			name:          "unknown field",
			body:          `{"name":"ok","bogusField":1}`,
			wantErrSubstr: "bogusField",
		},
		{
			name:          "empty body",
			body:          "",
			wantErrSubstr: "empty",
		},
		{
			name:          "invalid json",
			body:          `{"name":`,
			wantErrSubstr: "unexpected",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var v target
			err := decodeJSON(req, &v)
			if err == nil {
				t.Fatalf("decodeJSON returned nil error, want error containing %q", tc.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}

func TestDecodeJSON_AcceptsKnownFields(t *testing.T) {
	type target struct {
		Name string `json:"name"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	var v target
	if err := decodeJSON(req, &v); err != nil {
		t.Fatalf("decodeJSON returned unexpected error: %v", err)
	}
	if v.Name != "ok" {
		t.Errorf("decoded name = %q, want %q", v.Name, "ok")
	}
}

func TestDecodeJSON_RejectsResponseFields(t *testing.T) {
	type target struct {
		Name string `json:"name"`
	}
	body := `{
		"id":"019d-aaa","name":"my-gw","type":"teams",
		"createdBy":"alice","deploymentStatus":"deployed"
	}`
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body))
	var v target
	err := decodeJSON(req, &v)
	if err == nil {
		t.Fatal("decodeJSON should reject unknown fields from response shape")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error = %q, want it to mention 'unknown field'", err.Error())
	}
}

func TestDecodeJSON_RejectsOversizeBody(t *testing.T) {
	type target struct {
		Name string `json:"name"`
	}
	oversize := `{"name":"` + strings.Repeat("x", maxJSONBodyBytes+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversize))
	var v target
	err := decodeJSON(req, &v)
	if err == nil {
		t.Fatal("decodeJSON accepted an oversize body, want error")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want it to mention 'maximum size'", err.Error())
	}
}

func TestRawBodyMap_RejectsOversizeBody(t *testing.T) {
	oversize := `{"name":"` + strings.Repeat("x", maxJSONBodyBytes+1) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversize))
	rr := httptest.NewRecorder()

	type target struct {
		Name string `json:"name"`
	}
	var v target
	raw, ok := rawBodyMap(rr, req, &v)
	if ok {
		t.Fatal("rawBodyMap accepted an oversize body, want rejection")
	}
	if raw != nil {
		t.Errorf("raw = %v, want nil on rejection", raw)
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "maximum size") {
		t.Errorf("body = %q, want it to mention 'maximum size'", rr.Body.String())
	}
}

func TestBadRequestFromDecode_PreservesErrorMessage(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"bogusField":1}`))
	type target struct {
		Name string `json:"name"`
	}
	var v target
	err := decodeJSON(req, &v)
	if err == nil {
		t.Fatal("expected decode error")
	}

	rr := httptest.NewRecorder()
	badRequestFromDecode(rr, err)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "bogusField") {
		t.Errorf("response body = %q, want it to mention bogusField", body)
	}
	if !strings.Contains(body, "message") {
		t.Errorf("response body = %q, want 'message' key (ADR envelope)", body)
	}
	if !strings.Contains(body, "errorId") {
		t.Errorf("response body = %q, want 'errorId' key (ADR envelope)", body)
	}
}

func TestDecodeJSON_OversizeKeepsMessageAndIsMaxBytesError(t *testing.T) {
	oversize := `{"name":"` + strings.Repeat("x", maxJSONBodyBytes+1) + `"}`
	err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversize)), &struct {
		Name string `json:"name"`
	}{})

	if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
		t.Errorf("error %v does not unwrap to *http.MaxBytesError", err)
	}
	if want := "request body exceeds maximum size of 16 MB"; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}
