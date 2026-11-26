// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type waitingComponent struct{ mockComponent }

func (c *waitingComponent) Stop(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

type tailRecordingComponent struct {
	mockComponent
	expired atomic.Bool
}

func (c *tailRecordingComponent) Stop(ctx context.Context) error {
	c.expired.Store(ctx.Err() != nil)
	return nil
}

func TestStopKeepsATailForComponentsAfterAWait(t *testing.T) {
	rt := newTestRuntime()
	rt.gracePeriod = 2 * time.Second
	later := &tailRecordingComponent{}
	rt.Register("later", later)
	rt.Register("waiting", &waitingComponent{})
	if err := rt.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := rt.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if later.expired.Load() {
		t.Error("the component stopped after a waiting one got an expired context")
	}
}
