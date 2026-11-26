// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "time"

const (
	MetricLastSuccessfulAuthSecondsAgo = "email_last_successful_auth_seconds_ago"

	MetricDriverUnavailable = "email_driver_unavailable_total"
)

var driverUnavailableAlarmInterval = 60 * time.Second
