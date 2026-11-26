// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestHandleScheduleRequestRecovering_PanicDropsRequestInsteadOfCrashing(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)

	acks := 0
	msg := broker.NewDeliveredMessage("m1", "test-ns/sam/v1/schedule/request", []byte("not json"),
		func() {
			acks++
			if acks == 1 {
				panic("ack blew up inside the handler")
			}
		},
		func() { t.Error("a request that panicked must not be nacked for redelivery") },
	)

	svc.handleScheduleRequestRecovering(t.Context(), msg)

	if acks != 2 {
		t.Fatalf("acks = %d, want 2 (handler ack that panicked, then the recovery ack)", acks)
	}
}
