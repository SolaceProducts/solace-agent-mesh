// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	metricInboundEvents        = "googlechat_inbound_events"
	metricAckedWithoutTask     = "googlechat_acked_without_task"
	metricSubmitFailures       = "googlechat_submit_failures"
	metricTaskRoutingRecovered = "googlechat_task_routing_recovered"
	metricTaskRoutingLost      = "googlechat_task_routing_lost"
	metricFeedback             = "googlechat_feedback"
)

func (a *Adapter) meter() runtime.Metrics {
	if a.metrics == nil {
		return runtime.MetricsOrNoop(nil)
	}
	return a.metrics
}

type noticeThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

const maxNoticeKeys = 4096

func (t *noticeThrottle) allow(key string, window time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.last == nil || len(t.last) > maxNoticeKeys {
		t.last = make(map[string]time.Time)
	}
	if at, seen := t.last[key]; seen && now.Sub(at) < window {
		return false
	}
	t.last[key] = now
	return true
}
