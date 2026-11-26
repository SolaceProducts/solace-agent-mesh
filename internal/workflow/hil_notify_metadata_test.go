// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "testing"

func TestHILNotifyMetadata(t *testing.T) {
	md := hilNotifyMetadata("trace-1", "web-session-1")
	if md["trace_id"] != "trace-1" || md["session_id"] != "web-session-1" {
		t.Errorf("metadata = %v, want trace_id trace-1 and session_id web-session-1", md)
	}
	if md := hilNotifyMetadata("trace-2", ""); md["session_id"] != nil {
		t.Errorf("session_id = %v, want it absent for a run outside any chat", md["session_id"])
	}
}
