// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oauth2/google"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const validGoogleChatSubscription = "projects/p/subscriptions/s"

const googleChatKeyClientEmail = "bot@p.iam.gserviceaccount.com"

func googleChatServiceAccountKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": googleChatKeyClientEmail,
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    google.JWTTokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(key)
}

func googleChatKeyFields(t *testing.T, key string) map[string]string {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal([]byte(key), &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func withGoogleChatKeyField(t *testing.T, key, field, value string) string {
	t.Helper()
	fields := googleChatKeyFields(t, key)
	fields[field] = value
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestValidateGoogleChatValues_AcceptsValidBags(t *testing.T) {
	for name, values := range map[string]map[string]any{
		"minimal": {"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription},
		"domains as a list": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"allowed_email_domains": []any{"example.com", "@example.org"},
		},
		"numeric project number and string interval": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"project_number":       float64(123456789012),
			"min_edit_interval_ms": "0",
		},
		"boolean as a string": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"use_user_identity_in_channels": "true",
		},
		"service account key": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"service_account_key": googleChatServiceAccountKey(t),
		},
		"placeholder subscription": {"delivery": "pubsub", "pubsub_subscription": "<<__SAM_PUBSUB_SUBSCRIPTION__>>"},
		"placeholder key": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"service_account_key": "<<__SAM_SECRET_GOOGLE_CHAT__>>",
		},
		"a drive folder": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"artifact_drive_folder_id": " 0AExampleSharedDrive ", "artifact_drive_access_days": float64(365),
		},
		"a blank drive folder": {"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "artifact_drive_folder_id": " "},
		"access days as a string": {
			"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
			"artifact_drive_folder_id": "0AExampleSharedDrive", "artifact_drive_access_days": "1",
		},
		"blank domain list":  {"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "allowed_email_domains": " "},
		"interval at cap":    {"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "min_edit_interval_ms": float64(60_000)},
		"null optional keys": {"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "identity_claim_key": nil, "min_edit_interval_ms": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateGatewayValues("google_chat", values); err != nil {
				t.Errorf("valid bag rejected: %s", describeValidationError(err))
			}
		})
	}
}

func TestValidateGoogleChatValues_RejectsSubscriptionShapes(t *testing.T) {
	for name, subscription := range map[string]any{
		"missing":          nil,
		"empty":            "",
		"bare id":          "chat-events",
		"no project":       "projects//subscriptions/s",
		"topic, not a sub": "projects/p/topics/chat",
		"nested sub":       "projects/p/subscriptions/s/extra",
		"not a string":     42,
	} {
		t.Run(name, func(t *testing.T) {
			values := map[string]any{"delivery": "pubsub"}
			if subscription != nil {
				values["pubsub_subscription"] = subscription
			}
			details := googleChatDetails(t, values)
			if len(details["pubsub_subscription"]) == 0 {
				t.Errorf("pubsub_subscription %v accepted; details = %v", subscription, details)
			}
		})
	}
}

func TestValidateGoogleChatValues_RequiresTheFieldsOfItsDelivery(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	if details := googleChatDetails(t, map[string]any{}); len(details["endpoint_url"]) == 0 || len(details["pubsub_subscription"]) != 0 {
		t.Errorf("default delivery details = %v, want endpoint_url required and no subscription error", details)
	}
	if details := googleChatDetails(t, map[string]any{"delivery": "pubsub"}); len(details["pubsub_subscription"]) == 0 || len(details["endpoint_url"]) != 0 {
		t.Errorf("pubsub delivery details = %v, want pubsub_subscription required and no endpoint_url error", details)
	}
	if err := validateGoogleChatValues(map[string]any{"endpoint_url": "https://sam.example.com/gw/gc/events"}); err != nil {
		t.Errorf("default HTTPS bag rejected: %v", err)
	}
}

func TestValidateGoogleChatValues_EndpointOptionalWhenDerivable(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	if err := validateGoogleChatValues(map[string]any{}); err != nil {
		t.Errorf("HTTPS bag without endpoint_url rejected although it can be derived: %v", err)
	}
}

func TestValidateGoogleChatValues_CollectsEveryFieldError(t *testing.T) {
	details := googleChatDetails(t, map[string]any{
		"delivery":                      "carrier_pigeon",
		"project_number":                []any{"1"},
		"service_account_key":           `{"type": "authorized_user"}`,
		"allowed_email_domains":         "example.com, gmail",
		"use_user_identity_in_channels": "yes",
		"feedback_enabled":              float64(1),
		"min_edit_interval_ms":          float64(-5),
		"identity_claim_key":            "   ",
		"run_as":                        []any{"ops"},
	})
	want := []string{
		"delivery",
		"project_number",
		"service_account_key",
		"allowed_email_domains",
		"use_user_identity_in_channels",
		"feedback_enabled",
		"min_edit_interval_ms",
		"identity_claim_key",
		"run_as",
	}
	for _, field := range want {
		if len(details[field]) != 1 {
			t.Errorf("errors for %s = %v, want exactly one", field, details[field])
		}
	}
	for field := range details {
		if !slices.Contains(want, field) {
			t.Errorf("unexpected error on %s: %v", field, details[field])
		}
	}
}

func TestValidateGoogleChatValues_RejectsFractionalInterval(t *testing.T) {
	details := googleChatDetails(t, map[string]any{
		"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
		"min_edit_interval_ms": 12.5,
	})
	if len(details["min_edit_interval_ms"]) == 0 {
		t.Errorf("fractional min_edit_interval_ms accepted; the adapter refuses it at startup")
	}
}

func TestValidateGoogleChatValues_ThreadHistoryLimitRange(t *testing.T) {
	for _, ok := range []any{float64(0), float64(googlechat.MaxThreadHistoryLimit), "25"} {
		if err := validateGatewayValues("google_chat", map[string]any{"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "thread_history_limit": ok}); err != nil {
			t.Errorf("thread_history_limit %v refused: %s", ok, describeValidationError(err))
		}
	}
	for _, bad := range []any{float64(-1), float64(googlechat.MaxThreadHistoryLimit + 1), 2.5, "many", true} {
		details := googleChatDetails(t, map[string]any{"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription, "thread_history_limit": bad})
		if len(details["thread_history_limit"]) == 0 {
			t.Errorf("thread_history_limit %v accepted; the adapter refuses it at startup", bad)
		}
	}
}

func TestGoogleChatThreadHistoryFormMaxMatchesAdapterCap(t *testing.T) {
	def, _ := platpkg.LookupGatewayType("google_chat")
	for _, f := range def.Fields {
		if f.Name == "thread_history_limit" {
			if f.MaxValue == nil || *f.MaxValue != googlechat.MaxThreadHistoryLimit {
				t.Errorf("form max = %v, want the adapter cap %d", f.MaxValue, googlechat.MaxThreadHistoryLimit)
			}
			return
		}
	}
	t.Fatal("thread_history_limit is not a google_chat form field")
}

func TestValidateGoogleChatValues_RejectsMalformedServiceAccountKey(t *testing.T) {
	details := googleChatDetails(t, map[string]any{
		"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
		"service_account_key": "not json",
	})
	if len(details["service_account_key"]) == 0 {
		t.Error("a non-JSON service_account_key was accepted; the adapter refuses it at startup")
	}
}

func TestValidateGoogleChatValues_RejectsUnusableServiceAccountKey(t *testing.T) {
	valid := googleChatServiceAccountKey(t)
	privateKey := googleChatKeyFields(t, valid)["private_key"]
	for name, key := range map[string]string{
		"foreign token_uri":     withGoogleChatKeyField(t, valid, "token_uri", "https://attacker.example/token"),
		"truncated private key": withGoogleChatKeyField(t, valid, "private_key", privateKey[:200]),
	} {
		t.Run(name, func(t *testing.T) {
			details := googleChatDetails(t, map[string]any{
				"pubsub_subscription": validGoogleChatSubscription,
				"service_account_key": key,
			})
			problems := details["service_account_key"]
			if len(problems) == 0 {
				t.Fatalf("key accepted; details = %v", details)
			}
			text := strings.Join(problems, " ")
			body := strings.TrimPrefix(privateKey, "-----BEGIN PRIVATE KEY-----\n")
			for _, secret := range []string{body[:40], "PRIVATE KEY", googleChatKeyClientEmail, "attacker.example"} {
				if strings.Contains(text, secret) {
					t.Errorf("problem %q leaks key material %q", text, secret)
				}
			}
		})
	}
}

func TestValidateGoogleChatValues_RejectsDomainListNamingNoDomain(t *testing.T) {
	for name, domains := range map[string]any{
		"comma":       ",",
		"at":          "@",
		"separators":  " , ",
		"blank items": []any{"@", " "},
	} {
		t.Run(name, func(t *testing.T) {
			details := googleChatDetails(t, map[string]any{
				"pubsub_subscription":   validGoogleChatSubscription,
				"allowed_email_domains": domains,
			})
			if len(details["allowed_email_domains"]) == 0 {
				t.Errorf("allowed_email_domains %q accepted; the adapter would allow every domain", domains)
			}
		})
	}
}

func TestValidateGoogleChatValues_RejectsIntervalOverCap(t *testing.T) {
	for name, interval := range map[string]any{
		"just over":    float64(60_001),
		"int64 max":    "9223372036854775807",
		"huge float":   1e300,
		"over as int":  60_001,
		"over as text": "60001",
	} {
		t.Run(name, func(t *testing.T) {
			details := googleChatDetails(t, map[string]any{
				"pubsub_subscription":  validGoogleChatSubscription,
				"min_edit_interval_ms": interval,
			})
			if len(details["min_edit_interval_ms"]) == 0 {
				t.Errorf("min_edit_interval_ms %v accepted; the adapter refuses it at startup", interval)
			}
		})
	}
}

func googleChatDetails(t *testing.T, values map[string]any) map[string][]string {
	t.Helper()
	err := validateGatewayValues("google_chat", values)
	if err == nil {
		t.Fatalf("values %v were accepted", values)
	}
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return verr.Details
}

func TestValidateGoogleChatValues_RejectsDriveSettings(t *testing.T) {
	for name, tc := range map[string]struct {
		field string
		value any
	}{
		"a folder link":       {"artifact_drive_folder_id", "https://drive.google.com/drive/folders/0AExampleSharedDrive"},
		"a short folder id":   {"artifact_drive_folder_id", "abc"},
		"a folder id number":  {"artifact_drive_folder_id", float64(12345678901234)},
		"no days":             {"artifact_drive_access_days", float64(0)},
		"too many days":       {"artifact_drive_access_days", float64(366)},
		"fractional days":     {"artifact_drive_access_days", 1.5},
		"days that are words": {"artifact_drive_access_days", "a month"},
	} {
		t.Run(name, func(t *testing.T) {
			details := googleChatDetails(t, map[string]any{
				"delivery": "pubsub", "pubsub_subscription": validGoogleChatSubscription,
				"artifact_drive_folder_id": "0AExampleSharedDrive", tc.field: tc.value,
			})
			if len(details[tc.field]) != 1 {
				t.Errorf("errors for %s = %v, want exactly one", tc.field, details[tc.field])
			}
		})
	}
}
