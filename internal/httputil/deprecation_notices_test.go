// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func response(method, path, notice string) *http.Response {
	resp := &http.Response{Header: http.Header{}, Request: httptest.NewRequest(method, path, nil)}
	if notice != "" {
		resp.Header.Set(solaceDeprecatedHeader, notice)
	}
	return resp
}

func TestDeprecationNoticesSayNothingWhenNothingWasDeprecated(t *testing.T) {
	t.Parallel()
	var n DeprecationNotices
	n.Record(response(http.MethodGet, "/api/v2/platform/things", ""))

	if got := n.Message(); got != "" {
		t.Fatalf("Message() = %q, want empty", got)
	}
}

func TestDeprecationNoticesJoinEveryCallIntoOneMessage(t *testing.T) {
	t.Parallel()
	var n DeprecationNotices
	n.Record(response(http.MethodGet, "/api/v2/platform/things", noticeURL))
	n.Record(response(http.MethodGet, "/api/v2/platform/things", noticeURL))
	n.Record(response(http.MethodDelete, "/api/v2/platform/things/7", noticeURL))

	got := n.Message()

	if strings.Count(got, "\n") != 0 || strings.Count(got, noticeURL) != 1 {
		t.Fatalf("want a single line naming the notice once, got %q", got)
	}
	for _, route := range []string{"DELETE /api/v2/platform/things/7", "GET /api/v2/platform/things"} {
		if strings.Count(got, route) != 1 {
			t.Errorf("route %q should appear once in %q", route, got)
		}
	}
}

func TestDeprecationNoticesResetForgetsEarlierCalls(t *testing.T) {
	t.Parallel()
	var n DeprecationNotices
	n.Record(response(http.MethodGet, "/api/v2/platform/things", noticeURL))
	n.Reset()

	if got := n.Message(); got != "" {
		t.Fatalf("Message() after Reset = %q, want empty", got)
	}
}

func TestRecordIgnoresANoticeThatIsNotAPrintableHTTPURL(t *testing.T) {
	for name, notice := range map[string]string{
		"C1 control":       "https://docs.example.com/\u009b2J",
		"escape sequence":  "https://docs.example.com/\x1b[2J",
		"newline":          "https://docs.example.com/a\nforged: line",
		"not http":         "ftp://docs.example.com/x",
		"script with host": "javascript://host/x",
		"no host":          "https:///path",
		"plain text":       "see the docs",
	} {
		t.Run(name, func(t *testing.T) {
			n := &DeprecationNotices{}
			resp := &http.Response{Header: http.Header{}, Request: httptest.NewRequest(http.MethodGet, "/api/v2/platform/a", nil)}
			resp.Header.Set(solaceDeprecatedHeader, notice)

			n.Record(resp)

			if got := n.Message(); got != "" {
				t.Errorf("message = %q, want nothing recorded", got)
			}
		})
	}
}
