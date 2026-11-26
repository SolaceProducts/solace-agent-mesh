// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"reflect"
	"strings"
	"testing"
)

func sampleReplyFixture() map[string]any {
	return map[string]any{
		"nativePreferredLanguage": "English",
		"company":                 "Acme Corp (0001)",
		"companyCode":             "0001",
		"costCenter":              "Engineering Operations",
		"businessUnit":            "Engineering (10000001)",
		"division":                "Product Engineering (10000002)",
		"department":              "Platform (10000003)",
		"location":                "HQ Office (HQ01)",
		"country":                 "United States",
		"employeeGroup":           "Full Time Employee",
		"contractType":            "Indefinite",
		"fte":                     "1",
		"manager":                 "00000000",
		"managerName":             "Manager Name",
		"startDate":               "2024-01-01",
		"dateOfBirth":             "1990-01-01",
		"gender":                  "Unspecified",
		"userFirstName":           "Alice",
		"userSurname":             "Example",
		"workEmail":               "alice@example.com",
	}
}

func sampleMappingFixture() FieldMapping {
	return FieldMapping{
		PassThroughUnmapped: true,
		DefaultValues: map[string]any{
			"country":       "",
			"employeeGroup": "",
			"jobGrade":      "",
		},
		FieldMapping: map[string]string{
			"email": "workEmail",
		},
		ComputedFields: []ComputedField{
			{Target: "displayName", SourceFields: []string{"userFirstName", "userMiddleName", "userSurname"}, Separator: " "},
			{Target: "id", SourceFields: []string{"email"}, Separator: ""},
		},
	}
}

func TestApply_ReplyProducesCanonicalProfile(t *testing.T) {
	t.Parallel()
	out := sampleMappingFixture().Apply(sampleReplyFixture())

	checks := []struct {
		key  string
		want any
	}{
		{"id", "alice@example.com"},
		{"email", "alice@example.com"},
		{"displayName", "Alice Example"},
		{"country", "United States"},
		{"employeeGroup", "Full Time Employee"},
		{"location", "HQ Office (HQ01)"},
		{"department", "Platform (10000003)"},
	}
	for _, c := range checks {
		if got := out[c.key]; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.key, got, c.want)
		}
	}
	if _, ok := out["workEmail"]; ok {
		t.Errorf("rename should have consumed workEmail; still present: %v", out["workEmail"])
	}
}

func TestApply_DefaultValuesFillMissingFields(t *testing.T) {
	t.Parallel()
	reply := map[string]any{
		"workEmail":     "u@example.com",
		"userFirstName": "U",
		"userSurname":   "Ser",
	}
	out := sampleMappingFixture().Apply(reply)
	for _, k := range []string{"country", "employeeGroup", "jobGrade"} {
		if v, ok := out[k]; !ok || v != "" {
			t.Errorf("%s: want empty-string default, got (%v, ok=%v)", k, v, ok)
		}
	}
}

func TestApply_DefaultedKeyWithoutPassthroughIsDropped(t *testing.T) {
	t.Parallel()
	mapping := sampleMappingFixture()
	mapping.PassThroughUnmapped = false
	out := mapping.Apply(map[string]any{
		"workEmail":     "u@example.com",
		"userFirstName": "U",
		"userSurname":   "Ser",
	})
	for _, k := range []string{"country", "employeeGroup", "jobGrade"} {
		if _, ok := out[k]; ok {
			t.Errorf("%s: defaulted key should not surface without passthrough/rename/computed reference", k)
		}
	}
}

func TestApply_EmptyValueAlsoTriggersDefault(t *testing.T) {
	t.Parallel()
	reply := map[string]any{
		"workEmail":     "u@example.com",
		"userFirstName": "U",
		"userSurname":   "Ser",
		"country":       "",
	}
	mapping := sampleMappingFixture()
	mapping.DefaultValues["country"] = "Unknown"
	out := mapping.Apply(reply)
	if got := out["country"]; got != "Unknown" {
		t.Errorf("empty source should be replaced by default; got %v", got)
	}
}

func TestApply_PassThroughUnmappedFalseDropsExtras(t *testing.T) {
	t.Parallel()
	mapping := sampleMappingFixture()
	mapping.PassThroughUnmapped = false
	out := mapping.Apply(sampleReplyFixture())
	if _, ok := out["company"]; ok {
		t.Errorf("company should have been dropped when pass_through_unmapped=false")
	}
	if got := out["id"]; got != "alice@example.com" {
		t.Errorf("computed id should still resolve; got %v", got)
	}
}

func TestApply_ComputedFieldSkipsMissingSources(t *testing.T) {
	t.Parallel()
	reply := map[string]any{
		"workEmail":     "u@example.com",
		"userFirstName": "Ada",
		"userSurname":   "Lovelace",
	}
	out := sampleMappingFixture().Apply(reply)
	if got := out["displayName"]; got != "Ada Lovelace" {
		t.Errorf("displayName: got %q, want %q", got, "Ada Lovelace")
	}
}

func TestValidate_DuplicateSourceRejected(t *testing.T) {
	t.Parallel()
	fm := FieldMapping{
		FieldMapping: map[string]string{
			"email":   "workEmail",
			"contact": "workEmail",
		},
	}
	msg := fm.Validate()
	if msg == "" {
		t.Fatal("Validate accepted a config with two targets sharing the same source")
	}
	for _, want := range []string{"workEmail", "contact", "email"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Validate error should mention %q; got %q", want, msg)
		}
	}
}

func TestValidate_DistinctSourcesAccepted(t *testing.T) {
	t.Parallel()
	if msg := sampleMappingFixture().Validate(); msg != "" {
		t.Errorf("Validate rejected a legitimate mapping: %s", msg)
	}
}

func TestApply_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	reply := sampleReplyFixture()
	before := make(map[string]any, len(reply))
	for k, v := range reply {
		before[k] = v
	}
	_ = sampleMappingFixture().Apply(reply)
	if !reflect.DeepEqual(reply, before) {
		t.Errorf("Apply mutated input map")
	}
}
