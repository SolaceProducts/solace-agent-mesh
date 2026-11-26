// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"testing"
)

func TestDeepResearchPlanData_Defaults(t *testing.T) {
	d := DeepResearchPlanData{
		Type:              string(SignalDeepResearchPlan),
		PlanID:            "abc-123",
		AgentName:         "ResearchAgent",
		Title:             "AI Research",
		ResearchQuestion:  "What is AI?",
		Steps:             []string{"Step 1", "Step 2"},
		MaxIterations:     5,
		MaxRuntimeSeconds: 300,
	}
	if d.GetSignalType() != "deep_research_plan" {
		t.Fatalf("GetSignalType = %q, want %q", d.GetSignalType(), "deep_research_plan")
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantKeys := []string{"type", "plan_id", "agent_name", "title", "research_question", "steps", "research_type", "max_iterations", "max_runtime_seconds", "sources"}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in marshalled output: %s", k, b)
		}
	}
	if got["plan_id"] != "abc-123" {
		t.Errorf("plan_id = %v, want abc-123", got["plan_id"])
	}
}

func TestDeepResearchPlanData_RoundTrip(t *testing.T) {
	in := DeepResearchPlanData{
		Type:              "deep_research_plan",
		PlanID:            "p1",
		AgentName:         "ResearchAgent",
		Title:             "T",
		ResearchQuestion:  "Q?",
		Steps:             []string{"a", "b"},
		ResearchType:      "in-depth",
		MaxIterations:     10,
		MaxRuntimeSeconds: 600,
		Sources:           []string{"web", "kb"},
	}
	b, _ := json.Marshal(in)
	var out DeepResearchPlanData
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ResearchType != "in-depth" || len(out.Sources) != 2 || out.Sources[1] != "kb" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestDeepResearchPlanStaleData_Wire(t *testing.T) {
	d := DeepResearchPlanStaleData{
		Type:   string(SignalDeepResearchPlanStale),
		PlanID: "p1",
		Reason: "timed_out",
	}
	if d.GetSignalType() != "deep_research_plan_stale" {
		t.Errorf("GetSignalType = %q", d.GetSignalType())
	}
	b, _ := json.Marshal(d)
	got := string(b)
	for _, sub := range []string{`"type":"deep_research_plan_stale"`, `"plan_id":"p1"`, `"reason":"timed_out"`} {
		if !contains(got, sub) {
			t.Errorf("marshalled output missing %s: %s", sub, got)
		}
	}
}

func TestSamEventsTopic_MatchesPython(t *testing.T) {
	got := SamEventsTopic("acme/sam", "deep_research", "plan_response")
	want := "acme/sam/sam/events/deep_research/plan_response"
	if got != want {
		t.Errorf("SamEventsTopic = %q, want %q", got, want)
	}
	got = SamEventsSubscription("acme/sam", "deep_research")
	want = "acme/sam/sam/events/deep_research/>"
	if got != want {
		t.Errorf("SamEventsSubscription = %q, want %q", got, want)
	}
}

func TestUserProperties_GatewayCapabilities_Encode(t *testing.T) {
	up := &UserProperties{
		Timestamp:           1700000000000,
		ClientID:            "webui",
		UserID:              "u1",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": true},
	}
	m := EncodeUserProperties(up)
	raw, ok := m[PropGatewayCapabilities]
	if !ok {
		t.Fatalf("encoded props missing %q: %#v", PropGatewayCapabilities, m)
	}
	var caps map[string]bool
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, raw)
	}
	if !caps["interactive_plan_verification"] {
		t.Errorf("expected interactive_plan_verification=true, got %v", caps)
	}
}

func TestUserProperties_GatewayCapabilities_Decode(t *testing.T) {
	m := map[string]string{
		PropTimestamp:           "1700000000000",
		PropClientID:            "webui",
		PropGatewayCapabilities: `{"interactive_plan_verification":true}`,
	}
	up := DecodeUserProperties(m)
	if !up.GatewayCapabilities["interactive_plan_verification"] {
		t.Errorf("decoded gateway caps wrong: %#v", up.GatewayCapabilities)
	}
}

func TestUserProperties_GatewayCapabilities_DecodeMalformedSilent(t *testing.T) {
	m := map[string]string{
		PropTimestamp:           "1",
		PropGatewayCapabilities: "not-json",
	}
	up := DecodeUserProperties(m)
	if up.GatewayCapabilities != nil {
		t.Errorf("expected nil GatewayCapabilities on malformed input, got %#v", up.GatewayCapabilities)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
