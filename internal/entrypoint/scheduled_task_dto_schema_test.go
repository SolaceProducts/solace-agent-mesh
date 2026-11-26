// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestScheduledTaskDTO_MatchesToolResultSchema(t *testing.T) {
	dtoType := reflect.TypeOf(scheduledTaskDTO{})
	var wire []string
	for i := range dtoType.NumField() {
		tag := dtoType.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s carries no usable json tag (%q); the tools key off these",
				dtoType.Field(i).Name, tag)
		}
		wire = append(wire, name)
	}
	slices.Sort(wire)

	schema := tool.ResultSchemaOf(&tools.UpdateScheduledTaskTool{})
	if schema == nil {
		t.Fatal("update_scheduled_task declares no result schema")
	}
	declared := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		declared = append(declared, field)
	}
	slices.Sort(declared)

	if !slices.Equal(wire, declared) {
		t.Errorf("scheduledTaskDTO json tags = %v\nUpdateScheduledTaskTool.ResultSchema() = %v\n"+
			"update pkg/tools/scheduled_task_manage.go scheduledTaskDTOFields to match, in this change",
			wire, declared)
	}
}

func TestScheduledTaskControlShapes_MatchToolResultSchemas(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	task := makeEnabledTask(t, svc, "some-agent")
	exe := &GatewayExecutor{logger: slog.New(slog.DiscardHandler)}

	list, err := exe.listScheduledTasks(t.Context(), svc, nil, nil)
	if err != nil {
		t.Fatalf("listScheduledTasks: %v", err)
	}
	assertKeysMatchSchema(t, "list_scheduled_tasks", list, &tools.ListScheduledTasksTool{})

	disabled, err := exe.setScheduledTaskEnabled(t.Context(), svc, task.ID, nil, nil, false)
	if err != nil {
		t.Fatalf("setScheduledTaskEnabled: %v", err)
	}
	assertKeysMatchSchema(t, "set_scheduled_task_enabled", disabled, &tools.SetScheduledTaskEnabledTool{})

	deleted, err := exe.deleteScheduledTask(t.Context(), svc, task.ID, nil, nil)
	if err != nil {
		t.Fatalf("deleteScheduledTask: %v", err)
	}
	assertKeysMatchSchema(t, "delete_scheduled_task", deleted, &tools.DeleteScheduledTaskTool{})
}

var unproducedByAnyCase = map[string]map[string]bool{
	"set_scheduled_task_enabled": {
		"success":     true,
		"description": true,
		"lastRunTime": true,
		"nextRunTime": true,
	},
}

func assertKeysMatchSchema(t *testing.T, toolName string, response any, bt tool.BuiltinTool) {
	t.Helper()
	assertRequiredArePresent(t, toolName, response, bt)
	assertNoUndeclaredKeys(t, toolName, response, bt)

	produced := responseKeys(t, toolName, response)
	declared := declaredKeys(t, toolName, bt)
	for _, field := range declared {
		if unproducedByAnyCase[toolName][field] {
			continue
		}
		if !slices.Contains(produced, field) {
			t.Errorf("%s declares %q but the gateway handler does not produce it (produced %v) — "+
				"add a case that reaches it, or record it in unproducedByAnyCase with the reason",
				toolName, field, produced)
		}
	}
}

func assertNoUndeclaredKeys(t *testing.T, toolName string, response any, bt tool.BuiltinTool) {
	t.Helper()
	declared := declaredKeys(t, toolName, bt)
	for _, key := range responseKeys(t, toolName, response) {
		if !slices.Contains(declared, key) {
			t.Errorf("gateway handler produces %q, which %s does not declare — a reference to it "+
				"would be rejected; add it to the tool's ResultSchema in this change",
				key, toolName)
		}
	}
}

func assertRequiredArePresent(t *testing.T, toolName string, response any, bt tool.BuiltinTool) {
	t.Helper()
	schema := tool.ResultSchemaOf(bt)
	if schema == nil {
		t.Fatalf("%s declares no result schema", toolName)
	}
	produced := responseKeys(t, toolName, response)
	for _, req := range schema.Required {
		if !slices.Contains(produced, req) {
			t.Errorf("%s marks %q required but the gateway handler did not produce it (produced %v)",
				toolName, req, produced)
		}
	}
}

func responseKeys(t *testing.T, toolName string, response any) []string {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("%s: marshal handler response: %v", toolName, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: handler response is not a JSON object: %v", toolName, err)
	}
	keys := make([]string, 0, len(decoded))
	for key := range decoded {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func declaredKeys(t *testing.T, toolName string, bt tool.BuiltinTool) []string {
	t.Helper()
	schema := tool.ResultSchemaOf(bt)
	if schema == nil {
		t.Fatalf("%s declares no result schema", toolName)
	}
	keys := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		keys = append(keys, field)
	}
	slices.Sort(keys)
	return keys
}
