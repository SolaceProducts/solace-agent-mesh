// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"testing"
)

func TestBuildDocument_SortsEventsByCreatedTime(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}
	events := []Event{
		{TaskID: hdr.RootTaskID, CreatedTime: 300, Topic: "t3", Direction: "response"},
		{TaskID: hdr.RootTaskID, CreatedTime: 100, Topic: "t1", Direction: "request"},
		{TaskID: hdr.RootTaskID, CreatedTime: 200, Topic: "t2", Direction: "status"},
	}

	doc := BuildDocument(hdr, events)

	flow := doc["invocation_flow"].([]map[string]any)
	if len(flow) != 3 {
		t.Fatalf("flow length = %d, want 3", len(flow))
	}
	for i, want := range []int64{100, 200, 300} {
		got := flow[i]["created_time"].(int64)
		if got != want {
			t.Errorf("flow[%d].created_time = %d, want %d", i, got, want)
		}
	}
}

func TestBuildDocument_DoesNotMutateInputSlice(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}
	events := []Event{
		{CreatedTime: 300},
		{CreatedTime: 100},
		{CreatedTime: 200},
	}

	_ = BuildDocument(hdr, events)

	if events[0].CreatedTime != 300 || events[1].CreatedTime != 100 || events[2].CreatedTime != 200 {
		t.Errorf("input slice was reordered: got %v", []int64{events[0].CreatedTime, events[1].CreatedTime, events[2].CreatedTime})
	}
}

func TestBuildDocument_HeaderFieldsReflectedInInvocationDetails(t *testing.T) {
	hdr := Header{
		RootTaskID:         "abcdef12-3456-7890-0000-000000000000",
		UserID:             "user-1",
		StartTime:          1700000000000,
		EndTime:            1700000005000,
		Status:             "failed",
		InitialRequestText: "Hello world",
		TotalTasks:         3,
		IncludesChildTasks: true,
	}

	doc := BuildDocument(hdr, nil)
	details := doc["invocation_details"].(map[string]any)

	cases := map[string]any{
		"log_file_version":     "2.0",
		"task_id":              hdr.RootTaskID,
		"user_id":              hdr.UserID,
		"start_time":           hdr.StartTime,
		"end_time":             hdr.EndTime,
		"status":               hdr.Status,
		"initial_request_text": hdr.InitialRequestText,
		"total_tasks":          hdr.TotalTasks,
		"includes_child_tasks": hdr.IncludesChildTasks,
	}
	for key, want := range cases {
		if got := details[key]; got != want {
			t.Errorf("invocation_details[%q] = %v (%T), want %v (%T)", key, got, got, want, want)
		}
	}
}

func TestBuildDocument_PayloadParsedAsJSONWhenValid(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}
	events := []Event{{
		TaskID:      hdr.RootTaskID,
		CreatedTime: 100,
		Payload:     `{"kind":"text","value":42}`,
	}}

	doc := BuildDocument(hdr, events)
	flow := doc["invocation_flow"].([]map[string]any)
	payload, ok := flow[0]["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %T, want map[string]any (JSON-parsed)", flow[0]["payload"])
	}
	if payload["kind"] != "text" {
		t.Errorf("payload.kind = %v, want %q", payload["kind"], "text")
	}
}

func TestBuildDocument_PayloadFallsBackToRawStringWhenInvalidJSON(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}
	events := []Event{{
		TaskID:      hdr.RootTaskID,
		CreatedTime: 100,
		Payload:     "not-json {oops",
	}}

	doc := BuildDocument(hdr, events)
	flow := doc["invocation_flow"].([]map[string]any)
	if got := flow[0]["payload"]; got != "not-json {oops" {
		t.Errorf("payload = %v, want raw string fallback", got)
	}
}

func TestBuildDocument_UserIDOmittedWhenNil(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}
	uid := "user-1"
	events := []Event{
		{TaskID: hdr.RootTaskID, CreatedTime: 100, UserID: &uid},
		{TaskID: hdr.RootTaskID, CreatedTime: 200},
	}

	doc := BuildDocument(hdr, events)
	flow := doc["invocation_flow"].([]map[string]any)

	if flow[0]["user_id"] != "user-1" {
		t.Errorf("flow[0].user_id = %v, want %q", flow[0]["user_id"], "user-1")
	}
	if _, present := flow[1]["user_id"]; present {
		t.Errorf("flow[1].user_id present when Event.UserID is nil; want key omitted")
	}
}

func TestBuildDocument_EventIDUsesRootTaskIDPrefix(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef1234567890"}
	events := []Event{
		{TaskID: hdr.RootTaskID, CreatedTime: 100},
		{TaskID: hdr.RootTaskID, CreatedTime: 200},
	}

	doc := BuildDocument(hdr, events)
	flow := doc["invocation_flow"].([]map[string]any)
	want := []string{"evt-abcdef12-000", "evt-abcdef12-001"}
	for i, w := range want {
		if got := flow[i]["id"].(string); got != w {
			t.Errorf("flow[%d].id = %q, want %q", i, got, w)
		}
	}
}

func TestBuildDocument_ShortRootTaskIDNotTruncated(t *testing.T) {
	hdr := Header{RootTaskID: "abc"}
	events := []Event{{TaskID: "abc", CreatedTime: 100}}

	doc := BuildDocument(hdr, events)
	flow := doc["invocation_flow"].([]map[string]any)
	if got := flow[0]["id"].(string); got != "evt-abc-000" {
		t.Errorf("flow[0].id = %q, want %q", got, "evt-abc-000")
	}
}

func TestBuildDocument_EmptyEventsProducesEmptyFlow(t *testing.T) {
	hdr := Header{RootTaskID: "abcdef12-3456-7890-0000-000000000000"}

	doc := BuildDocument(hdr, nil)
	flow, ok := doc["invocation_flow"].([]map[string]any)
	if !ok {
		t.Fatalf("invocation_flow = %T, want []map[string]any", doc["invocation_flow"])
	}
	if len(flow) != 0 {
		t.Errorf("flow length = %d, want 0", len(flow))
	}
	if flow == nil {
		t.Errorf("flow is nil; want empty non-nil slice so it JSON-marshals as [] not null")
	}
}
