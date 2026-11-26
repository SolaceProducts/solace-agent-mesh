// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var expectedDataKeys = []string{
	"current_time",
	"timezone",
	"timezone_offset",
	"timezone_abbreviation",
	"formatted_time",
	"timestamp",
	"date",
	"time",
	"day_of_week",
}

func TestGetCurrentDatetime_ZeroArgs_DefaultsToUTC(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}

	result, err := tt.Execute(t.Context(), nil, &tool.Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success (message=%q)", result.Status, result.Message)
	}
	if got := result.Data["timezone"]; got != "UTC" {
		t.Errorf("timezone = %v, want UTC", got)
	}
	assertAllNineFieldsPresent(t, result.Data)
}

func TestGetCurrentDatetime_SchemaHasNoArgs(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}
	if tt.Name() != "get_current_time" {
		t.Errorf("tool name = %q, want get_current_time", tt.Name())
	}
	params := tt.Parameters()
	if params == nil {
		t.Fatalf("parameters schema is nil")
	}
	if len(params.Required) != 0 {
		t.Errorf("tool should take no required args, got %v", params.Required)
	}
	if len(params.Properties) != 0 {
		t.Errorf("tool should take no declared properties, got %v", params.Properties)
	}
}

func TestGetCurrentDatetime_SessionTimezone_AmericaToronto(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}
	toolCtx := &tool.Context{A2AUserConfig: `{"user_timezone":"America/Toronto"}`}

	result, err := tt.Execute(t.Context(), nil, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if got := result.Data["timezone"]; got != "America/Toronto" {
		t.Errorf("timezone = %v, want America/Toronto", got)
	}

	offset, ok := result.Data["timezone_offset"].(string)
	if !ok {
		t.Fatalf("timezone_offset is not a string: %T", result.Data["timezone_offset"])
	}
	if offset[:1] != "-" {
		t.Errorf("Toronto offset should be negative, got %q", offset)
	}
	if matched, _ := regexp.MatchString(`^[+-]\d{2}:\d{2}$`, offset); !matched {
		t.Errorf("timezone_offset %q does not match +HH:MM or -HH:MM", offset)
	}

	assertAllNineFieldsPresent(t, result.Data)
}

func TestGetCurrentDatetime_InvalidTimezone_FallsBackToUTC(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}
	toolCtx := &tool.Context{A2AUserConfig: `{"user_timezone":"Not/A_Real_Zone"}`}

	result, err := tt.Execute(t.Context(), nil, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success (invalid-tz must fall back, not error); message=%q",
			result.Status, result.Message)
	}
	if got := result.Data["timezone"]; got != "UTC" {
		t.Errorf("timezone = %v, want UTC fallback", got)
	}
	if got := result.Data["timezone_offset"]; got != "+00:00" {
		t.Errorf("timezone_offset = %v, want +00:00 on UTC fallback", got)
	}
	assertAllNineFieldsPresent(t, result.Data)
}

func TestGetCurrentDatetime_AllNineFieldsShape(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}
	result, err := tt.Execute(t.Context(), nil, &tool.Context{
		A2AUserConfig: `{"user_timezone":"UTC"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	assertAllNineFieldsPresent(t, result.Data)

	current, _ := result.Data["current_time"].(string)
	if _, err := time.Parse(time.RFC3339Nano, current); err != nil {
		t.Errorf("current_time does not parse as RFC3339Nano: %v (value=%q)", err, current)
	}
	if !regexp.MustCompile(`T\d{2}:\d{2}:\d{2}\.\d{6}(?:Z|[+-]\d{2}:\d{2})$`).MatchString(current) {
		t.Errorf("current_time must carry 6-digit microseconds and Z or ±HH:MM offset, got %q", current)
	}

	date, _ := result.Data["date"].(string)
	if _, err := time.Parse("2006-01-02", date); err != nil {
		t.Errorf("date does not parse as YYYY-MM-DD: %v (value=%q)", err, date)
	}

	timeOnly, _ := result.Data["time"].(string)
	if _, err := time.Parse("15:04:05", timeOnly); err != nil {
		t.Errorf("time does not parse as HH:MM:SS: %v (value=%q)", err, timeOnly)
	}

	ts, ok := result.Data["timestamp"].(int64)
	if !ok {
		t.Fatalf("timestamp type = %T, want int64", result.Data["timestamp"])
	}
	delta := time.Now().Unix() - ts
	if delta < -5 || delta > 5 {
		t.Errorf("timestamp %d is more than 5 seconds off from now", ts)
	}

	dow, _ := result.Data["day_of_week"].(string)
	switch dow {
	case "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday":
	default:
		t.Errorf("day_of_week = %q, want a weekday name", dow)
	}

	if got := result.Data["timezone_offset"]; got != "+00:00" {
		t.Errorf("UTC timezone_offset = %v, want +00:00", got)
	}
	if got := result.Data["timezone_abbreviation"]; got != "UTC" {
		t.Errorf("UTC timezone_abbreviation = %v, want UTC", got)
	}
}

func TestGetCurrentDatetime_CurrentTimeMicrosecondPrecision(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}

	cases := []struct {
		name          string
		config        string
		suffixPattern string
	}{
		{
			name:          "UTC emits +00:00 suffix (not literal Z)",
			config:        `{"user_timezone":"UTC"}`,
			suffixPattern: `T\d{2}:\d{2}:\d{2}\.\d{6}\+00:00$`,
		},
		{
			name:          "America/New_York emits -HH:MM suffix",
			config:        `{"user_timezone":"America/New_York"}`,
			suffixPattern: `T\d{2}:\d{2}:\d{2}\.\d{6}-\d{2}:\d{2}$`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tt.Execute(t.Context(), nil, &tool.Context{A2AUserConfig: tc.config})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			current, _ := result.Data["current_time"].(string)
			re := regexp.MustCompile(tc.suffixPattern)
			if !re.MatchString(current) {
				t.Errorf("current_time %q does not match %s", current, tc.suffixPattern)
			}
		})
	}
}

func TestGetCurrentDatetime_MessageIncludesTimezoneAndFormattedTime(t *testing.T) {
	tt := &GetCurrentDatetimeTool{}
	result, err := tt.Execute(t.Context(), nil, &tool.Context{
		A2AUserConfig: `{"user_timezone":"UTC"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Message == "" {
		t.Fatal("message is empty")
	}
	formatted, _ := result.Data["formatted_time"].(string)
	if formatted == "" || !strings.Contains(result.Message, formatted) {
		t.Errorf("message %q should contain formatted_time %q", result.Message, formatted)
	}
	if !strings.Contains(result.Message, "UTC") {
		t.Errorf("message %q should mention timezone UTC", result.Message)
	}
}

func TestFormatTimezoneOffset(t *testing.T) {
	loc := time.FixedZone("test", 5*3600+30*60)
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, loc)
	if got := formatTimezoneOffset(ts); got != "+05:30" {
		t.Errorf("+05:30 expected, got %q", got)
	}

	loc = time.FixedZone("test", -5*3600)
	ts = time.Date(2024, 1, 1, 0, 0, 0, 0, loc)
	if got := formatTimezoneOffset(ts); got != "-05:00" {
		t.Errorf("-05:00 expected, got %q", got)
	}

	if got := formatTimezoneOffset(time.Now().UTC()); got != "+00:00" {
		t.Errorf("UTC offset expected +00:00, got %q", got)
	}
}

func TestGetUserTimezoneHelper(t *testing.T) {
	cases := []struct {
		name string
		c    *tool.Context
		want string
	}{
		{"nil context", nil, "UTC"},
		{"empty config", &tool.Context{}, "UTC"},
		{"unparseable json", &tool.Context{A2AUserConfig: "not json"}, "UTC"},
		{"missing key", &tool.Context{A2AUserConfig: `{"other":"value"}`}, "UTC"},
		{"empty user_timezone", &tool.Context{A2AUserConfig: `{"user_timezone":""}`}, "UTC"},
		{"set to America/New_York", &tool.Context{A2AUserConfig: `{"user_timezone":"America/New_York"}`}, "America/New_York"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tool.GetUserTimezone(tc.c); got != tc.want {
				t.Errorf("GetUserTimezone = %q, want %q", got, tc.want)
			}
		})
	}
}

func assertAllNineFieldsPresent(t *testing.T, data map[string]any) {
	t.Helper()
	for _, key := range expectedDataKeys {
		if _, ok := data[key]; !ok {
			t.Errorf("missing data field %q; got keys=%v", key, keysOf(data))
		}
	}
	if got := len(data); got < len(expectedDataKeys) {
		t.Errorf("only %d data fields returned, want at least %d", got, len(expectedDataKeys))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
