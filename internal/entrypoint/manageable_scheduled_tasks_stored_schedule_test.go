// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestBuildControlUpdateFields_StoredScheduleIsJudgedByTheStoredRulesUntilItChanges(t *testing.T) {
	const embedded = "CRON_TZ=Europe/Berlin 0 9 * * *"
	const paddedNeverFires = "  0 0 30 2 *  "
	const refused = ""
	str := func(s string) *string { return &s }
	tests := []struct {
		name         string
		storedType   string
		stored       string
		req          scheduledTaskUpdateRequest
		wantExpr     string
		wantRejected bool
	}{
		{"stored expression resent", "cron", embedded, scheduledTaskUpdateRequest{ScheduleExpression: str(embedded)}, embedded, false},
		{"timezone-only change leaves the stored expression out of the update", "cron", embedded, scheduledTaskUpdateRequest{Timezone: str("America/New_York")}, "", false},
		{"new embedded-timezone expression", "cron", embedded, scheduledTaskUpdateRequest{ScheduleExpression: str("CRON_TZ=Asia/Tokyo 0 8 * * *")}, refused, true},
		{"padded stored never-firing expression resent padded", "cron", paddedNeverFires, scheduledTaskUpdateRequest{ScheduleExpression: str(paddedNeverFires)}, "0 0 30 2 *", false},
		{"timezone-only change on a padded stored never-firing expression", "cron", paddedNeverFires, scheduledTaskUpdateRequest{Timezone: str("America/New_York")}, "0 0 30 2 *", false},
		{"timezone-only change on a padded stored interval", "interval", "  1h  ", scheduledTaskUpdateRequest{Timezone: str("America/New_York")}, "1h", false},
		{"new never-firing expression", "cron", "0 9 * * *", scheduledTaskUpdateRequest{ScheduleExpression: str("0 0 30 2 *")}, refused, true},
		{"stored expression resent with a new schedule type", "cron", embedded, scheduledTaskUpdateRequest{ScheduleType: str("interval"), ScheduleExpression: str(embedded)}, refused, true},
		{"stored expression kept while the type changes to cron", "interval", embedded, scheduledTaskUpdateRequest{ScheduleType: str("cron")}, refused, true},
		{"exact empty expression beside a timezone means unchanged", "cron", "0 9 * * *", scheduledTaskUpdateRequest{ScheduleExpression: str(""), Timezone: str("America/New_York")}, "", false},
		{"whitespace-only expression", "cron", "0 9 * * *", scheduledTaskUpdateRequest{ScheduleExpression: str("   ")}, refused, true},
		{"whitespace-only expression beside a timezone", "cron", "0 9 * * *", scheduledTaskUpdateRequest{ScheduleExpression: str("   "), Timezone: str("America/New_York")}, refused, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := &store.ScheduledTask{ScheduleType: tt.storedType, ScheduleExpression: tt.stored, Timezone: "UTC"}
			fields, _, err := buildControlUpdateFields(stored, tt.req)
			if (err != nil) != tt.wantRejected {
				t.Fatalf("buildControlUpdateFields(stored %q) err = %v, wantRejected %v", tt.stored, err, tt.wantRejected)
			}
			if got, _ := fields["schedule_expression"].(string); got != tt.wantExpr {
				t.Fatalf("schedule_expression to store = %q, want %q", got, tt.wantExpr)
			}
		})
	}
}
