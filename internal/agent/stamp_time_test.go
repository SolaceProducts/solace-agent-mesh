// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

var fixedNow = time.Date(2026, 9, 21, 20, 47, 30, 0, time.UTC)

func TestCurrentTimeStampFormat(t *testing.T) {
	tests := []struct {
		name   string
		tz     string
		want   string
		fallbk bool
	}{
		{
			name: "utc",
			tz:   "UTC",
			want: "Monday, 2026-09-21T20:47:30Z (UTC)",
		},
		{
			name: "empty falls back to utc without reporting an error",
			tz:   "",
			want: "Monday, 2026-09-21T20:47:30Z (UTC)",
		},
		{
			name: "west of utc keeps the same weekday",
			tz:   "America/New_York",
			want: "Monday, 2026-09-21T16:47:30-04:00 (America/New_York)",
		},
		{
			name: "east of utc rolls over to the next weekday",
			tz:   "Asia/Tokyo",
			want: "Tuesday, 2026-09-22T05:47:30+09:00 (Asia/Tokyo)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, fallback := currentTimeStamp(fixedNow, tc.tz)
			if got != tc.want {
				t.Errorf("currentTimeStamp(%q) = %q, want %q", tc.tz, got, tc.want)
			}
			if fallback != tc.fallbk {
				t.Errorf("currentTimeStamp(%q) fallback = %v, want %v", tc.tz, fallback, tc.fallbk)
			}
		})
	}
}

func TestCurrentTimeStampInvalidZoneFallsBackToUTC(t *testing.T) {
	for _, tz := range []string{"Not/A_Real_Zone", "../../etc/passwd", "Mars/Olympus_Mons"} {
		got, fallback := currentTimeStamp(fixedNow, tz)
		if !fallback {
			t.Errorf("currentTimeStamp(%q) should report a fallback", tz)
		}
		if want := "Monday, 2026-09-21T20:47:30Z (UTC)"; got != want {
			t.Errorf("currentTimeStamp(%q) = %q, want %q", tz, got, want)
		}
	}
}

func TestStripCurrentTimeStampRoundTripsNewFormat(t *testing.T) {
	for _, tz := range []string{"UTC", "America/New_York", "Asia/Tokyo"} {
		stamp, _ := currentTimeStamp(fixedNow, tz)
		stamped := currentTimePrefix + stamp + "]\n" + "what day is it?"
		if got := stripCurrentTimeStamp(stamped); got != "what day is it?" {
			t.Errorf("tz %q: strip = %q, want %q", tz, got, "what day is it?")
		}
	}
}

func TestStampingIsIdempotent(t *testing.T) {
	stamp, _ := currentTimeStamp(fixedNow, "America/New_York")
	text := "original question"
	for range 3 {
		text = currentTimePrefix + stamp + "]\n" + stripCurrentTimeStamp(text)
	}
	if n := strings.Count(text, currentTimePrefix); n != 1 {
		t.Fatalf("stamp stacked %d times, want 1: %q", n, text)
	}
}

func TestStripCurrentTimeStampAcceptsLegacyBareFormatFromPersistedSessions(t *testing.T) {
	legacy := currentTimePrefix + "2026-09-21T20:47:30Z" + "]\nhello"
	if got := stripCurrentTimeStamp(legacy); got != "hello" {
		t.Errorf("legacy strip = %q, want %q", got, "hello")
	}
}

func TestStripCurrentTimeStampLeavesUserTextAlone(t *testing.T) {
	tests := []string{
		"[Current time: meeting moved to 2026-09-21T20:47:30Z, please]\nremind me",
		"[Current time: line one\nline two 2026-09-21T20:47:30Z\nmore]\nremind me",
		"[Current time: Tuesday, 2026-09-21T20:47:30Z (UTC)]\nweekday disagrees with the date",
		"[Current time: Monday, 2026-09-21T20:47:30Z]\nno zone",
		"[Current time: Monday, 2026-09-21T20:47:30Z ()]\nempty zone",
		"[Current time: not a timestamp]\nreal user text",
		"[Current time: ]\nreal user text",
		"plain user text",
		"[Current time: 2026-09-21T20:47:30Z no closing bracket",
	}
	for _, in := range tests {
		if got := stripCurrentTimeStamp(in); got != in {
			t.Errorf("strip(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestUserTimezoneReachesTurnStamp(t *testing.T) {
	gc := gatewayContextFrom(gatewayMetadataOf(a2a.Message{}), a2a.UserProperties{
		A2AUserConfig: `{"user_timezone":"Asia/Tokyo"}`,
	}, false)
	if gc.UserTimezone != "Asia/Tokyo" {
		t.Fatalf("gc.UserTimezone = %q, want Asia/Tokyo", gc.UserTimezone)
	}

	stamp, fallback := currentTimeStamp(fixedNow, gc.UserTimezone)
	if fallback {
		t.Error("Asia/Tokyo reported a fallback")
	}
	if want := "Tuesday, 2026-09-22T05:47:30+09:00 (Asia/Tokyo)"; stamp != want {
		t.Errorf("stamp = %q, want %q", stamp, want)
	}
}

func TestExtractGatewayContextTimezoneAbsent(t *testing.T) {
	gc := gatewayContextFrom(gatewayMetadataOf(a2a.Message{}), a2a.UserProperties{
		A2AUserConfig: `{"user_profile":{"name":"Ada"}}`,
	}, false)
	if gc.UserTimezone != "" {
		t.Errorf("UserTimezone = %q, want empty so the stamp uses UTC", gc.UserTimezone)
	}
}
