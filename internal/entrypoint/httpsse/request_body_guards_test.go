// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestOversizeBodyReturns413(t *testing.T) {
	gw := newTestGateway(t)

	body := bytes.NewReader(make([]byte, 60*1024*1024))
	req, err := http.NewRequest("POST", gw.server.URL+"/api/v1/feedback", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 from MaxBytes middleware, got %d", resp.StatusCode)
	}
}

func TestOversizeBodyOnAuthSkipRouteStillCapped(t *testing.T) {
	gw := newTestGateway(t)

	body := bytes.NewReader(make([]byte, 60*1024*1024))
	req, err := http.NewRequest("POST", gw.server.URL+"/health", body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 even for SkipPaths route, got %d", resp.StatusCode)
	}
}

func TestTextPlainOnJSONRouteReturns415(t *testing.T) {
	gw := newTestGateway(t)

	body := strings.NewReader(`{"taskId":"x","rating":"up"}`)
	req, err := http.NewRequest("POST", gw.server.URL+"/api/v1/feedback", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for text/plain body on JSON-only route, got %d", resp.StatusCode)
	}
}

func TestApplicationJSONReachesHandler(t *testing.T) {
	gw := newTestGateway(t)

	for _, ct := range []string{"application/json", "application/json; charset=utf-8"} {
		body := strings.NewReader(`{"taskId":"x","rating":"up"}`)
		req, err := http.NewRequest("POST", gw.server.URL+"/api/v1/feedback", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", ct)
		resp, err := gw.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: expected handler invoked, got 415", ct)
		}
	}
}

func TestMultipartUploadRouteOptsOutOfRequireJSON(t *testing.T) {
	gw := newTestGateway(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("uploadFile", "x.txt")
	_, _ = fw.Write([]byte("hello"))
	_ = w.WriteField("sessionId", "")
	_ = w.Close()

	req, err := http.NewRequest("POST", gw.server.URL+"/api/v1/artifacts/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Fatalf("multipart upload route should not return 415 — opt-out via AllowNonJSON not wired")
	}
}

func TestDeleteWithoutBodyPassesRequireJSON(t *testing.T) {
	gw := newTestGateway(t)

	req, err := http.NewRequest("DELETE", gw.server.URL+"/api/v1/sessions/some-nonexistent-id", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Fatalf("DELETE without body should not be 415-d, got %d", resp.StatusCode)
	}
}
