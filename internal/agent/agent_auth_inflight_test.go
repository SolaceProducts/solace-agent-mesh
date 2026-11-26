// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "testing"

func TestBeginAuthFlow_LeaderThenFollower(t *testing.T) {
	agent, _, _ := newTestAgent()

	if !agent.beginAuthFlow("u1", "trace-1", "cred") {
		t.Fatal("first caller must be leader")
	}
	if agent.beginAuthFlow("u1", "trace-1", "cred") {
		t.Error("second concurrent caller in the same request must be follower")
	}
	if !agent.beginAuthFlow("u2", "trace-1", "cred") {
		t.Error("different user must be its own leader")
	}
	if !agent.beginAuthFlow("u1", "trace-1", "other") {
		t.Error("different credential must be its own leader")
	}

	agent.endAuthFlow("u1", "trace-1", "cred")
	if !agent.beginAuthFlow("u1", "trace-1", "cred") {
		t.Error("after endAuthFlow, next caller must be leader")
	}
}

func TestBeginAuthFlow_DifferentTraceIDsDoNotCoalesce(t *testing.T) {
	agent, _, _ := newTestAgent()

	if !agent.beginAuthFlow("u1", "trace-A", "cred") {
		t.Fatal("request A must be leader")
	}
	if !agent.beginAuthFlow("u1", "trace-B", "cred") {
		t.Error("a different originating request (traceID) must be its own leader, not a follower")
	}
	if agent.beginAuthFlow("u1", "trace-B", "cred") {
		t.Error("a second call within the same request must be a follower")
	}
	agent.endAuthFlow("u1", "trace-A", "cred")
	if agent.beginAuthFlow("u1", "trace-B", "cred") {
		t.Error("ending request A must not release request B's in-flight marker")
	}
}
