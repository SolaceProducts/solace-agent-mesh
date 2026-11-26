// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"strings"
	"testing"
)

func TestDefaultCredentialKeyBase_DropsURLUserinfo(t *testing.T) {
	const canary = "sk-live-MUST-NOT-APPEAR"

	for _, tc := range []struct{ name, url string }{
		{"token only", "https://" + canary + "@mcp.example.com/tenant1"},
		{"user and password", "https://svc:" + canary + "@mcp.example.com/tenant1"},
		{"unencoded @ inside secret", "https://svc:" + canary + "@x@mcp.example.com/tenant1"},
		{"http scheme", "http://" + canary + "@mcp.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: tc.url})
			if strings.Contains(got, canary) || strings.Contains(got, strings.ReplaceAll(canary, "-", "_")) {
				t.Errorf("client id carries the credential: %q", got)
			}
			if !strings.Contains(got, "mcp_example_com") {
				t.Errorf("client id lost the host, which is what makes it distinct: %q", got)
			}
		})
	}
}

func TestDefaultCredentialKeyBase_KeepsPathAfterStrippingUserinfo(t *testing.T) {
	a := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://tok@mcp.example.com/tenant1"})
	b := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://tok@mcp.example.com/tenant2"})
	if a == b {
		t.Errorf("tenants collapsed to one credential key: %q", a)
	}
}

func TestDefaultCredentialKeyBase_UnchangedWithoutUserinfo(t *testing.T) {
	got := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://mcp.example.com/tenant1"})
	if want := "mcp_mcp_example_com_tenant1"; got != want {
		t.Errorf("id for a plain URL changed: got %q want %q", got, want)
	}
}

func TestDefaultCredentialKeyBase_DropsURLQuery(t *testing.T) {
	const canary = "AIzaSy-MUST-NOT-APPEAR"

	for _, tc := range []struct{ name, url string }{
		{"api_key", "https://mcp.example.com/sse?api_key=" + canary},
		{"access_token", "https://mcp.example.com/mcp?access_token=" + canary},
		{"among other params", "https://mcp.example.com/sse?v=2&token=" + canary + "&x=1"},
		{"alongside userinfo", "https://svc:" + canary + "@mcp.example.com/sse?api_key=" + canary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: tc.url})
			if strings.Contains(got, canary) || strings.Contains(got, strings.ReplaceAll(canary, "-", "_")) {
				t.Errorf("client id carries the credential: %q", got)
			}
			if !strings.Contains(got, "mcp_example_com") {
				t.Errorf("client id lost the host, which is what makes it distinct: %q", got)
			}
		})
	}
}

func TestDefaultCredentialKeyBase_DistinctCredentialsStayDistinct(t *testing.T) {
	id := func(url string) string {
		return defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: url})
	}

	for _, tc := range []struct{ name, a, b string }{
		{"userinfo", "https://tokenA@mcp.example.com/shared", "https://tokenB@mcp.example.com/shared"},
		{"query", "https://mcp.example.com/shared?api_key=A", "https://mcp.example.com/shared?api_key=B"},
		{"password only", "https://svc:A@mcp.example.com/shared", "https://svc:B@mcp.example.com/shared"},
		{"credential in different halves", "https://A@mcp.example.com/shared", "https://mcp.example.com/shared?k=A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if a, b := id(tc.a), id(tc.b); a == b {
				t.Errorf("two credentials collapsed onto one id %q — the second toolset would run over the first's connection", a)
			}
		})
	}
}

func TestDefaultCredentialKeyBase_IsStableForTheSameURL(t *testing.T) {
	const url = "https://svc:secret@mcp.example.com/sse?api_key=abc"
	first := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: url})
	for range 5 {
		if got := defaultCredentialKeyBase(&ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: url}); got != first {
			t.Fatalf("id is not stable: %q then %q — the stored credential would be orphaned on restart", first, got)
		}
	}
}
