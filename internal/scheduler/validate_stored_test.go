// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import "testing"

func TestValidateStoredExpression(t *testing.T) {
	tests := []struct {
		name         string
		scheduleType ScheduleType
		expression   string
		timezone     string
		wantErr      bool
	}{
		{"stored embedded-timezone cron", ScheduleTypeCron, "CRON_TZ=Europe/Berlin 0 9 * * *", "UTC", false},
		{"stored cron that never fires", ScheduleTypeCron, "0 0 30 2 *", "UTC", false},
		{"stored bare embedded timezone", ScheduleTypeCron, "CRON_TZ=UTC", "UTC", true},
		{"stored malformed cron", ScheduleTypeCron, "every morning", "UTC", true},
		{"stored interval", ScheduleTypeInterval, "1h", "UTC", false},
		{"stored interval below the minimum", ScheduleTypeInterval, "10s", "UTC", true},
		{"stored one-time", ScheduleTypeOneTime, "2099-01-01T09:00:00Z", "UTC", false},
		{"stored one-time that does not parse", ScheduleTypeOneTime, "tomorrow", "UTC", true},
		{"unknown timezone", ScheduleTypeCron, "0 9 * * *", "Mars/Phobos", true},
		{"unknown schedule type", ScheduleType("weekly"), "0 9 * * *", "UTC", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStoredExpression(tt.scheduleType, tt.expression, tt.timezone)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateStoredExpression(%q, %q, %q) = %v, wantErr %v", tt.scheduleType, tt.expression, tt.timezone, err, tt.wantErr)
			}
		})
	}
}
