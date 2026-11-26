// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

type errorBody struct {
	Message           string              `json:"message"`
	ErrorID           string              `json:"errorId"`
	ValidationDetails map[string][]string `json:"validationDetails"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if body.ErrorID == "" {
		t.Errorf("errorId is empty in %s", rec.Body.String())
	}
	return body
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

func expectDetail(t *testing.T, rec *httptest.ResponseRecorder, status int, field string) {
	t.Helper()
	expectStatus(t, rec, status)
	if body := decodeError(t, rec); len(body.ValidationDetails[field]) == 0 {
		t.Errorf("validationDetails has nothing for %q: %s", field, rec.Body.String())
	}
}

func stub[Req, Resp any]() Handler {
	return Handle(func(context.Context, Req) (Resp, error) {
		var resp Resp
		return resp, nil
	}, "stub")
}
