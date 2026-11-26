// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestLoggingAdapter_ZeroValueDoesNotPanic(t *testing.T) {
	t.Parallel()
	a := &LoggingAdapter{}
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "t1", &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
	}, nil)
	a.OnArtifactUpdate(ctx, "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{ArtifactID: "a1"},
	}, nil)
	a.OnFinalResponse(ctx, "t1", &a2a.Task{
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}, nil)
	a.OnError(ctx, "t1", &a2a.Error{Code: 1, Message: "boom"}, nil)
	a.OnAgentDiscovered(&a2a.AgentCard{Name: "TestAgent"})
	a.OnAgentRemoved("TestAgent")
}
