// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"strings"
	"testing"
)

func TestNormalizeVerificationCode(t *testing.T) {
	for _, in := range []string{"ABCD-EFGH", "abcd-efgh", " ABCDEFGH ", "ab cd-ef gh"} {
		if got := normalizeVerificationCode(in); got != "ABCDEFGH" {
			t.Errorf("normalize(%q) = %q, want ABCDEFGH", in, got)
		}
	}
}

func TestHashVerificationCode_MatchesTheStoredHashes(t *testing.T) {
	const want = "6ec1ce255309fde45195b7d76a1486431d653d056faf8c9922f8782dcf8484fb"
	if got := hashVerificationCode("alice", "slack", "ABCD-EFGH"); got != want {
		t.Fatalf("hash = %s, want %s: changing it strands every code in flight", got, want)
	}
}

func TestHashVerificationCode_BindsUserAndChannel(t *testing.T) {
	base := hashVerificationCode("alice", "slack", "ABCD-EFGH")
	if base != hashVerificationCode("alice", "slack", "abcdefgh") {
		t.Fatal("hash must ignore case and the separator")
	}
	if base == hashVerificationCode("bob", "slack", "ABCD-EFGH") || base == hashVerificationCode("alice", "teams", "ABCD-EFGH") {
		t.Fatal("hash must differ per user and per channel")
	}
	if strings.Contains(base, "ABCD") {
		t.Fatal("the stored hash must not contain the code")
	}
}

func TestValidateConfirmBaseURL(t *testing.T) {
	valid := []string{"", "https://sam.example.com/#/notifications/confirm", "http://localhost:3000/#/notifications/confirm"}
	for _, raw := range valid {
		if err := validateConfirmBaseURL(raw); err != nil {
			t.Errorf("validateConfirmBaseURL(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"https://sam.example.com/confirm",
		"https://sam.example.com/#/notifications/confirm?x=1",
		"https://sam.example.com/?x=1#/notifications/confirm",
		"/#/notifications/confirm",
		"https:///#/notifications/confirm",
		"ftp://sam.example.com/#/notifications/confirm",
		"://bad",
	}
	for _, raw := range invalid {
		if err := validateConfirmBaseURL(raw); err == nil {
			t.Errorf("validateConfirmBaseURL(%q) = nil, want an error", raw)
		}
	}
}

func TestParseNotifyConfig_RejectsDeadConfirmLink(t *testing.T) {
	_, err := ParseNotifyConfig(map[string]any{"notify": map[string]any{"confirm_base_url": "https://sam.example.com/confirm"}})
	if err == nil || !strings.Contains(err.Error(), "confirm_base_url") {
		t.Fatalf("ParseNotifyConfig with a non-hash-route base = %v, want a confirm_base_url error", err)
	}
	if _, typeErr := ParseNotifyConfig(map[string]any{"notify": map[string]any{"confirm_base_url": 42}}); typeErr == nil || !strings.Contains(typeErr.Error(), "must be a string") {
		t.Fatalf("ParseNotifyConfig with a non-string base = %v, want a type error", typeErr)
	}
	cfg, err := ParseNotifyConfig(map[string]any{"notify": map[string]any{"confirm_base_url": "https://sam.example.com/#/notifications/confirm"}})
	if err != nil || cfg.ConfirmBaseURL != "https://sam.example.com/#/notifications/confirm" {
		t.Fatalf("valid base: cfg=%+v err=%v", cfg, err)
	}
}
