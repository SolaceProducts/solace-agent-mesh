// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import (
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const maxWallClockSteps = 128

func nextCronFire(expression string, sched cron.Schedule, after time.Time, loc *time.Location) time.Time {
	local := after.In(loc)
	if hasEmbeddedTimezone(expression) || !firesAtFixedHours(expression) {
		return sched.Next(local)
	}
	wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
	for range maxWallClockSteps {
		wall = sched.Next(wall)
		if wall.IsZero() {
			return wall
		}
		candidate := wallClockInstant(wall, loc)
		if candidate.After(after) {
			return candidate
		}
	}
	return time.Time{}
}

func wallClockInstant(wall time.Time, loc *time.Location) time.Time {
	instant := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, loc)
	resolved := time.Date(instant.Year(), instant.Month(), instant.Day(), instant.Hour(), instant.Minute(), 0, 0, time.UTC)
	if gap := wall.Sub(resolved); gap > 0 {
		return instant.Add(gap)
	}
	return instant
}

func firesAtFixedHours(expression string) bool {
	fields := strings.Fields(expression)
	const hourField = 1
	if len(fields) <= hourField {
		return false
	}
	hours := fields[hourField]
	return !strings.ContainsAny(hours, "*?")
}
