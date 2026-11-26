// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const testSubscription = "projects/p/subscriptions/s"

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"endpoint_url": addOnEndpointForTest})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Delivery != DeliveryHTTPS || cfg.AudienceType != AudienceEndpointURL || cfg.IdentityClaimKey != emailClaimKey || cfg.MinEditInterval != perSpaceWriteInterval {
		t.Errorf("defaults = %+v", cfg)
	}
	if cfg.ChatAPIBaseURL != defaultChatAPIBaseURL || cfg.PubSubAPIBaseURL != defaultPubSubAPIBaseURL {
		t.Errorf("default endpoints = %q, %q", cfg.ChatAPIBaseURL, cfg.PubSubAPIBaseURL)
	}
}

func TestParseConfig_Rejects(t *testing.T) {
	for name, raw := range map[string]map[string]any{
		"missing endpoint url":    {},
		"missing subscription":    {"delivery": "pubsub"},
		"bare subscription id":    {"delivery": "pubsub", "pubsub_subscription": "sub"},
		"subscription wrong kind": {"delivery": "pubsub", "pubsub_subscription": "projects/p/topics/t"},
		"unknown delivery":        {"delivery": "smoke", "pubsub_subscription": testSubscription},
		"run_as not a string":     {"delivery": "pubsub", "pubsub_subscription": testSubscription, "run_as": true},
		"bool not a bool":         {"delivery": "pubsub", "pubsub_subscription": testSubscription, "use_user_identity_in_channels": "maybe"},
		"empty claim key":         {"delivery": "pubsub", "pubsub_subscription": testSubscription, "identity_claim_key": ""},
		"negative interval":       {"delivery": "pubsub", "pubsub_subscription": testSubscription, "min_edit_interval_ms": -1},
		"interval over the cap":   {"delivery": "pubsub", "pubsub_subscription": testSubscription, "min_edit_interval_ms": MaxMinEditIntervalMillis + 1},
		"interval overflowing":    {"delivery": "pubsub", "pubsub_subscription": testSubscription, "min_edit_interval_ms": "9223372036854775807"},
		"domain without a dot":    {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": []any{"gmail"}},
		"domain with a path":      {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": []any{"b.com/x"}},
		"domains only a comma":    {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": ","},
		"domains only an at":      {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": "@"},
		"domains only separators": {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": " , "},
		"domains list of blanks":  {"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": []any{"@", " "}},
		"add-on signer with project number audience": {
			"delivery": "https", "project_number": "1", "addon_service_account_email": addOnSignerForTest,
		},
		"add-on signer not google managed": {
			"delivery": "https", "auth_audience_type": "endpoint_url", "endpoint_url": "https://sam.example.com/gw/gc/events",
			"addon_service_account_email": "attacker@evil-project.iam.gserviceaccount.com",
		},
		"skip auth off loopback": {
			"delivery": "pubsub", "pubsub_subscription": testSubscription, "test_skip_google_auth": true,
			"api_base_url": "https://chat.googleapis.com", "pubsub_base_url": "http://127.0.0.1:1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(raw); err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
				t.Errorf("ParseConfig(%v) err = %v, want a google_chat config error", raw, err)
			}
		})
	}
}

func TestParseConfig_Overrides(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"delivery":                      "pubsub",
		"pubsub_subscription":           testSubscription,
		"project_number":                123456789012,
		"allowed_email_domains":         "Example.com, @corp.example.com",
		"use_user_identity_in_channels": "true",
		"min_edit_interval_ms":          250,
		"api_base_url":                  "http://127.0.0.1:9",
		"pubsub_base_url":               "http://localhost:9/",
		"test_skip_google_auth":         true,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ProjectNumber != "123456789012" {
		t.Errorf("project number = %q, want an unquoted number rendered as a string", cfg.ProjectNumber)
	}
	if strings.Join(cfg.AllowedEmailDomains, ",") != "example.com,corp.example.com" {
		t.Errorf("domains = %v", cfg.AllowedEmailDomains)
	}
	if !cfg.UseUserIdentityInChannels || cfg.MinEditInterval != 250*time.Millisecond || cfg.ChatAPIBaseURL != "http://127.0.0.1:9/" {
		t.Errorf("overrides = %+v", cfg)
	}
}

func TestParseConfig_NullMeansAbsent(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"delivery":               "pubsub",
		"pubsub_subscription":    testSubscription,
		"identity_claim_key":     nil,
		"min_edit_interval_ms":   nil,
		"initial_status_message": nil,
		"allowed_email_domains":  nil,
		"service_account_key":    nil,
	})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.IdentityClaimKey != emailClaimKey || cfg.MinEditInterval != perSpaceWriteInterval ||
		cfg.InitialStatusMessage != defaultInitialStatusText || cfg.AllowedEmailDomains != nil || cfg.ServiceAccountKey != "" {
		t.Errorf("nulls parsed to %+v, want the defaults", cfg)
	}
}

func TestParseConfig_BlankDomainListAllowsEveryDomain(t *testing.T) {
	for name, domains := range map[string]any{"empty string": "", "whitespace": "  ", "empty list": []any{}} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig(map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "allowed_email_domains": domains})
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if !cfg.emailDomainAllowed("ada@anywhere.example") {
				t.Errorf("an unset allowlist refused a sender")
			}
		})
	}
}

func TestParseConfig_IntervalAtTheCap(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "min_edit_interval_ms": MaxMinEditIntervalMillis})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.MinEditInterval != time.Minute {
		t.Errorf("interval = %v, want %v", cfg.MinEditInterval, time.Minute)
	}
}

func TestParseConfig_AcceptsGoogleServiceAccountKey(t *testing.T) {
	key := googleServiceAccountKey(t)
	cfg, err := ParseConfig(map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "service_account_key": key})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ServiceAccountKey != key {
		t.Error("service_account_key did not round-trip")
	}
}

func TestParseConfig_RejectsUnusableServiceAccountKey(t *testing.T) {
	valid := googleServiceAccountKey(t)
	for name, key := range map[string]string{
		"foreign token_uri":     withKeyField(t, valid, "token_uri", "https://attacker.example/token"),
		"truncated private key": truncatedPrivateKey(t, valid),
		"not a service account": withKeyField(t, valid, "type", "authorized_user"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig(map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "service_account_key": key})
			if err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
				t.Fatalf("ParseConfig err = %v, want a google_chat config error", err)
			}
			assertNoKeyMaterial(t, err.Error(), key)
		})
	}
}

func TestParseConfig_NonStringServiceAccountKeyDoesNotEchoIt(t *testing.T) {
	key := googleServiceAccountKey(t)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(key), &decoded); err != nil {
		t.Fatal(err)
	}
	_, err := ParseConfig(map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription, "service_account_key": decoded})
	if err == nil {
		t.Fatal("a map-shaped service_account_key was accepted")
	}
	assertNoKeyMaterial(t, err.Error(), key)
}

func TestParseEvent_DirectMessage(t *testing.T) {
	ev, err := parseEvent([]byte(`{
		"type": "MESSAGE",
		"space": {"name": "spaces/D", "type": "DM", "spaceType": "DIRECT_MESSAGE", "singleUserBotDm": true},
		"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
		"message": {"name": "spaces/D/messages/M", "text": " hello ", "thread": {"name": "spaces/D/threads/T"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != kindMessage || !ev.IsDM || !ev.Addressed || ev.Text != "hello" || ev.EventKey != "spaces/D/messages/M" || ev.ThreadName != "spaces/D/threads/T" {
		t.Errorf("event = %+v", ev)
	}
}

func TestParseEvent_SpaceMentionStripsMentionAndUnmentionedIsNotAddressed(t *testing.T) {
	mentioned, _ := parseEvent([]byte(`{
		"type": "MESSAGE",
		"space": {"name": "spaces/S", "type": "ROOM", "spaceType": "SPACE"},
		"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
		"message": {"name": "spaces/S/messages/M", "text": "@SAM what now?", "argumentText": " what now?",
			"annotations": [{"type": "USER_MENTION", "userMention": {"user": {"name": "users/app", "type": "BOT"}}}]}
	}`))
	if mentioned.IsDM || !mentioned.Addressed || mentioned.Text != "what now?" {
		t.Errorf("mentioned = %+v, want addressed with the mention stripped", mentioned)
	}

	unmentioned, _ := parseEvent([]byte(`{
		"type": "MESSAGE",
		"space": {"name": "spaces/S", "spaceType": "SPACE"},
		"user": {"name": "users/1", "email": "ada@example.com", "type": "HUMAN"},
		"message": {"name": "spaces/S/messages/M2", "text": "just chatting"}
	}`))
	if unmentioned.Addressed {
		t.Errorf("unmentioned space message = %+v, want not addressed", unmentioned)
	}
}

func TestParseEvent_UnknownSpaceTypeCountsAsMultiParty(t *testing.T) {
	for name, space := range map[string]string{
		"missing":                     `{"name": "spaces/X"}`,
		"direct message, not 1:1 bot": `{"name": "spaces/X", "spaceType": "DIRECT_MESSAGE"}`,
		"group chat":                  `{"name": "spaces/X", "spaceType": "GROUP_CHAT"}`,
	} {
		t.Run(name, func(t *testing.T) {
			ev, _ := parseEvent([]byte(`{"type": "MESSAGE", "space": ` + space + `,
				"user": {"name": "users/1", "type": "HUMAN"}, "message": {"name": "spaces/X/messages/M", "text": "hi"}}`))
			if ev.IsDM {
				t.Errorf("IsDM = true for %s, want multi-party (fail closed)", name)
			}
		})
	}
}

func TestParseEvent_IgnoresBotsAndMembershipEvents(t *testing.T) {
	for name, payload := range map[string]string{
		"bot sender":  `{"type": "MESSAGE", "user": {"type": "BOT"}, "message": {"name": "spaces/S/messages/M"}}`,
		"added":       `{"type": "ADDED_TO_SPACE", "user": {"type": "HUMAN"}}`,
		"unknown":     `{"type": "APP_HOME", "user": {"type": "HUMAN"}}`,
		"no msg name": `{"type": "MESSAGE", "user": {"type": "HUMAN"}, "message": {"text": "hi"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ev, err := parseEvent([]byte(payload))
			if err != nil || ev.Kind != kindIgnored {
				t.Errorf("parseEvent = (%+v, %v), want ignored", ev, err)
			}
		})
	}
	if _, err := parseEvent([]byte("not json")); err == nil {
		t.Error("an undecodable payload must be an error")
	}
}

func TestBuildExternalKey(t *testing.T) {
	dm := buildExternalKey(&inboundEvent{IsDM: true, SpaceName: "spaces/D", ThreadName: "spaces/D/threads/T"})
	if dm != "dm:spaces/D:spaces/D/threads/T" {
		t.Errorf("dm key = %q", dm)
	}
	unthreaded := buildExternalKey(&inboundEvent{IsDM: true, SpaceName: "spaces/D", MessageName: "spaces/D/messages/M"})
	if unthreaded != "dm:spaces/D:spaces/D/messages/M" {
		t.Errorf("dm key without a thread = %q, want the message's own session", unthreaded)
	}
	thread := buildExternalKey(&inboundEvent{SpaceName: "spaces/S", ThreadName: "spaces/S/threads/T"})
	if thread != "space:spaces/S:spaces/S/threads/T" {
		t.Errorf("space key = %q", thread)
	}
	long := buildExternalKey(&inboundEvent{SpaceName: "spaces/S", ThreadName: strings.Repeat("t", 200)})
	if len(long) > maxExternalKeyLen || !strings.HasPrefix(long, "gc:") {
		t.Errorf("long key = %q, want hashed under the limit", long)
	}
}

func TestRetryBudgets(t *testing.T) {
	if budgetSpent(claimWith(0, "", 0), outcomeUnauthorized, false) {
		t.Error("the first unauthorized attempt must not spend the budget")
	}
	if !budgetSpent(claimWith(maxAuthzDenialAttempts-1, "unauthorized", maxAuthzDenialAttempts-1), outcomeUnauthorized, false) {
		t.Error("the sixth consecutive unauthorized attempt must spend the budget")
	}
	if budgetSpent(claimWith(maxAuthzDenialAttempts-1, "transient", maxAuthzDenialAttempts-1), outcomeUnauthorized, false) {
		t.Error("a transient streak must not spend the unauthorized budget")
	}
	if !budgetSpent(claimWith(1, "transient", maxTransientAttempts-1), outcomeAuthzUnavailable, false) {
		t.Error("the absolute ceiling must bind whatever the streak")
	}
	if got := retryPace(100); got != maxTransientRetryDelay {
		t.Errorf("retryPace(100) = %v, want the cap", got)
	}
	if budgetSpent(claimWith(maxClickAttempts-2, "transient", maxClickAttempts-2), outcomeTransient, true) {
		t.Error("a click must be retried until its own budget is spent")
	}
	if !budgetSpent(claimWith(maxClickAttempts-1, "transient", maxClickAttempts-1), outcomeTransient, true) {
		t.Error("a transient click must give up at the click budget, not the transient one")
	}
	if !budgetSpent(claimWith(1, "transient", maxClickAttempts-1), outcomeAuthzUnavailable, true) {
		t.Error("a click's ceiling is the click budget whatever the streak")
	}
	if !budgetSpent(claimWith(stalledRetryBudget-1, store.StalledClaimFailure, stalledRetryBudget-1), outcomeStalled, true) {
		t.Error("a stalled click keeps the stalled budget when it is the smaller")
	}
	if budgetSpent(claimWith(maxClickAttempts-1, "transient", maxClickAttempts-1), outcomeTransient, false) {
		t.Error("a message keeps the transient budget")
	}
}

func claimWith(attempts int, kind string, total int) store.DedupClaim {
	return store.DedupClaim{Outcome: store.ClaimAcquired, Attempts: attempts, FailureKind: kind, AttemptsTotal: total}
}
