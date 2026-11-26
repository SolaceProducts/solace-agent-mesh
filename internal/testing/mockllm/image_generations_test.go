// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

type imageResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

func postImage(t *testing.T, srv *MockLLMServer, body string) (*http.Response, []byte, time.Duration) {
	t.Helper()
	start := time.Now()
	resp, err := http.Post("http://"+srv.Addr()+"/v1/images/generations", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("post image: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read image response: %v", err)
	}
	return resp, raw, time.Since(start)
}

func TestImageGenerationsReturnsDecodablePNG(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"img","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	resp, raw, _ := postImage(t, srv, `{"model":"e2e-mock","prompt":"a red square"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, raw)
	}

	var got imageResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, raw)
	}
	if len(got.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(got.Data))
	}
	if got.Data[0].URL != "" {
		t.Errorf("data[0].url = %q; the tool's SSRF guard rejects loopback URLs, so the mock must answer with b64_json only", got.Data[0].URL)
	}
	img, err := base64.StdEncoding.DecodeString(got.Data[0].B64JSON)
	if err != nil {
		t.Fatalf("decode b64_json: %v", err)
	}
	if want := "\x89PNG\r\n\x1a\n"; !bytes.HasPrefix(img, []byte(want)) {
		t.Errorf("payload is not a PNG: % x", img[:min(len(img), 8)])
	}
}

func TestImageGenerationsDoesNotCountAsAChatCall(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"img","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	postImage(t, srv, `{"model":"e2e-mock","prompt":"a red square"}`)
	if n := srv.RequestCount(); n != 0 {
		t.Errorf("RequestCount = %d after an image call, want 0", n)
	}

	postChat(t, srv, `{"model":"e2e-mock","messages":[{"role":"user","content":"hi"}]}`, false)
	if n := srv.RequestCount(); n != 1 {
		t.Errorf("RequestCount = %d after one chat call, want 1", n)
	}
}

func TestImageGenerationsIsCaptured(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"img","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	postImage(t, srv, `{"model":"e2e-mock","prompt":"a red square"}`)

	bodies := srv.CapturedRequests()
	if len(bodies) != 1 {
		t.Fatalf("captured %d requests, want 1", len(bodies))
	}
	var captured map[string]any
	if err := json.Unmarshal(bodies[0], &captured); err != nil {
		t.Fatalf("captured body is not JSON: %v", err)
	}
	if captured["prompt"] != "a red square" {
		t.Errorf("captured prompt = %v, want %q", captured["prompt"], "a red square")
	}
}

func TestImageGenerationsAppliesTTFT(t *testing.T) {
	t.Run("global delay", func(t *testing.T) {
		srv := startServer(t, mustScenario(t, `{"id":"img","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 120*time.Millisecond, 0)

		_, _, elapsed := postImage(t, srv, `{"model":"e2e-mock","prompt":"a red square"}`)
		if elapsed < 120*time.Millisecond {
			t.Errorf("elapsed = %v, want >= 120ms", elapsed)
		}
	})

	t.Run("matched scenario overrides", func(t *testing.T) {
		srv := startServer(t, mustScenario(t, `{"id":"img","match":"red square","responses":[{"latency":{"ttftMs":200},"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

		_, _, elapsed := postImage(t, srv, `{"model":"e2e-mock","prompt":"draw me a red square"}`)
		if elapsed < 200*time.Millisecond {
			t.Errorf("elapsed = %v, want >= 200ms (the matched scenario's ttftMs)", elapsed)
		}

		_, _, unmatched := postImage(t, srv, `{"model":"e2e-mock","prompt":"draw me a blue circle"}`)
		if unmatched >= 200*time.Millisecond {
			t.Errorf("elapsed = %v for an unmatched prompt, want the 0ms global delay", unmatched)
		}
	})
}

func TestImageGenerationsRejectsBadRequests(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"img","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)

	resp, err := http.Get("http://" + srv.Addr() + "/v1/images/generations")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", resp.StatusCode)
	}

	if r, _, _ := postImage(t, srv, `{"model":"e2e-mock"}`); r.StatusCode != http.StatusBadRequest {
		t.Errorf("empty-prompt status = %d, want 400", r.StatusCode)
	}
	if r, _, _ := postImage(t, srv, `not json`); r.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed-body status = %d, want 400", r.StatusCode)
	}
}
