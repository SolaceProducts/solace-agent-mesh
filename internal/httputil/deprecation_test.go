// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const noticeURL = "https://docs.example.com/deprecations/old-route"

func serve(t *testing.T, d Deprecation, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/things", WithDeprecation(d, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestWithDeprecationMarksTheWholeRoute(t *testing.T) {
	t.Parallel()
	since := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	rec := serve(t, Deprecation{NoticeURL: noticeURL, Since: since}, "/api/v2/platform/things")

	if rec.Code != http.StatusOK {
		t.Fatalf("the route must keep working, got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Solace-API-Deprecated"); got != noticeURL {
		t.Errorf("X-Solace-API-Deprecated = %q, want %q", got, noticeURL)
	}
	if got, want := rec.Header().Get("Deprecation"), "@1798859045"; got != want {
		t.Errorf("Deprecation = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Link"), "<"+noticeURL+`>; rel="deprecation"`; got != want {
		t.Errorf("Link = %q, want %q", got, want)
	}
}

func TestWithDeprecationOmitsTheDateWhenUnknown(t *testing.T) {
	t.Parallel()

	rec := serve(t, Deprecation{NoticeURL: noticeURL}, "/api/v2/platform/things")

	if _, present := rec.Header()["Deprecation"]; present {
		t.Error("Deprecation header must be omitted when no date is given")
	}
	if rec.Header().Get("X-Solace-API-Deprecated") == "" {
		t.Error("the house header is always sent")
	}
}

func TestWithDeprecationOfFieldsSendsOnlyTheHouseHeader(t *testing.T) {
	t.Parallel()

	rec := serve(t, Deprecation{NoticeURL: noticeURL, Since: time.Unix(1, 0), FieldsOnly: true}, "/api/v2/platform/things")

	if got := rec.Header().Get("X-Solace-API-Deprecated"); got != noticeURL {
		t.Errorf("X-Solace-API-Deprecated = %q, want %q", got, noticeURL)
	}
	for _, h := range []string{"Deprecation", "Link"} {
		if got, present := rec.Header()[h]; present {
			t.Errorf("%s = %q, want it omitted: RFC 9745 reads it as the whole route being deprecated", h, got)
		}
	}
}

func TestWithDeprecationMarksOnlyRequestsUsingTheDeprecatedParameter(t *testing.T) {
	t.Parallel()
	d := Deprecation{NoticeURL: noticeURL, QueryParams: []string{"colour"}}

	if rec := serve(t, d, "/api/v2/platform/things?colour=red"); rec.Header().Get("X-Solace-API-Deprecated") == "" {
		t.Error("a request using the deprecated parameter must be marked")
	}
	if rec := serve(t, d, "/api/v2/platform/things?owner=alice"); rec.Header().Get("X-Solace-API-Deprecated") != "" {
		t.Error("a request that does not use it must not be marked")
	}
}

func TestWithDeprecationReportsTheRouteWithoutUserData(t *testing.T) {
	t.Parallel()
	var routes []string
	d := Deprecation{NoticeURL: noticeURL, OnHit: func(route string) { routes = append(routes, route) }}

	serve(t, d, "/api/v2/platform/things?secret=abc")

	if len(routes) != 1 || routes[0] != "GET /api/v2/platform/things" {
		t.Errorf("OnHit got %v, want the route pattern only", routes)
	}
}

func TestWithDeprecationKeepsAnExistingLinkHeader(t *testing.T) {
	t.Parallel()
	handler := WithDeprecation(Deprecation{NoticeURL: noticeURL}, func(w http.ResponseWriter, _ *http.Request) {})
	rec := httptest.NewRecorder()
	rec.Header().Set("Link", `</next>; rel="next"`)

	handler(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	want := `</next>; rel="next", <` + noticeURL + `>; rel="deprecation"`
	if got := rec.Header().Get("Link"); got != want {
		t.Errorf("Link = %q, want %q", got, want)
	}
}

func TestWithDeprecationRequiresANoticeURL(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("a deprecation without a notice URL must fail at registration")
		}
	}()

	WithDeprecation(Deprecation{}, func(http.ResponseWriter, *http.Request) {})
}
