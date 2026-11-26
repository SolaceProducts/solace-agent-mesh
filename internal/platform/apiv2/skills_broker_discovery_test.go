// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const skillsBrokerDiscoveryNamespace = "test-ns"

type skillsBrokerDiscoveryAPI struct {
	*skillAPI
	broker broker.Broker
}

func newSkillsBrokerDiscoveryAPI(t *testing.T) *skillsBrokerDiscoveryAPI {
	t.Helper()
	core, deps := newTestCore(t)
	var tracker *platpkg.ResourceTracker
	for _, svc := range deps.BackgroundServices {
		if rt, ok := svc.(*platpkg.ResourceTracker); ok {
			tracker = rt
		}
	}
	if tracker == nil {
		t.Fatal("the platform wired no resource tracker")
	}
	if err := tracker.Start(t.Context()); err != nil {
		t.Fatalf("start resource tracker: %v", err)
	}
	t.Cleanup(func() { _ = tracker.Stop(t.Context()) })
	return &skillsBrokerDiscoveryAPI{
		skillAPI: &skillAPI{&toolsetAPI{entrypointAPI: &entrypointAPI{apiClient: newAPIClient(t, core)}, deps: deps}},
		broker:   core.Services().Broker(),
	}
}

func (a *skillsBrokerDiscoveryAPI) publish(topic, method string, params any) {
	a.t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		a.t.Fatal(err)
	}
	if err := a.broker.Publish(a.t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		a.t.Fatalf("publish to %s: %v", topic, err)
	}
}

func (a *skillsBrokerDiscoveryAPI) listedOnceStatus(id, status string) Skill {
	a.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, sk := range decodeList[Skill](a.t, a.call(http.MethodGet, "/skills?pageSize=100", nil)).Data {
			if sk.ID == id && sk.DiscoveryStatus == status {
				return sk
			}
		}
		if time.Now().After(deadline) {
			a.t.Fatalf("skill %s never listed as %s", id, status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSkills_AnInitOnTheBrokerMakesTheSkillReadyInTheList(t *testing.T) {
	api := newSkillsBrokerDiscoveryAPI(t)
	created := api.create("broker-init-skill")

	api.publish(remotetool.SkillInitTopic(skillsBrokerDiscoveryNamespace, created.Name), remotetool.MethodSkillInit, remotetool.SkillInitParams{
		SkillName:     created.Name,
		Description:   "Skill visible via HTTP",
		Tags:          []string{"http", "test"},
		HasReferences: true,
		ResourceCount: 3,
		BundledTools:  []remotetool.SkillBundledTool{{ToolName: "bundled_tool", Description: "A bundled tool"}},
	})

	got := api.listedOnceStatus(created.ID, "ready")
	if !got.HasReferences || got.ResourceCount != 3 || len(got.Tags) != 2 || !got.HasTools || len(got.BundledTools) != 1 {
		t.Errorf("listed skill %+v, want the init's references, 3 resources, 2 tags and 1 bundled tool", got)
	}
}

func TestSkills_ARemovalOnTheBrokerMarksTheSkillFailedInTheList(t *testing.T) {
	api := newSkillsBrokerDiscoveryAPI(t)
	created := api.create("broker-removed-skill")

	api.publish(remotetool.SkillInitTopic(skillsBrokerDiscoveryNamespace, created.Name), remotetool.MethodSkillInit,
		remotetool.SkillInitParams{SkillName: created.Name, Description: "Lifecycle test"})
	api.listedOnceStatus(created.ID, "ready")

	api.publish(remotetool.SkillRemovedTopic(skillsBrokerDiscoveryNamespace, created.Name), remotetool.MethodSkillRemoved,
		remotetool.SkillRemovedParams{SkillName: created.Name})
	api.listedOnceStatus(created.ID, "failed")
}
