// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tracededup

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const llmInvocationFixture = `{"jsonrpc":"2.0","id":"01a0b0b6-1e87-722a-98f1-5fa988a88624","result":{"kind":"status-update","final":false,"taskId":"01a0b0b6-1e87-722a-98f1-5fa988a88624","contextId":"ctx-1","metadata":{"agent_name":"TraceCorpusAgent"},"status":{"state":"working","timestamp":"2026-09-17T18:52:00.521035+00:00","message":{"kind":"message","role":"agent","messageId":"m1","parts":[{"kind":"data","data":{"type":"llm_invocation","usage":null,"request":{"model":"openai/e2e-mock","contents":[{"role":"user","parts":[{"text":"hello"}]}],"config":{"system_instruction":"You are an agent. Big id 9007199254740993 stays exact.","generation_config":{"max_output_tokens":4096},"tools":[{"function_declarations":[{"name":"zeta_tool","description":"z","parameters":{"type":"object"}},{"name":"alpha_tool","description":"a","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}]}]}}}}]}}}}`

func decodeJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return v
}

func TestProcess_ExtractsAndRehydratesLosslessly(t *testing.T) {
	res := Process([]byte(llmInvocationFixture))
	if !res.Changed {
		t.Fatal("expected config block to be extracted")
	}
	if len(res.Blobs) != 2 {
		t.Fatalf("blobs = %d, want 2", len(res.Blobs))
	}
	if len(res.Payload) >= len(llmInvocationFixture) {
		t.Fatalf("deduped payload (%d) not smaller than original (%d)", len(res.Payload), len(llmInvocationFixture))
	}
	if !HasRefs(res.Payload) {
		t.Fatal("deduped payload carries no reference")
	}
	if res.SignalType != "llm_invocation" || res.AgentName != "TraceCorpusAgent" {
		t.Fatalf("classification = %q/%q", res.SignalType, res.AgentName)
	}

	byHash := map[string]string{}
	for _, b := range res.Blobs {
		byHash[b.Hash] = b.Content
	}
	refs := Refs(res.Payload)
	if len(refs) != 2 {
		t.Fatalf("Refs = %v, want 2 hashes", refs)
	}
	back, err := Rehydrate(res.Payload, func(h string) (string, bool) { c, ok := byHash[h]; return c, ok })
	if err != nil {
		t.Fatal(err)
	}
	expected := decodeJSON(t, []byte(llmInvocationFixture))
	forEachLLMInvocationConfig(expected, func(config map[string]any) {
		config["tools"] = decodeJSON(t, []byte(canonicalizeTools(config["tools"])))
	})
	if !reflect.DeepEqual(decodeJSON(t, back), expected) {
		t.Fatalf("rehydrated payload differs from original up to tool order:\n%s\n%s", back, llmInvocationFixture)
	}
	if !strings.Contains(string(back), "9007199254740993") {
		t.Fatal("large integer altered across the round trip")
	}
}

func TestProcess_ToolOrderDoesNotChangeHash(t *testing.T) {
	a := Process([]byte(llmInvocationFixture))
	swapped := strings.Replace(llmInvocationFixture,
		`[{"name":"zeta_tool","description":"z","parameters":{"type":"object"}},{"name":"alpha_tool","description":"a","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}}]`,
		`[{"name":"alpha_tool","description":"a","parameters":{"type":"object","properties":{"n":{"type":"integer"}}}},{"name":"zeta_tool","description":"z","parameters":{"type":"object"}}]`, 1)
	if swapped == llmInvocationFixture {
		t.Fatal("fixture rewrite did not apply")
	}
	b := Process([]byte(swapped))
	hashes := func(r Result) map[string]string {
		m := map[string]string{}
		for _, bl := range r.Blobs {
			m[bl.Kind] = bl.Hash
		}
		return m
	}
	if hashes(a)[KindTools] != hashes(b)[KindTools] {
		t.Fatalf("tools hash depends on declaration order: %s vs %s", hashes(a)[KindTools], hashes(b)[KindTools])
	}
}

func TestProcess_IdempotentOnAlreadyDedupedPayload(t *testing.T) {
	first := Process([]byte(llmInvocationFixture))
	second := Process(first.Payload)
	if second.Changed {
		t.Fatal("second pass extracted again from a payload that only holds references")
	}
	if string(second.Payload) != string(first.Payload) {
		t.Fatal("second pass altered the payload")
	}
}

func TestRehydrate_LeavesUnknownReferenceInPlace(t *testing.T) {
	res := Process([]byte(llmInvocationFixture))
	back, err := Rehydrate(res.Payload, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if !HasRefs(back) {
		t.Fatal("unknown reference was dropped instead of preserved")
	}
}

func TestProcess_ClassifiesNonLLMEvents(t *testing.T) {
	cases := []struct {
		name, payload, signal, agent, tool string
	}{
		{"request", `{"jsonrpc":"2.0","id":"1","method":"message/send","params":{"message":{"kind":"message","metadata":{"agent_name":"Orchestrator"},"parts":[{"kind":"text","text":"hi"}]}}}`, "request", "Orchestrator", ""},
		{"text delta", `{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"metadata":{"agent_name":"A"},"status":{"state":"working","message":{"kind":"message","parts":[{"kind":"text","text":"partial"}]}}}}`, "text", "A", ""},
		{"tool result", `{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"status":{"state":"working","message":{"kind":"message","metadata":{"agent_name":"B"},"parts":[{"kind":"data","data":{"type":"tool_result","tool_name":"list_artifacts","result_data":{}}}]}}}}`, "tool_result", "B", "list_artifacts"},
		{"final task", `{"jsonrpc":"2.0","id":"1","result":{"kind":"task","id":"t","status":{"state":"completed","message":{"kind":"message","parts":[{"kind":"text","text":"done"}]}}}}`, "task", "", ""},
		{"error", `{"jsonrpc":"2.0","id":"1","error":{"code":-32000,"message":"boom"}}`, "error", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Process([]byte(tc.payload))
			if res.Changed {
				t.Fatal("non-llm payload must not be rewritten")
			}
			if res.SignalType != tc.signal || res.AgentName != tc.agent || res.ToolName != tc.tool {
				t.Fatalf("got %q/%q/%q, want %q/%q/%q", res.SignalType, res.AgentName, res.ToolName, tc.signal, tc.agent, tc.tool)
			}
		})
	}
}

func TestProcess_InvalidJSONIsReturnedUntouched(t *testing.T) {
	in := []byte(`{"llm_invocation" "config" not json`)
	res := Process(in)
	if res.Changed || string(res.Payload) != string(in) || res.SignalType != "" {
		t.Fatalf("invalid payload was altered: %+v", res)
	}
}

func TestProcess_ReadsForwardedFromPeer(t *testing.T) {
	cases := []struct{ name, payload string }{
		{"inner message metadata", `{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"status":{"state":"working","message":{"kind":"message","metadata":{"agent_name":"Parent","forwarded_from_peer":"Peer"},"parts":[{"kind":"data","data":{"type":"llm_invocation","request":{"model":"m","contents":[]}}}]}}}}`},
		{"result metadata", `{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"metadata":{"agent_name":"Parent","forwarded_from_peer":"Peer"},"status":{"state":"working","message":{"kind":"message","parts":[{"kind":"data","data":{"type":"llm_response","usage":null}}]}}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Process([]byte(tc.payload))
			if res.AgentName != "Parent" || res.ForwardedFrom != "Peer" {
				t.Fatalf("agent/forwarded = %q/%q, want Parent/Peer", res.AgentName, res.ForwardedFrom)
			}
		})
	}
	direct := Process([]byte(`{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"metadata":{"agent_name":"Parent"},"status":{"state":"working","message":{"kind":"message","parts":[{"kind":"text","text":"hi"}]}}}}`))
	if direct.ForwardedFrom != "" {
		t.Fatalf("direct event reported ForwardedFrom %q", direct.ForwardedFrom)
	}
}

func TestProcess_LeavesPayloadWithDivergentConfigsInline(t *testing.T) {
	toolsNamed := func(name string) string {
		return `[{"function_declarations":[{"name":"` + name + `","description":"a","parameters":{"type":"object"}}]}]`
	}
	const tools = `[{"function_declarations":[{"name":"alpha_tool","description":"a","parameters":{"type":"object"}}]}]`
	payloadWith := func(firstPrompt, firstTools, secondPrompt, secondTools string) string {
		part := func(prompt, tools string) string {
			return `{"kind":"data","data":{"type":"llm_invocation","request":{"model":"openai/e2e-mock","contents":[],"config":{"system_instruction":"` + prompt + `","tools":` + tools + `}}}}`
		}
		return `{"jsonrpc":"2.0","id":"1","result":{"kind":"status-update","final":false,"metadata":{"agent_name":"TwoPartAgent"},"status":{"state":"working","message":{"kind":"message","parts":[` + part(firstPrompt, firstTools) + `,` + part(secondPrompt, secondTools) + `]}}}}`
	}
	payload := func(firstPrompt, secondPrompt string) string {
		return payloadWith(firstPrompt, tools, secondPrompt, tools)
	}
	assertInline := func(t *testing.T, in string) {
		t.Helper()
		res := Process([]byte(in))
		if res.Changed || !res.Divergent {
			t.Fatalf("payload with divergent configs: changed=%v divergent=%v, want false/true", res.Changed, res.Divergent)
		}
		if len(res.Blobs) != 0 {
			t.Fatalf("blobs = %d, want 0", len(res.Blobs))
		}
		if res.PromptHash != "" || res.ToolsHash != "" {
			t.Fatalf("hashes = %q/%q, want empty", res.PromptHash, res.ToolsHash)
		}
		if string(res.Payload) != in {
			t.Fatalf("payload altered:\n%s\n%s", res.Payload, in)
		}
		if res.AgentName != "TwoPartAgent" || res.SignalType != "llm_invocation" {
			t.Fatalf("typed columns lost on the inline path: agent=%q signal=%q", res.AgentName, res.SignalType)
		}
	}

	t.Run("divergent prompts", func(t *testing.T) {
		assertInline(t, payload("You are the first agent.", "You are the second agent."))
	})

	t.Run("divergent tools", func(t *testing.T) {
		assertInline(t, payloadWith("You are the only agent.", toolsNamed("alpha_tool"), "You are the only agent.", toolsNamed("beta_tool")))
	})

	t.Run("identical configs", func(t *testing.T) {
		res := Process([]byte(payload("You are the only agent.", "You are the only agent.")))
		if !res.Changed || res.Divergent {
			t.Fatalf("two identical configs: changed=%v divergent=%v, want true/false", res.Changed, res.Divergent)
		}
		if len(res.Blobs) != 2 {
			t.Fatalf("blobs = %d, want 2", len(res.Blobs))
		}
		if res.PromptHash == "" || res.ToolsHash == "" {
			t.Fatalf("hashes = %q/%q, want both set", res.PromptHash, res.ToolsHash)
		}
	})
}
