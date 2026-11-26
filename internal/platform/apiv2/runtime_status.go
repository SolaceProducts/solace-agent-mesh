// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type RuntimeStatus struct {
	Status   string           `json:"status" example:"degraded"`
	Problems []RuntimeProblem `json:"problems"`
}

type RuntimeProblem struct {
	Key       string  `json:"key" example:"ingress"`
	Severity  string  `json:"severity" example:"degraded"`
	Detail    string  `json:"detail" example:"The entrypoint is unable to receive inbound webhooks. Retrying automatically."`
	SinceTime Instant `json:"sinceTime" example:"\"2026-01-02T03:04:05.678Z\""`
}

func toRuntimeStatus(rs platpkg.RuntimeStatus) RuntimeStatus {
	problems := make([]RuntimeProblem, len(rs.Problems()))
	for i, p := range rs.Problems() {
		var since Instant
		if !p.Since.IsZero() {
			since = Instant(p.Since.UnixMilli())
		}
		problems[i] = RuntimeProblem{Key: p.ID, Severity: p.Severity, Detail: p.Detail, SinceTime: since}
	}
	return RuntimeStatus{Status: rs.Status(), Problems: problems}
}
