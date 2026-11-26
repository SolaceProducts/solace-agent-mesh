// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"net/http"
	"strings"
	"testing"
)

func postMessage(t *testing.T, m *MockGoogleChatServer, space string) (status int, retryAfter string) {
	t.Helper()
	resp, err := http.Post(m.URL()+"/v1/"+space+"/messages", "application/json", strings.NewReader(`{"text": "hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Retry-After")
}

func TestMockGoogleChatServerEnforcesTheSpaceWriteRate(t *testing.T) {
	m := NewMockGoogleChatServer()
	t.Cleanup(m.Close)
	m.EnforceSpaceWriteRate(GoogleChatSpaceWriteInterval)

	if status, _ := postMessage(t, m, "spaces/A"); status != http.StatusOK {
		t.Fatalf("first write = %d, want 200", status)
	}
	if status, retryAfter := postMessage(t, m, "spaces/A"); status != http.StatusTooManyRequests || retryAfter == "" {
		t.Errorf("second write to the space = %d (Retry-After %q), want 429 with a Retry-After", status, retryAfter)
	}
	if status, _ := postMessage(t, m, "spaces/B"); status != http.StatusOK {
		t.Errorf("write to another space = %d, want 200", status)
	}
	if n := m.RateLimitedWrites(); n != 1 {
		t.Errorf("rate-limited writes = %d, want 1", n)
	}
}
