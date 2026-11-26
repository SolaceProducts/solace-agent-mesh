// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestADisabledAgentIsNotWokenForARequest(t *testing.T) {
	var wakes atomic.Int32
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wakes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(runtimeSrv.Close)
	enabled := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL}}, nil)
	enabled.awaitWakeWatch()
	enabled.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })

	disabled := newRig(t, map[string]any{"enabled": false, "wake": map[string]any{"url": runtimeSrv.URL}}, nil)
	disabled.submit("T2", "tok:alice:T2", nil)
	select {
	case <-disabled.adapter.agent(agentName).wakeWatching:
		t.Fatal("a disabled agent's requests are watched for a wake")
	case <-time.After(300 * time.Millisecond):
	}
	if n := wakes.Load(); n != 1 {
		t.Fatalf("the runtime was called %d times, once of them for the enabled agent", n)
	}
}

func TestAnotherPublisherOfTheAgentsNameIsReportedOnce(t *testing.T) {
	logs := captureLogs(t)
	r, c := connectedRig(t, nil)
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	const warning = "agent link agent shares its name with another agent on the mesh"
	waitFor(t, func() bool { return len(r.core.AgentRegistry().InstanceIDs(agentName)) == 1 })

	r.adapter.sweepOnce(time.Now())
	if n := logs.count(warning); n != 0 {
		t.Fatalf("the link's own card was reported as another agent %d times", n)
	}

	r.core.AgentRegistry().AddOrUpdateInstance(a2a.AgentCard{Name: agentName, URL: "solace:native"}, "claims-assistant-7f9c")
	r.adapter.sweepOnce(time.Now())
	r.adapter.sweepOnce(time.Now())
	if n := logs.count(warning); n != 1 {
		t.Fatalf("another publisher of the name was reported %d times", n)
	}
}
