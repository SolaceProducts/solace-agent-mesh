// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"strings"
	"testing"
)

func identityCfgBase() map[string]any {
	return map[string]any{
		"access_token":    "tok",
		"phone_number_id": "555111",
		"app_secret":      "secret",
		"verify_token":    "verify",
	}
}

func TestParseIdentityLookup(t *testing.T) {
	t.Run("defaults when neither key is set", func(t *testing.T) {
		cfg, err := ParseWhatsAppConfig(identityCfgBase())
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityLookupKey != IdentityLookupPhone {
			t.Errorf("IdentityLookupKey = %q, want %q", cfg.IdentityLookupKey, IdentityLookupPhone)
		}
		if cfg.IdentityClaimKey != defaultPhoneClaimKey {
			t.Errorf("IdentityClaimKey = %q, want %q", cfg.IdentityClaimKey, defaultPhoneClaimKey)
		}
	})

	t.Run("an explicit phone key and the phone claim are kept", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = "phone"
		values["identity_claim_key"] = defaultPhoneClaimKey
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityClaimKey != defaultPhoneClaimKey {
			t.Errorf("IdentityClaimKey = %q, want %q", cfg.IdentityClaimKey, defaultPhoneClaimKey)
		}
	})

	t.Run("a phone lookup against a custom claim is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = "phone"
		values["identity_claim_key"] = "extension_deadbeef_waPhone"
		_, err := ParseWhatsAppConfig(values)
		if err == nil {
			t.Fatal("a phone lookup against a custom claim was accepted; it can never match")
		}
		for _, want := range []string{"identity_claim_key", defaultPhoneClaimKey, "identity provider"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("keys are trimmed, since a claim name is free text and padding would change it", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = "  phone  "
		values["identity_claim_key"] = "  phone_number  "
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityLookupKey != "phone" || cfg.IdentityClaimKey != "phone_number" {
			t.Errorf("keys = %q/%q, want them trimmed", cfg.IdentityLookupKey, cfg.IdentityClaimKey)
		}
	})

	t.Run("a blank claim key falls back to the default", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_claim_key"] = "   "
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityClaimKey != defaultPhoneClaimKey {
			t.Errorf("IdentityClaimKey = %q, want the default", cfg.IdentityClaimKey)
		}
	})

	t.Run("wa_username is accepted when it names its claim", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = IdentityLookupWAUsername
		values["identity_claim_key"] = "extension_deadbeef_waUsername"
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityLookupKey != IdentityLookupWAUsername || cfg.IdentityClaimKey != "extension_deadbeef_waUsername" {
			t.Errorf("keys = %q/%q, want the username lookup and its claim", cfg.IdentityLookupKey, cfg.IdentityClaimKey)
		}
	})

	t.Run("wa_username without a claim is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = IdentityLookupWAUsername
		_, err := ParseWhatsAppConfig(values)
		if err == nil {
			t.Fatal("a username lookup with no claim was accepted; every sender would be refused at runtime")
		}
		if !strings.Contains(err.Error(), "identity_claim_key") {
			t.Errorf("error = %q, want it to name the missing key", err)
		}
	})

	t.Run("an unknown lookup key is refused", func(t *testing.T) {
		values := identityCfgBase()
		values["identity_lookup_key"] = "email"
		if _, err := ParseWhatsAppConfig(values); err == nil {
			t.Fatal("an unsupported lookup key was accepted")
		}
	})

	t.Run("a non-string key is refused rather than coerced", func(t *testing.T) {
		for _, field := range []string{"identity_lookup_key", "identity_claim_key"} {
			values := identityCfgBase()
			values[field] = []any{"phone"}
			if _, err := ParseWhatsAppConfig(values); err == nil {
				t.Errorf("%s accepted a list; a coerced value would match nothing", field)
			}
		}
	})

	t.Run("run_as leaves both keys unset", func(t *testing.T) {
		values := identityCfgBase()
		values["run_as"] = "ops"
		cfg, err := ParseWhatsAppConfig(values)
		if err != nil {
			t.Fatalf("ParseWhatsAppConfig: %v", err)
		}
		if cfg.IdentityLookupKey != "" || cfg.IdentityClaimKey != "" {
			t.Errorf("keys = %q/%q, want both unset — a run_as entrypoint never resolves the sender",
				cfg.IdentityLookupKey, cfg.IdentityClaimKey)
		}
	})

	t.Run("an identity key alongside run_as is refused", func(t *testing.T) {
		for _, field := range []string{"identity_lookup_key", "identity_claim_key"} {
			values := identityCfgBase()
			values["run_as"] = "ops"
			values[field] = "phone"
			if _, err := ParseWhatsAppConfig(values); err == nil {
				t.Errorf("%s alongside run_as was accepted; one of the two settings is silently ignored", field)
			}
		}
	})

	t.Run("a blank key alongside run_as is not a conflict", func(t *testing.T) {
		values := identityCfgBase()
		values["run_as"] = "ops"
		values["identity_lookup_key"] = ""
		values["identity_claim_key"] = "  "
		if _, err := ParseWhatsAppConfig(values); err != nil {
			t.Errorf("blank keys alongside run_as were refused: %v", err)
		}
	})
}

func TestUsernameLookupRefusesThePhoneClaim(t *testing.T) {
	for _, spelling := range []string{defaultPhoneClaimKey, "Phone_Number", "PHONE_NUMBER", " phone_number "} {
		t.Run(spelling, func(t *testing.T) {
			values := identityCfgBase()
			values["identity_lookup_key"] = IdentityLookupWAUsername
			values["identity_claim_key"] = spelling

			_, err := ParseWhatsAppConfig(values)
			if err == nil {
				t.Fatalf("a wa_username lookup against %q was accepted; the platform folds it to phone_number and a handle spelled from somebody else's digits would match their claim", spelling)
			}
			if !strings.Contains(err.Error(), defaultPhoneClaimKey) {
				t.Errorf("error does not name the offending claim: %v", err)
			}
		})
	}
}

func TestPhoneLookupAcceptsThePhoneClaimInAnyCase(t *testing.T) {
	values := identityCfgBase()
	values["identity_lookup_key"] = IdentityLookupPhone
	values["identity_claim_key"] = "Phone_Number"

	cfg, err := ParseWhatsAppConfig(values)
	if err != nil {
		t.Fatalf("a phone lookup against Phone_Number was refused: %v", err)
	}
	if cfg.IdentityClaimKey != "Phone_Number" {
		t.Errorf("IdentityClaimKey = %q, want the operator's spelling kept", cfg.IdentityClaimKey)
	}
}

func TestCompositeIdentityKeysAreRefused(t *testing.T) {
	for _, field := range []string{"identity_lookup_key", "identity_claim_key"} {
		for name, bad := range map[string]any{
			"list": []any{"phone"},
			"map":  map[string]any{"a": 1},
			"null": nil,
			"int":  42,
			"bool": true,
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				values := identityCfgBase()
				values[field] = bad
				if _, err := ParseWhatsAppConfig(values); err == nil {
					t.Errorf("%s = %#v was accepted; it renders as \"\", which means not configured", field, bad)
				}
			})
		}
	}
}
