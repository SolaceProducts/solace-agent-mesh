// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestScheduledTaskManageTools_Metadata(t *testing.T) {
	cases := []struct {
		tl       tool.BuiltinTool
		name     string
		required []string
	}{
		{&ListScheduledTasksTool{}, "list_scheduled_tasks", nil},
		{&UpdateScheduledTaskTool{}, "update_scheduled_task", []string{"task_id"}},
		{&DeleteScheduledTaskTool{}, "delete_scheduled_task", []string{"task_id"}},
		{&SetScheduledTaskEnabledTool{}, "set_scheduled_task_enabled", []string{"task_id", "enabled"}},
	}
	for _, c := range cases {
		if c.tl.Name() != c.name {
			t.Errorf("Name = %q, want %q", c.tl.Name(), c.name)
		}
		if strings.TrimSpace(c.tl.Description()) == "" {
			t.Errorf("%s: description is empty", c.name)
		}
		p := c.tl.Parameters()
		if p == nil || p.Type != "object" || p.Properties == nil {
			t.Fatalf("%s: bad parameters schema %+v", c.name, p)
		}
		if !slices.Equal(p.Required, c.required) {
			t.Errorf("%s: required = %v, want %v", c.name, p.Required, c.required)
		}
	}
}

func TestScheduledTaskControl_GatewayErrorSurfaced(t *testing.T) {
	f := &fakeControlQuerier{err: fmt.Errorf("boom from gateway")}
	res, err := (&DeleteScheduledTaskTool{}).Execute(context.Background(),
		map[string]any{"task_id": "t1"}, &tool.Context{ControlQuerier: f, UserID: "u1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError || !strings.Contains(res.Message, "boom from gateway") {
		t.Errorf("status=%v message=%q, want error carrying the gateway message", res.Status, res.Message)
	}
}

func TestScheduledTaskControl_BadResponseSurfaced(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{not valid json`)}
	res, err := (&ListScheduledTasksTool{}).Execute(context.Background(),
		map[string]any{}, &tool.Context{ControlQuerier: f, UserID: "u1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("status = %v, want error on unparseable response", res.Status)
	}
}

func TestScheduledTaskManageTools_NoClientScopes(t *testing.T) {
	tools := []tool.BuiltinTool{
		&ListScheduledTasksTool{},
		&UpdateScheduledTaskTool{},
		&DeleteScheduledTaskTool{},
		&SetScheduledTaskEnabledTool{},
	}
	for _, tl := range tools {
		if got := tool.RequiredScopesOf(tl); got != nil {
			t.Errorf("%s.RequiredScopes = %v, want nil", tl.Name(), got)
		}
	}
}

func TestListScheduledTasks_RoutesAndCarriesUser(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"tasks":[],"total":0}`)}
	toolCtx := &tool.Context{ControlQuerier: f, UserID: "alice@example.com"}

	res, err := (&ListScheduledTasksTool{}).Execute(context.Background(), map[string]any{"enabled_only": true}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (message: %s)", res.Status, res.Message)
	}
	if f.app != "gateway" || f.method != "get" || f.path != "scheduledTasks" {
		t.Errorf("routed to %s/%s/%s, want gateway/get/scheduledTasks", f.app, f.method, f.path)
	}
	var body map[string]any
	if err := json.Unmarshal(f.body, &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["enabledOnly"] != true {
		t.Errorf("enabledOnly not forwarded: %s", f.body)
	}
	if body["userId"] != "alice@example.com" {
		t.Errorf("advisory userId not forwarded: %s", f.body)
	}
}

func TestUpdateScheduledTask_RoutesToPatch(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"id":"t1","name":"renamed"}`)}
	toolCtx := &tool.Context{ControlQuerier: f, UserID: "u1"}

	res, err := (&UpdateScheduledTaskTool{}).Execute(context.Background(), map[string]any{
		"task_id": "t1",
		"name":    "renamed",
		"message": "new instruction",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (message: %s)", res.Status, res.Message)
	}
	if f.method != "patch" || f.path != "scheduledTasks/t1" {
		t.Errorf("routed to %s %s, want patch scheduledTasks/t1", f.method, f.path)
	}
	var body map[string]any
	_ = json.Unmarshal(f.body, &body)
	if body["name"] != "renamed" || body["message"] != "new instruction" {
		t.Errorf("update fields not forwarded: %s", f.body)
	}
}

func TestUpdateScheduledTask_RequiresIDAndAtLeastOneField(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{}`)}
	toolCtx := &tool.Context{ControlQuerier: f, UserID: "u1"}
	tl := &UpdateScheduledTaskTool{}

	if res, _ := tl.Execute(context.Background(), map[string]any{"name": "x"}, toolCtx); res.Status != tool.StatusError {
		t.Errorf("missing task_id should error, got %v", res.Status)
	}
	if res, _ := tl.Execute(context.Background(), map[string]any{"task_id": "t1"}, toolCtx); res.Status != tool.StatusError {
		t.Errorf("update with no fields should error, got %v", res.Status)
	}
}

func TestDeleteScheduledTask_RoutesToDelete(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{"success":true,"deleted":true}`)}
	toolCtx := &tool.Context{ControlQuerier: f, UserID: "u1"}

	res, err := (&DeleteScheduledTaskTool{}).Execute(context.Background(), map[string]any{"task_id": "t9"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (message: %s)", res.Status, res.Message)
	}
	if f.method != "delete" || f.path != "scheduledTasks/t9" {
		t.Errorf("routed to %s %s, want delete scheduledTasks/t9", f.method, f.path)
	}
}

func TestSetScheduledTaskEnabled_RoutesEnableDisable(t *testing.T) {
	cases := []struct {
		enabled  bool
		wantPath string
	}{
		{true, "scheduledTasks/t1/enable"},
		{false, "scheduledTasks/t1/disable"},
	}
	for _, tc := range cases {
		f := &fakeControlQuerier{resp: json.RawMessage(`{"id":"t1"}`)}
		toolCtx := &tool.Context{ControlQuerier: f, UserID: "u1"}
		res, err := (&SetScheduledTaskEnabledTool{}).Execute(context.Background(), map[string]any{
			"task_id": "t1",
			"enabled": tc.enabled,
		}, toolCtx)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != tool.StatusSuccess {
			t.Fatalf("status = %v (message: %s)", res.Status, res.Message)
		}
		if f.method != "post" || f.path != tc.wantPath {
			t.Errorf("enabled=%v routed to %s %s, want post %s", tc.enabled, f.method, f.path, tc.wantPath)
		}
	}
}

func TestSetScheduledTaskEnabled_RequiresArgs(t *testing.T) {
	f := &fakeControlQuerier{resp: json.RawMessage(`{}`)}
	toolCtx := &tool.Context{ControlQuerier: f, UserID: "u1"}
	tl := &SetScheduledTaskEnabledTool{}

	if res, _ := tl.Execute(context.Background(), map[string]any{"enabled": true}, toolCtx); res.Status != tool.StatusError {
		t.Errorf("missing task_id should error, got %v", res.Status)
	}
	if res, _ := tl.Execute(context.Background(), map[string]any{"task_id": "t1"}, toolCtx); res.Status != tool.StatusError {
		t.Errorf("missing enabled should error, got %v", res.Status)
	}
}

func TestScheduledTaskManageTools_NoControlQuerier(t *testing.T) {
	empty := &tool.Context{}
	checks := []struct {
		name string
		run  func() *tool.Result
	}{
		{"list", func() *tool.Result {
			r, _ := (&ListScheduledTasksTool{}).Execute(context.Background(), map[string]any{}, empty)
			return r
		}},
		{"update", func() *tool.Result {
			r, _ := (&UpdateScheduledTaskTool{}).Execute(context.Background(), map[string]any{"task_id": "t1", "name": "x"}, empty)
			return r
		}},
		{"delete", func() *tool.Result {
			r, _ := (&DeleteScheduledTaskTool{}).Execute(context.Background(), map[string]any{"task_id": "t1"}, empty)
			return r
		}},
		{"set_enabled", func() *tool.Result {
			r, _ := (&SetScheduledTaskEnabledTool{}).Execute(context.Background(), map[string]any{"task_id": "t1", "enabled": true}, empty)
			return r
		}},
	}
	for _, c := range checks {
		if res := c.run(); res.Status != tool.StatusError {
			t.Errorf("%s with no ControlQuerier should error, got %v", c.name, res.Status)
		}
	}
}
