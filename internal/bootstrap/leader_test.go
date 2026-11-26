// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap_test

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type plainComponent struct{}

func (plainComponent) Init(context.Context, config.Config) error { return nil }
func (plainComponent) Start(context.Context) error               { return nil }
func (plainComponent) Stop(context.Context) error                { return nil }
func (plainComponent) Health() error                             { return nil }

type jobComponent struct {
	plainComponent
	jobs []leader.Job
}

func (c jobComponent) LeaderJobs() []leader.Job { return c.jobs }

func TestTheLeaderRegistersLastWithTheJobsOfEveryComponentBeforeIt(t *testing.T) {
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil)
	rt.Register("plain", plainComponent{})
	rt.Register("with-jobs", jobComponent{jobs: []leader.Job{{Name: "not_a_job", Every: time.Minute, Run: func(context.Context, leader.Term) error { return nil }}}})
	e := bootstrap.NewLeader(rt.Services(), "test-ns", leader.RoleGateway, nil)

	if err := bootstrap.RegisterLeader(rt, e); err != nil {
		t.Fatal(err)
	}
	components := rt.Components()
	if last := components[len(components)-1]; last.Name != "leader" || last.Component != e {
		t.Errorf("last component = %s, want the leader", last.Name)
	}
	if err := e.Init(t.Context(), nil); err == nil {
		t.Error("the leader initialized without seeing the component's jobs")
	}
}
