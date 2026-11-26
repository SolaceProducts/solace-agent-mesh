// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
)

const mirrorRefusal = "this agent mirrors a remote agent"

func TestAgentWriters_RefuseARemoteAgentsMirror(t *testing.T) {
	t.Parallel()
	liveHistory := []seededDeployment{{DeployActionDeploy, DeployStatusSuccess}}
	cases := []struct {
		name    string
		history []seededDeployment
		call    func(t *testing.T, env *agentDeleteEnv, id string) error
	}{
		{"service Update", nil, func(t *testing.T, env *agentDeleteEnv, id string) error {
			_, err := env.agentSvc.Update(t.Context(), id, agentUpdateBody("Hijacked Mirror"), "u")
			return err
		}},
		{"service Delete", nil, func(t *testing.T, env *agentDeleteEnv, id string) error {
			return env.agentSvc.Delete(t.Context(), id)
		}},
		{"service DeleteWithUndeploy", nil, func(t *testing.T, env *agentDeleteEnv, id string) error {
			return env.agentSvc.DeleteWithUndeploy(t.Context(), id, "u", env.deploySvc)
		}},
		{"service DeleteWithUndeploy of a possibly live proxy", liveHistory, func(t *testing.T, env *agentDeleteEnv, id string) error {
			return env.agentSvc.DeleteWithUndeploy(t.Context(), id, "u", env.deploySvc)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newAgentDeleteEnv(testOpenDB(t), nil)
			id := env.createRemoteAgent(t, "weather-remote")
			env.seedHistory(t, id, tc.history)

			err := tc.call(t, env, id)

			var conflict *ConflictError
			switch {
			case errors.As(err, &conflict):
				if !strings.Contains(conflict.Message, mirrorRefusal) {
					t.Errorf("message = %q, want it to say the agent mirrors a remote agent", conflict.Message)
				}
			case err != nil && strings.HasPrefix(err.Error(), "status 409"):
				if !strings.Contains(err.Error(), mirrorRefusal) {
					t.Errorf("response = %v, want it to say the agent mirrors a remote agent", err)
				}
			default:
				t.Fatalf("err = %v, want a 409 conflict (a 502 would mean an undeploy was attempted first)", err)
			}
			if !env.remoteAgentKept(t, id) || !env.agentKept(t, id) {
				t.Error("a refused write dropped the remote agent or its mirror")
			}
			mirror, _, _, _, _, _ := env.agentRepo.GetByID(t.Context(), id)
			if mirror == nil || mirror.Name != "weather-remote" {
				t.Errorf("mirror after a refused write = %+v, want it unchanged", mirror)
			}
			remote, _ := env.a2aRepo.GetByID(t.Context(), id)
			if remote == nil || remote.Name != "weather-remote" {
				t.Errorf("remote agent after a refused write = %+v, want it unchanged", remote)
			}
		})
	}
}

func TestAgentWriters_StillWriteAStandardAgent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func(t *testing.T, env *agentDeleteEnv, id string) error
		gone bool
	}{
		{"Update", func(t *testing.T, env *agentDeleteEnv, id string) error {
			_, err := env.agentSvc.Update(t.Context(), id, agentUpdateBody("Renamed Standard"), "u")
			return err
		}, false},
		{"Delete", func(t *testing.T, env *agentDeleteEnv, id string) error { return env.agentSvc.Delete(t.Context(), id) }, true},
		{"DeleteWithUndeploy", func(t *testing.T, env *agentDeleteEnv, id string) error {
			return env.agentSvc.DeleteWithUndeploy(t.Context(), id, "u", env.deploySvc)
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newAgentDeleteEnv(testOpenDB(t), nil)
			id := env.createAgent(t, "standard-agent")

			if err := tc.call(t, env, id); err != nil {
				t.Fatalf("err = %v, want the write to succeed", err)
			}
			if kept := env.agentKept(t, id); kept == tc.gone {
				t.Errorf("agent kept = %v, want %v", kept, !tc.gone)
			}
		})
	}
}
