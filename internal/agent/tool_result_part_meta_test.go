// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const servedSkillHash = "0f343b0931126a20f133d67c2b018a3b5e6a7a6c2c6f3f5f4b2b0f1e9c8d7a6b"

type publishedPart struct {
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata"`
}

func drainParts(t *testing.T, h *toolStatusHarness, count int) []publishedPart {
	t.Helper()
	parts := make([]publishedPart, 0, count)
	for range count {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := h.broker.Receive(ctx, h.queue)
		cancel()
		if err != nil {
			t.Fatalf("expected a status update: %v", err)
		}
		var resp a2a.Response
		if err := json.Unmarshal(msg.Payload, &resp); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(resp.Result)
		var event a2a.TaskStatusUpdateEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Status.Message == nil || len(event.Status.Message.Parts) != 1 {
			t.Fatalf("event = %s (%v), want one part", raw, err)
		}
		partJSON, _ := json.Marshal(event.Status.Message.Parts[0])
		var part publishedPart
		if err := json.Unmarshal(partJSON, &part); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, part)
	}
	return parts
}

func servingSkill(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	res := tool.OK("Loaded skill 'web'.")
	res.LoadedSkillContentHash = servedSkillHash
	return res, nil
}

func TestADispatchedToolResultCarriesTheSkillVersionItLoaded(t *testing.T) {
	for name, calls := range map[string][]llm.ToolCall{
		"alone":    {{ID: "c1", Name: "load_skill", Arguments: `{"name":"web"}`}},
		"parallel": {{ID: "c1", Name: "load_skill", Arguments: `{"name":"web"}`}, {ID: "c2", Name: "echo", Arguments: `{}`}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newToolStatusHarness(t, "task-partmeta-"+name)
			h.registerTool(t, "load_skill", servingSkill)
			h.registerTool(t, "echo", func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.OK("echoed"), nil
			})
			group := ""
			if len(calls) > 1 {
				group = "group-1"
			}
			h.dispatch(t.Context(), calls, group)
			found := 0
			for _, part := range drainParts(t, h, 2*len(calls)) {
				if part.Data["type"] != string(a2a.SignalToolResult) {
					continue
				}
				switch part.Data["function_call_id"] {
				case "c1":
					found++
					if part.Metadata[a2a.PartMetaSkillContentHash] != servedSkillHash || part.Data["result_data"] != "Loaded skill 'web'." {
						t.Errorf("load_skill part = %+v, want the version in its metadata and the result unchanged", part)
					}
				default:
					if part.Metadata != nil {
						t.Errorf("another tool's part carries metadata %v", part.Metadata)
					}
				}
			}
			if found != 1 {
				t.Errorf("load_skill results published = %d, want 1", found)
			}
		})
	}
}

func TestACheckpointedResultKeepsTheSkillVersionItLoaded(t *testing.T) {
	res := tool.OK("Loaded skill 'web'.")
	res.LoadedSkillContentHash = servedSkillHash
	res.LLMData = map[string]any{SkillInstructionsDataKey: "Search first."}
	raw, err := json.Marshal(newPersistedToolResult(res))
	if err != nil {
		t.Fatal(err)
	}
	var restored persistedToolResult
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.toResult(); got == nil || got.LoadedSkillContentHash != servedSkillHash {
		t.Errorf("restored result = %+v, want the skill version kept", got)
	}
	if meta := toolResultPartMeta(restored.toResult()); meta[a2a.PartMetaSkillContentHash] != servedSkillHash {
		t.Errorf("the resumed result would publish metadata %v, want the skill version", meta)
	}
	if got, _ := json.Marshal(restored.toResult().LLMData[SkillInstructionsDataKey]); string(got) != `"Search first."` {
		t.Errorf("restored model-only data renders as %s, want the instructions the load delivered", got)
	}
}

func TestAResumedTaskRepublishesTheSkillVersionItLoaded(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	const taskID, subTaskID = "task-resume-skill", "sub-resume-skill"
	const statusTopic = "test-ns/a2a/v1/gateway/status/gw/task-resume-skill"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.UserProps.A2AStatusTopic = statusTopic
	state.Messages[1].ToolCalls = append(state.Messages[1].ToolCalls, llm.ToolCall{ID: "tc-2", Name: "load_skill"})
	state.PausedAssistantMsg = state.Messages[1]
	loaded := tool.OK("Loaded skill 'web'.")
	loaded.LoadedSkillContentHash = servedSkillHash
	state.SyncResults = map[int]*tool.Result{1: loaded}

	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)
	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)
	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")

	var republished *publishedPart
	for _, msg := range survivorBrk.getPublished() {
		if msg.topic != statusTopic {
			continue
		}
		var resp a2a.Response
		if json.Unmarshal(msg.payload, &resp) != nil {
			continue
		}
		raw, _ := json.Marshal(resp.Result)
		var event a2a.TaskStatusUpdateEvent
		if json.Unmarshal(raw, &event) != nil || event.Status.Message == nil {
			continue
		}
		for _, part := range event.Status.Message.Parts {
			partJSON, _ := json.Marshal(part)
			var p publishedPart
			if json.Unmarshal(partJSON, &p) == nil && p.Data["type"] == string(a2a.SignalToolResult) && p.Data["function_call_id"] == "tc-2" {
				republished = &p
			}
		}
	}
	if republished == nil || republished.Metadata[a2a.PartMetaSkillContentHash] != servedSkillHash {
		t.Errorf("the resumed load_skill result = %+v, want it republished with the skill version", republished)
	}
}
