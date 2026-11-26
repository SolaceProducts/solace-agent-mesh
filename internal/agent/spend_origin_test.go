// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	deployedAgentInstance = "agent_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f60"
	deployedAgentID       = "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f60"
)

type delegationPath struct {
	name string
	run  func(t *testing.T, agent *Agent, toolCtx *tool.Context)
}

func delegationPaths() []delegationPath {
	return []delegationPath{
		{"peer", func(t *testing.T, agent *Agent, toolCtx *tool.Context) {
			agent.cfg.PeerRequestTimeoutSeconds = 1
			pt := &peerAgentTool{peerName: "peer", agent: agent, namespace: agent.cfg.Namespace}
			if _, err := pt.Execute(context.Background(), map[string]any{"task_description": "go"}, toolCtx); err != nil {
				t.Fatal(err)
			}
		}},
		{"sub_task", func(t *testing.T, agent *Agent, toolCtx *tool.Context) {
			st := &subTaskTool{agent: agent}
			if _, err := st.Execute(context.Background(), map[string]any{"instruction": "go", "type": "fresh"}, toolCtx); err != nil {
				t.Fatal(err)
			}
		}},
		{"workflow_peer", func(t *testing.T, agent *Agent, toolCtx *tool.Context) {
			agent.cfg.WorkflowRequestTimeoutSeconds = 1
			wt := &workflowAgentTool{
				peerName:    "wf",
				card:        a2a.AgentCard{Name: "wf"},
				agent:       agent,
				namespace:   agent.cfg.Namespace,
				inputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			}
			if _, err := wt.Execute(context.Background(), map[string]any{}, toolCtx); err != nil {
				t.Fatal(err)
			}
		}},
	}
}

func publishedDelegation(t *testing.T, path delegationPath, agent *Agent, toolCtx *tool.Context) publishedMsg {
	t.Helper()
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk
	agent.peerRouter = NewPeerRouter(brk, agent.cfg.Namespace, agent.logger)
	path.run(t, agent, toolCtx)
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.published) == 0 {
		t.Fatal("expected a broker publish")
	}
	return brk.published[0]
}

func setupTaskContext(t *testing.T, agent *Agent, msg a2a.Message, up a2a.UserProperties) (*TaskExecutionState, *tool.Context) {
	t.Helper()
	up.UserID = "u"
	state, rc, earlyResp, err := agent.setupTask(t.Context(), "task-1", msg, up, taskDelivery{})
	if err != nil || earlyResp != nil {
		t.Fatalf("setupTask: resp=%v err=%v", earlyResp, err)
	}
	return state, rc.toolCtx
}

func TestSetupTask_AgentIsTheRootWhenTheTaskNamesNone(t *testing.T) {
	for _, tc := range []struct {
		name, instance, wantID string
	}{
		{"plain name", "test-agent", "test-agent"},
		{"deployed agent", deployedAgentInstance, deployedAgentID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			agent.cfg.Name = tc.instance

			state, toolCtx := setupTaskContext(t, agent, newUserMessage("hi"), a2a.UserProperties{})

			if got := state.UserProps; got.RootKind != a2a.RootKindAgent || got.RootID != tc.wantID || got.RootNodeID != "" {
				t.Errorf("state root = %q/%q/%q, want agent/%s/empty", got.RootKind, got.RootID, got.RootNodeID, tc.wantID)
			}
			if toolCtx.RootKind != a2a.RootKindAgent || toolCtx.RootID != tc.wantID {
				t.Errorf("tool context root = %q/%q, want agent/%s", toolCtx.RootKind, toolCtx.RootID, tc.wantID)
			}
		})
	}
}

func TestSetupTask_KeepsTheRootTheTaskNames(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.Name = deployedAgentInstance

	state, toolCtx := setupTaskContext(t, agent, newUserMessage("hi"), a2a.UserProperties{
		RootKind: a2a.RootKindWorkflow, RootID: "wf-id", RootNodeID: "node-1",
	})

	got := state.UserProps
	if got.RootKind != a2a.RootKindWorkflow || got.RootID != "wf-id" || got.RootNodeID != "node-1" {
		t.Errorf("state root = %q/%q/%q, want the one the task named", got.RootKind, got.RootID, got.RootNodeID)
	}
	if toolCtx.RootKind != a2a.RootKindWorkflow || toolCtx.RootID != "wf-id" || toolCtx.RootNodeID != "node-1" {
		t.Errorf("tool context root = %q/%q/%q, want the one the task named", toolCtx.RootKind, toolCtx.RootID, toolCtx.RootNodeID)
	}
}

func TestSetupTask_ReadsTheSpendOriginFromTheFirstHop(t *testing.T) {
	const workflowInstance = "workflow_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f61"
	withMetadata := func(kv map[string]any) a2a.Message {
		msg := newUserMessage("hi")
		msg.Metadata = kv
		return msg
	}
	for _, tc := range []struct {
		name       string
		trigger    string
		delegating string
		msg        a2a.Message
		want       SpendOrigin
	}{
		{
			name:    "scheduled task id on a scheduled run",
			trigger: a2a.TriggerScheduled,
			msg:     withMetadata(map[string]any{a2a.MetadataScheduledTaskID: "sched-7"}),
			want:    SpendOrigin{ScheduledTaskID: "sched-7"},
		},
		{
			name:    "scheduled task id a client put on an interactive run is ignored",
			trigger: a2a.TriggerInteractive,
			msg:     withMetadata(map[string]any{a2a.MetadataScheduledTaskID: "sched-7"}),
		},
		{
			name:       "workflow dispatch identity",
			trigger:    a2a.TriggerInteractive,
			delegating: workflowInstance,
			msg:        withMetadata(map[string]any{"workflow_name": workflowInstance, "node_id": "summarise"}),
			want:       SpendOrigin{WorkflowID: "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f61", WorkflowNodeID: "summarise"},
		},
		{
			name:       "YAML-defined workflow is named",
			trigger:    a2a.TriggerInteractive,
			delegating: "nightly-report",
			msg:        withMetadata(map[string]any{"workflow_name": "nightly-report", "node_id": "summarise"}),
			want:       SpendOrigin{WorkflowName: "nightly-report", WorkflowNodeID: "summarise"},
		},
		{
			name:    "workflow identity a client put in its metadata is ignored",
			trigger: a2a.TriggerInteractive,
			msg:     withMetadata(map[string]any{"workflow_name": workflowInstance, "node_id": "summarise"}),
		},
		{
			name:       "workflow identity naming a different workflow than the delegating agent is ignored",
			trigger:    a2a.TriggerInteractive,
			delegating: "some-peer-agent",
			msg:        withMetadata(map[string]any{"workflow_name": workflowInstance, "node_id": "summarise"}),
		},
		{
			name:    "delegating task",
			trigger: a2a.TriggerInteractive,
			msg:     withMetadata(map[string]any{"parentTaskId": "task-parent"}),
			want:    SpendOrigin{ParentTaskID: "task-parent"},
		},
		{
			name:    "no metadata",
			trigger: a2a.TriggerInteractive,
			msg:     newUserMessage("hi"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			state, _ := setupTaskContext(t, agent, tc.msg, a2a.UserProperties{Trigger: tc.trigger, DelegatingAgentName: tc.delegating})
			if state.SpendOrigin != tc.want {
				t.Errorf("SpendOrigin = %+v, want %+v", state.SpendOrigin, tc.want)
			}
		})
	}
}

func TestResumedToolContextCarriesTheRoot(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthWithCred("cred-x"))
	state := newTestTaskState(agent)
	state.UserProps.RootKind, state.UserProps.RootID, state.UserProps.RootNodeID = a2a.RootKindWorkflow, "wf-id", "node-1"
	pca := defaultPauseCallArgs(agent, state)

	toolCtx := agent.buildToolCtxFromState(t.Context(), state, pca.sp)

	if toolCtx.RootKind != a2a.RootKindWorkflow || toolCtx.RootID != "wf-id" || toolCtx.RootNodeID != "node-1" {
		t.Errorf("resumed root = %q/%q/%q", toolCtx.RootKind, toolCtx.RootID, toolCtx.RootNodeID)
	}
}

func TestDelegationForwardsTheEntrypointAndTheRootThisAgentBecame(t *testing.T) {
	for _, path := range delegationPaths() {
		t.Run(path.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			agent.cfg.Name = deployedAgentInstance
			_, toolCtx := setupTaskContext(t, agent, newUserMessage("hi"), a2a.UserProperties{ClientID: "gw-1", Origin: a2a.Origin{Channel: httputil.SurfaceWebUI, UserUUID: "user-uuid", EntrypointUUID: "gw-uuid"}})

			props := publishedDelegation(t, path, agent, toolCtx).userProps

			want := map[string]string{
				a2a.PropEntrypointName: "gw-1",
				a2a.PropRootKind:       a2a.RootKindAgent,
				a2a.PropRootID:         deployedAgentID,
				a2a.PropChannel:        httputil.SurfaceWebUI,
				a2a.PropUserUUID:       "user-uuid",
				a2a.PropEntrypointUUID: "gw-uuid",
			}
			for key, value := range want {
				if props[key] != value {
					t.Errorf("%s = %q, want %q", key, props[key], value)
				}
			}
			for _, key := range []string{a2a.PropRootNodeID, a2a.PropClientID} {
				if _, ok := props[key]; ok {
					t.Errorf("%s stamped on the delegation: %q", key, props[key])
				}
			}
		})
	}
}

func TestDelegationForwardsTheRootAndEntrypointItReceivedVerbatim(t *testing.T) {
	for _, path := range delegationPaths() {
		t.Run(path.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			_, toolCtx := setupTaskContext(t, agent, newUserMessage("hi"), a2a.UserProperties{
				ClientID: "wf-client", DelegatingAgentName: "parent-agent",
				Origin:   a2a.Origin{Channel: "mcp", UserUUID: "user-uuid", EntrypointUUID: "gw-uuid", EntrypointName: "webui"},
				RootKind: a2a.RootKindWorkflow, RootID: "wf-id", RootNodeID: "node-1",
			})

			props := publishedDelegation(t, path, agent, toolCtx).userProps

			want := map[string]string{
				a2a.PropEntrypointName: "webui",
				a2a.PropRootKind:       a2a.RootKindWorkflow,
				a2a.PropRootID:         "wf-id",
				a2a.PropRootNodeID:     "node-1",
				a2a.PropChannel:        "mcp",
				a2a.PropUserUUID:       "user-uuid",
				a2a.PropEntrypointUUID: "gw-uuid",
			}
			for key, value := range want {
				if props[key] != value {
					t.Errorf("%s = %q, want %q", key, props[key], value)
				}
			}
			if _, ok := props[a2a.PropClientID]; ok {
				t.Errorf("%s forwarded on the delegation: %q", a2a.PropClientID, props[a2a.PropClientID])
			}
		})
	}
}

func TestDelegationDoesNotForwardTheScheduledTaskID(t *testing.T) {
	for _, path := range delegationPaths() {
		t.Run(path.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			msg := newUserMessage("hi")
			msg.Metadata = map[string]any{a2a.MetadataScheduledTaskID: "sched-7"}
			_, toolCtx := setupTaskContext(t, agent, msg, a2a.UserProperties{Trigger: a2a.TriggerScheduled})

			sent := publishedDelegation(t, path, agent, toolCtx)

			if strings.Contains(string(sent.payload), "sched-7") {
				t.Errorf("scheduled task id forwarded in the request body")
			}
			for key, value := range sent.userProps {
				if value == "sched-7" {
					t.Errorf("scheduled task id forwarded as user property %q", key)
				}
			}
		})
	}
}

func TestToolContextEntrypointNameIsTheChainsWhileClientIDStaysTheTasks(t *testing.T) {
	cases := []struct {
		name           string
		props          a2a.UserProperties
		wantEntrypoint string
	}{
		{"top-level task", a2a.UserProperties{ClientID: "gw-1", Origin: a2a.Origin{EntrypointName: "forged"}}, "gw-1"},
		{"peer-delegated task", a2a.UserProperties{ClientID: "gw-1", DelegatingAgentName: "parent-agent", Origin: a2a.Origin{EntrypointName: "webui"}}, "webui"},
		{"workflow-dispatched task", a2a.UserProperties{ClientID: "gw-1", DelegatingAgentName: "nightly", Origin: a2a.Origin{EntrypointName: "webui"}}, "webui"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
			state, toolCtx := setupTaskContext(t, agent, newUserMessage("hi"), tc.props)
			if toolCtx.ClientID != "gw-1" || toolCtx.Origin.EntrypointName != tc.wantEntrypoint || state.UserProps.EntrypointName != tc.wantEntrypoint {
				t.Errorf("ClientID=%q EntrypointName=%q state=%q, want gw-1 and %q",
					toolCtx.ClientID, toolCtx.Origin.EntrypointName, state.UserProps.EntrypointName, tc.wantEntrypoint)
			}
		})
	}
}

func TestResumedToolContextCarriesTheEntrypointName(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthWithCred("cred-x"))
	state := newTestTaskState(agent)
	state.UserProps.EntrypointName = "webui"
	pca := defaultPauseCallArgs(agent, state)

	if toolCtx := agent.buildToolCtxFromState(t.Context(), state, pca.sp); toolCtx.Origin.EntrypointName != "webui" {
		t.Errorf("resumed EntrypointName = %q, want webui", toolCtx.Origin.EntrypointName)
	}
}

func TestSpendIdentitiesSplitIntoTheRowUUIDOrTheName(t *testing.T) {
	const bareUUID = "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f62"
	for _, tc := range []struct {
		name               string
		split              func(string) (string, string)
		in, wantID, wantNm string
	}{
		{"deployed agent", ComponentIdentity, deployedAgentInstance, deployedAgentID, ""},
		{"deployed workflow", ComponentIdentity, "workflow_0190c7a2_5f4e_7b3a_9d21_3c8e5a1b7f61", "0190c7a2-5f4e-7b3a-9d21-3c8e5a1b7f61", ""},
		{"YAML agent", ComponentIdentity, "spend-agent", "", "spend-agent"},
		{"prefixed name that is no UUID", ComponentIdentity, "agent_reporter", "", "agent_reporter"},
		{"component named as a bare UUID is a name", ComponentIdentity, bareUUID, "", bareUUID},
		{"deployed root", RootIdentity, deployedAgentID, deployedAgentID, ""},
		{"YAML root", RootIdentity, "spend-agent", "", "spend-agent"},
		{"root named as a bare UUID reads as an id", RootIdentity, bareUUID, bareUUID, ""},
		{"no root", RootIdentity, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, name := tc.split(tc.in)
			if id != tc.wantID || name != tc.wantNm {
				t.Errorf("split(%q) = (%q, %q), want (%q, %q)", tc.in, id, name, tc.wantID, tc.wantNm)
			}
		})
	}
}
