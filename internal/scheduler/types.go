// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package scheduler

import "time"

type ScheduleType string

const (
	ScheduleTypeCron ScheduleType = "cron"

	ScheduleTypeInterval ScheduleType = "interval"

	ScheduleTypeOneTime ScheduleType = "one_time"
)

const defaultTimezone = "UTC"

const minInterval = 60 * time.Second

const (
	MaxTaskRetries       = 10
	MaxRetryDelaySeconds = 24 * 60 * 60
	MinTimeoutSeconds    = 1
	MaxTimeoutSeconds    = 7 * 24 * 60 * 60
)
