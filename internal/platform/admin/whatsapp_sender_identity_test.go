// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"testing"
)

func senderIdentityValues(extra map[string]any) map[string]any {
	values := whatsappValues()
	maps.Copy(values, extra)
	return values
}

func TestSenderIdentityModeIsDerivedFromRunAs(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		values map[string]any
		want   string
	}{
		"a stored run_as means the swap": {
			values: map[string]any{"run_as": "ops"},
			want:   SenderIdentitySystemUser,
		},
		"no run_as means a lookup": {
			values: map[string]any{},
			want:   SenderIdentityIDPLookup,
		},
		"a blank run_as means a lookup": {
			values: map[string]any{"run_as": ""},
			want:   SenderIdentityIDPLookup,
		},
		"whitespace is not a system user": {
			values: map[string]any{"run_as": "   "},
			want:   SenderIdentityIDPLookup,
		},
		"an explicit mode wins over the derivation": {
			values: map[string]any{"run_as": "ops", SenderIdentityModeKey: SenderIdentityIDPLookup},
			want:   SenderIdentityIDPLookup,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			normalized := normalizeValuesForType("whatsapp", senderIdentityValues(tc.values))
			if got := normalized[SenderIdentityModeKey]; got != tc.want {
				t.Errorf("%s = %v, want %q", SenderIdentityModeKey, got, tc.want)
			}
		})
	}
}

func TestMergeKeepsRunAsOnALegacyPartialPatch(t *testing.T) {
	t.Parallel()

	legacyStored := senderIdentityValues(map[string]any{"run_as": "ops"})
	delete(legacyStored, SenderIdentityModeKey)

	incoming := map[string]any{"default_agent_name": "Support"}

	merged, err := mergeGatewayValuesForUpdate("whatsapp",
		incoming, normalizeValuesForType("whatsapp", legacyStored), nil)
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	if merged["run_as"] != "ops" {
		t.Errorf("run_as = %v, want the stored ops preserved — an unrelated edit must not retire the swap", merged["run_as"])
	}
}

func TestMergeDropsRunAsWhenTheModeSwitchesToALookup(t *testing.T) {
	t.Parallel()

	stored := normalizeValuesForType("whatsapp", senderIdentityValues(map[string]any{"run_as": "ops"}))
	if stored[SenderIdentityModeKey] != SenderIdentitySystemUser {
		t.Fatalf("fixture is not in the system-user mode: %v", stored[SenderIdentityModeKey])
	}

	authored := normalizeValuesForType("whatsapp", senderIdentityValues(nil))

	merged, err := mergeGatewayValuesForUpdate("whatsapp", authored, stored, nil)
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	if runAs, present := merged["run_as"]; present && runAs != "" {
		t.Errorf("run_as = %v, want it dropped — removing it from the config has to disable it", runAs)
	}
	if err := validateWhatsAppValues(merged); err != nil {
		t.Errorf("the merged bag does not validate: %v", err)
	}
}

func TestMergeDropsTheLookupKeysUnderTheSystemUserMode(t *testing.T) {
	t.Parallel()

	stored := normalizeValuesForType("whatsapp", senderIdentityValues(map[string]any{
		"identity_lookup_key":               "phone",
		"identity_claim_key":                "phone_number",
		"contact_request_template":          "request_contact_info_1",
		"contact_request_template_language": "en_US",
	}))

	incoming := map[string]any{SenderIdentityModeKey: SenderIdentitySystemUser, "run_as": "ops"}

	merged, err := mergeGatewayValuesForUpdate("whatsapp", incoming, stored, nil)
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}
	for _, field := range []string{
		"identity_lookup_key", "identity_claim_key",
		"contact_request_template", "contact_request_template_language",
	} {
		if v, present := merged[field]; present && v != "" {
			t.Errorf("%s = %v, want it dropped under the system-user mode", field, v)
		}
	}
	if err := validateWhatsAppValues(merged); err != nil {
		t.Errorf("the merged bag does not validate: %v", err)
	}
}

func TestSenderIdentityModeValidation(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		values    map[string]any
		wantField string
	}{
		"the system-user mode needs a system user": {
			values:    map[string]any{SenderIdentityModeKey: SenderIdentitySystemUser},
			wantField: "run_as",
		},
		"a lookup cannot carry a run_as": {
			values:    map[string]any{SenderIdentityModeKey: SenderIdentityIDPLookup, "run_as": "ops"},
			wantField: "run_as",
		},
		"an unknown mode is refused": {
			values:    map[string]any{SenderIdentityModeKey: "whatever"},
			wantField: SenderIdentityModeKey,
		},
		"a non-string mode is refused": {
			values:    map[string]any{SenderIdentityModeKey: []any{"idp_lookup"}},
			wantField: SenderIdentityModeKey,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := validateWhatsAppValues(senderIdentityValues(tc.values))
			if err == nil {
				t.Fatalf("accepted %v, want a validation error", tc.values)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error = %T (%v), want *ValidationError", err, err)
			}
			if len(ve.Details[tc.wantField]) == 0 {
				t.Errorf("no detail on %q; details = %v", tc.wantField, ve.Details)
			}
		})
	}

	for name, values := range map[string]map[string]any{
		"the system user with its user":  {SenderIdentityModeKey: SenderIdentitySystemUser, "run_as": "ops"},
		"a lookup with no run_as":        {SenderIdentityModeKey: SenderIdentityIDPLookup},
		"a legacy bag with only run_as":  {"run_as": "ops"},
		"a legacy bag with nothing much": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateWhatsAppValues(senderIdentityValues(values)); err != nil {
				t.Errorf("refused %v: %v", values, err)
			}
		})
	}
}

func TestReadPathStampsTheSenderIdentityMode(t *testing.T) {
	t.Parallel()

	legacy := senderIdentityValues(map[string]any{"run_as": "ops"})
	delete(legacy, SenderIdentityModeKey)

	stamped := normalizeValuesForType("whatsapp", legacy)
	if got := stamped[SenderIdentityModeKey]; got != SenderIdentitySystemUser {
		t.Errorf("%s = %v, want %q — an unstamped legacy row loses its run_as on the next save",
			SenderIdentityModeKey, got, SenderIdentitySystemUser)
	}
	if got := stamped["run_as"]; got != "ops" {
		t.Errorf("run_as = %v, want it preserved", got)
	}
}
