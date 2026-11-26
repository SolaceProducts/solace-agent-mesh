// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package channels

import (
	"net"
	"testing"
)

func TestValidateWebhookURL_BlocksPrivateAndBadSchemes(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/hook",
		"http://10.0.0.1/hook",
		"http://169.254.169.254/latest/meta-data/",
		"ftp://example.com/hook",
		"http:///nohost",
	}
	for _, u := range blocked {
		if err := ValidateWebhookURL(u); err == nil {
			t.Errorf("expected %q to be rejected", u)
		}
	}
}

func TestValidateWebhookURL_AllowsPublicHTTPS(t *testing.T) {
	if err := ValidateWebhookURL("https://example.com/hook"); err != nil {
		t.Errorf("expected https://example.com to pass, got %v", err)
	}
}

func TestIsBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,
		"10.0.0.1":        true,
		"172.16.0.1":      true,
		"192.168.1.1":     true,
		"169.254.169.254": true,
		"::1":             true,
		"8.8.8.8":         false,
		"1.1.1.1":         false,
	}
	for ip, want := range cases {
		if got := isBlockedIP(net.ParseIP(ip)); got != want {
			t.Errorf("isBlockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestDeniedWebhookHeaders(t *testing.T) {
	for _, h := range []string{"authorization", "cookie", "host", "x-forwarded-for"} {
		if !DeniedWebhookHeaders[h] {
			t.Errorf("expected %q to be denied", h)
		}
	}
}

func TestFormatSlackPayload_HasBlocks(t *testing.T) {
	out := FormatSlackPayload(Payload{Title: "Hi", Message: "body"})
	if _, ok := out["blocks"]; !ok {
		t.Errorf("expected Slack Block Kit payload to contain 'blocks', got %v", out)
	}
}
