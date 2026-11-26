// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceheaders

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, name := range Names() {
		if _, err := Lookup(name); err != nil {
			t.Errorf("Lookup(%s) = %v", name, err)
		}
	}
	if len(Names()) != 10 {
		t.Fatalf("supported headers = %d, want the 10 the Go API can set", len(Names()))
	}

	cases := map[string]string{
		"solace_isReply":         "not supported",
		"solace_senderTimestamp": "not supported",
		"solace_bogus":           "supported headers: solace_applicationMessageId",
	}
	for name, want := range cases {
		_, err := Lookup(name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Lookup(%s) = %v, want error containing %q", name, err, want)
		}
	}
}

func TestGuaranteedOnly(t *testing.T) {
	want := map[string]bool{DMQEligible: true, TimeToLive: true, Expiration: true}
	for _, name := range Names() {
		s, _ := Lookup(name)
		if s.GuaranteedOnly != want[name] {
			t.Errorf("%s GuaranteedOnly = %v, want %v", name, s.GuaranteedOnly, want[name])
		}
	}
}

func TestCoerce(t *testing.T) {
	cases := []struct {
		header string
		in     any
		want   any
		err    string
	}{
		{DMQEligible, true, true, ""},
		{DMQEligible, "false", false, ""},
		{DMQEligible, "yes", nil, "must be true or false"},
		{TimeToLive, 3000, int64(3000), ""},
		{TimeToLive, float64(3000), int64(3000), ""},
		{TimeToLive, "3000", int64(3000), ""},
		{TimeToLive, 2.5, nil, "whole number"},
		{TimeToLive, -1, nil, "must be between 0 and 9223372036854"},
		{TimeToLive, int64(9_223_372_036_855), nil, "must be between 0 and 9223372036854"},
		{TimeToLive, int64(9_223_372_036_854), int64(9_223_372_036_854), ""},
		{Expiration, int64(1_900_000_000_000), int64(1_900_000_000_000), ""},
		{Priority, 0, 0, ""},
		{Priority, "255", 255, ""},
		{Priority, 256, nil, "between 0 and 255"},
		{SequenceNumber, 7, uint64(7), ""},
		{SequenceNumber, "18446744073709551615", uint64(18446744073709551615), ""},
		{SequenceNumber, -3, nil, "must be >= 0"},
		{CorrelationID, "abc", "abc", ""},
		{CorrelationID, 42, "42", ""},
		{CorrelationID, float64(1.5), "1.5", ""},
		{SenderID, map[string]any{}, nil, "must be a string"},
	}
	for _, c := range cases {
		s, err := Lookup(c.header)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.Coerce(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s.Coerce(%v) err = %v, want %q", c.header, c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s.Coerce(%v) = %v (%T), %v; want %v (%T)", c.header, c.in, got, got, err, c.want, c.want)
		}
	}
}

func TestValidateDeliveryMode(t *testing.T) {
	for _, ok := range []string{"", DeliveryDirect, DeliveryGuaranteed} {
		if err := ValidateDeliveryMode(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if err := ValidateDeliveryMode("persistent"); err == nil {
		t.Error("persistent must be rejected")
	}
}

func TestRuntimeAndAuthoredValueRoundTrip(t *testing.T) {
	cases := []struct {
		authored any
		runtime  any
	}{
		{true, true},
		{3000, 3000},
		{"sam", "static:sam"},
		{"corr-{{text://user_data.forward_context:id}}", "template:corr-{{text://user_data.forward_context:id}}"},
	}
	for _, c := range cases {
		if got := RuntimeValue(c.authored); got != c.runtime {
			t.Errorf("RuntimeValue(%v) = %v, want %v", c.authored, got, c.runtime)
		}
		if got := AuthoredValue(c.runtime); got != c.authored {
			t.Errorf("AuthoredValue(%v) = %v, want %v", c.runtime, got, c.authored)
		}
	}
	if got := AuthoredValue("input.payload:orderId"); got != "{{text://input.payload:orderId}}" {
		t.Errorf("AuthoredValue(bare expression) = %v", got)
	}
}

func TestAuthoredValue_LiteralWithTemplateBracesDoesNotRoundTrip(t *testing.T) {
	authored := AuthoredValue("static:a{{b")
	if authored != "a{{b" {
		t.Fatalf("AuthoredValue = %v, want a{{b", authored)
	}
	if got := RuntimeValue(authored); got != "template:a{{b" {
		t.Errorf("RuntimeValue(%v) = %v, want template:a{{b", authored, got)
	}
}

func TestValidateAuthoredValue(t *testing.T) {
	prio, _ := Lookup(Priority)
	if err := prio.ValidateAuthoredValue("{{text://input.payload:p}}"); err != nil {
		t.Errorf("template must be accepted at authoring time: %v", err)
	}
	if err := prio.ValidateAuthoredValue(9); err != nil {
		t.Errorf("9: %v", err)
	}
	if err := prio.ValidateAuthoredValue("high"); err == nil {
		t.Error(`literal "high" must be rejected for solace_priority`)
	}
}

func TestValidateUserPropertyKey(t *testing.T) {
	if err := ValidateUserPropertyKey("orderId"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "solace.messaging.message.priority"} {
		if err := ValidateUserPropertyKey(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
