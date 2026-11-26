// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

type createWidgetRequest struct {
	Name        string            `json:"name" validate:"required"`
	Description string            `json:"description" validate:"required"`
	Size        int               `json:"size"`
	StartTime   Instant           `json:"startTime"`
	Label       Patch[string]     `json:"label"`
	Spec        Patch[widgetSpec] `json:"spec"`
}

type widgetSpec struct {
	Name string `json:"name"`
}

func TestDecodeRequest(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
		wantFields  []string
	}{
		{"valid", `{"name":"a","description":"b","startTime":"2023-11-14T22:13:20.123Z"}`, 0, "", nil},
		{"not JSON", `{"name":`, http.StatusBadRequest, MsgInvalidBody, nil},
		{"empty", ``, http.StatusBadRequest, MsgInvalidBody, nil},
		{"unknown field", `{"name":"a","description":"b","extra":"c"}`, http.StatusBadRequest, MsgInvalidBody, []string{"extra"}},
		{"wrong type", `{"name":"a","description":"b","size":"big"}`, http.StatusBadRequest, MsgInvalidBody, []string{"size"}},
		{"empty date-time", `{"name":"a","description":"b","startTime":""}`, http.StatusBadRequest, MsgInvalidBody, []string{"startTime"}},
		{"wrong type in patch", `{"name":"a","description":"b","label":7}`, http.StatusBadRequest, MsgInvalidBody, []string{"label"}},
		{"wrong type inside a patched object", `{"name":"a","description":"b","spec":{"name":1}}`, http.StatusBadRequest, MsgInvalidBody, []string{"spec.name"}},
		{"first of two wrong types", `{"name":"a","description":"b","startTime":"bad","size":"big"}`, http.StatusBadRequest, MsgInvalidBody, []string{"startTime"}},
		{"unknown field inside a patched object", `{"name":"a","description":"b","spec":{"b":1}}`, http.StatusBadRequest, MsgInvalidBody, []string{"spec.b"}},
		{"unknown field before a bad member", `{"extra":1,"spec":{"b":1}}`, http.StatusBadRequest, MsgInvalidBody, []string{"extra"}},
		{"every field error", `{}`, http.StatusUnprocessableEntity, MsgValidationFailed, []string{"description", "name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var v createWidgetRequest
			ok := decodeRequest(rec, req, &v, false)

			if tc.wantStatus == 0 {
				if !ok {
					t.Fatalf("rejected a valid body: %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			if ok || rec.Code != tc.wantStatus {
				t.Fatalf("ok = %v, status = %d, want false and %d (%s)", ok, rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := decodeError(t, rec)
			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
			if got := slices.Sorted(maps.Keys(body.ValidationDetails)); !slices.Equal(got, tc.wantFields) {
				t.Errorf("validationDetails fields = %v, want %v", got, tc.wantFields)
			}
		})
	}
}

func TestPathUUID(t *testing.T) {
	for _, tc := range []struct {
		id string
		ok bool
	}{
		{"019f9b46-77bc-76ba-a494-017487b5f5c4", true},
		{"019F9B46-77BC-76BA-A494-017487B5F5C4", false},
		{"019f9b4677bc76baa494017487b5f5c4", false},
		{"{019f9b46-77bc-76ba-a494-017487b5f5c4}", false},
		{"my-agent", false},
	} {
		mux := http.NewServeMux()
		var got string
		var ok bool
		mux.HandleFunc("GET /agents/{agentId}", func(w http.ResponseWriter, r *http.Request) {
			got, ok = PathUUID(w, r, "agentId")
		})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agents/"+tc.id, nil))

		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.id, ok, tc.ok)
			continue
		}
		if ok {
			if got != tc.id {
				t.Errorf("%s: id = %q", tc.id, got)
			}
			continue
		}
		body := decodeError(t, rec)
		if rec.Code != http.StatusBadRequest || body.ValidationDetails["agentId"] == nil {
			t.Errorf("%s: status %d details %v, want 400 naming agentId", tc.id, rec.Code, body.ValidationDetails)
		}
	}
}

func TestParseListQuery(t *testing.T) {
	for _, tc := range []struct {
		query      string
		want       Page
		wantStatus int
		wantFields []string
	}{
		{"", Page{Number: 1, Size: 20}, 0, nil},
		{"pageNumber=3&pageSize=100", Page{Number: 3, Size: 100}, 0, nil},
		{"pageNumber=0&pageSize=101", Page{}, http.StatusUnprocessableEntity, []string{"pageNumber", "pageSize"}},
		{"pageNumber=x&pageSize=101", Page{}, http.StatusBadRequest, []string{"pageNumber", "pageSize"}},
		{"pageSize=1.5", Page{}, http.StatusBadRequest, []string{"pageSize"}},
		{"pageNumber=1000000", Page{Number: 1_000_000, Size: 20}, 0, nil},
		{"pageNumber=1000001", Page{}, http.StatusUnprocessableEntity, []string{"pageNumber"}},
		{"pageNumber=9223372036854775807", Page{}, http.StatusUnprocessableEntity, []string{"pageNumber"}},
		{"pageNumber=99999999999999999999", Page{}, http.StatusBadRequest, []string{"pageNumber"}},
	} {
		rec := httptest.NewRecorder()
		got, ok := parseListQuery(rec, httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil))

		if tc.wantStatus == 0 {
			if !ok || got != tc.want {
				t.Errorf("%q: got %+v ok=%v, want %+v", tc.query, got, ok, tc.want)
			}
			continue
		}
		if ok || rec.Code != tc.wantStatus {
			t.Errorf("%q: ok = %v, status = %d, want false and %d", tc.query, ok, rec.Code, tc.wantStatus)
			continue
		}
		body := decodeError(t, rec)
		if fields := slices.Sorted(maps.Keys(body.ValidationDetails)); !slices.Equal(fields, tc.wantFields) {
			t.Errorf("%q: validationDetails fields = %v, want %v", tc.query, fields, tc.wantFields)
		}
	}
}

func TestParseSort(t *testing.T) {
	for _, tc := range []struct {
		query      string
		want       SortOrder
		wantStatus int
	}{
		{"", SortOrder{}, 0},
		{"sort=name:asc", SortOrder{Field: "name"}, 0},
		{"sort=createdTime:desc", SortOrder{Field: "createdTime", Desc: true}, 0},
		{"sort=name", SortOrder{Field: "name"}, 0},
		{"sort=name:", SortOrder{}, http.StatusBadRequest},
		{"sort=name:up", SortOrder{}, http.StatusBadRequest},
		{"sort=name:ASC", SortOrder{}, http.StatusBadRequest},
		{"sort=:asc", SortOrder{}, http.StatusBadRequest},
		{"sort=name:asc:desc", SortOrder{}, http.StatusBadRequest},
		{"sort=size:asc", SortOrder{}, http.StatusUnprocessableEntity},
		{"sort=Name:asc", SortOrder{}, http.StatusUnprocessableEntity},
		{"sort=name:asc&sort=createdTime:desc", SortOrder{}, http.StatusUnprocessableEntity},
	} {
		rec := httptest.NewRecorder()
		got, ok := parseSort(rec, httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil), []string{"name", "createdTime"})

		if tc.wantStatus == 0 {
			if !ok || got != tc.want {
				t.Errorf("%q: got %+v ok=%v, want %+v", tc.query, got, ok, tc.want)
			}
			continue
		}
		if ok || rec.Code != tc.wantStatus {
			t.Errorf("%q: ok = %v, status = %d, want false and %d", tc.query, ok, rec.Code, tc.wantStatus)
			continue
		}
		if body := decodeError(t, rec); body.Message != MsgInvalidQueryParam || body.ValidationDetails["sort"] == nil {
			t.Errorf("%q: got %+v, want %q naming sort", tc.query, body, MsgInvalidQueryParam)
		}
	}
}

type crossCheckedRequest struct {
	Name     string         `json:"name" validate:"required"`
	Extra    map[string]any `json:"extra"`
	problems map[string][]string
}

func (r crossCheckedRequest) CrossFieldProblems() map[string][]string { return r.problems }

func TestValidateBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		req        crossCheckedRequest
		wantFields []string
		wantOK     bool
	}{
		{"no rule broken", crossCheckedRequest{Name: "a"}, nil, true},
		{"tags only", crossCheckedRequest{}, []string{"name"}, false},
		{"cross-field only", crossCheckedRequest{Name: "a", problems: map[string][]string{"extra": {"unknown key"}}}, []string{"extra"}, false},
		{"both, reported together", crossCheckedRequest{problems: map[string][]string{"extra": {"unknown key"}}}, []string{"extra", "name"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBody(tc.req)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("validateBody = %v, want nil", err)
				}
				return
			}
			invalid, ok := errors.AsType[*apierr.ValidationError](err)
			if !ok {
				t.Fatalf("validateBody = %v, want a ValidationError", err)
			}
			if got := slices.Sorted(maps.Keys(invalid.Details)); !slices.Equal(got, tc.wantFields) {
				t.Errorf("fields %v, want %v", got, tc.wantFields)
			}
			if invalid.Message == "" {
				t.Error("message is empty; the envelope needs a sentence")
			}
		})
	}
}
