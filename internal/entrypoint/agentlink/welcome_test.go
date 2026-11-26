// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"testing"
	"time"
)

func TestTheWelcomeSaysWhetherSAMHasStoppedTheAgent(t *testing.T) {
	r := newRig(t, nil, nil)
	welcomed := func() (*client, bool) {
		t.Helper()
		c, _, err := r.dial(agentName, token)
		if err != nil {
			t.Fatal(err)
		}
		w := c.next(typeWelcome)
		if w.Enabled == nil {
			t.Fatalf("welcome without the agent's state: %s", w.Raw)
		}
		return c, *w.Enabled
	}
	if _, enabled := welcomed(); !enabled {
		t.Fatal("a running agent was welcomed as stopped")
	}
	r.stop()
	c, enabled := welcomed()
	if enabled {
		t.Fatal("a stopped agent was welcomed as running")
	}
	if f := c.next(typeControl); f.Enabled == nil || *f.Enabled || f.Reason != "stopped_by_sam" {
		t.Fatalf("control after the welcome %s", f.Raw)
	}
}

func TestAWelcomeAndTheControlsAfterItAgreeWithAStopThatRacesTheConnect(t *testing.T) {
	r := newRig(t, nil, nil)
	for i := range 30 {
		stopped := i%2 == 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			if stopped {
				r.stop()
			} else {
				r.release()
			}
		}()
		c, _, err := r.dial(agentName, token)
		if err != nil {
			t.Fatal(err)
		}
		<-done
		w := c.next(typeWelcome)
		enabled := *w.Enabled
		for quiet := false; !quiet; {
			select {
			case f := <-c.in:
				if f.Type == typeControl {
					enabled = *f.Enabled
				}
			case <-time.After(200 * time.Millisecond):
				quiet = true
			}
		}
		if enabled == stopped {
			t.Fatalf("round %d: the agent was left believing enabled=%v after SAM set enabled=%v", i, enabled, !stopped)
		}
		_ = c.ws.Close()
	}
}
