// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"gopkg.in/yaml.v3"
)

var riskyStimStrings = []string{
	"",
	"Here's my plan:\n    indented code\nback",
	"  starts indented\nthen normal",
	"Plan:\n- a\n    - nested\n- b",
	"trailing space \nx",
}

var blockScalarHeader = regexp.MustCompile(`(?m):\s*[|>][0-9]*[-+]?\s*$`)

func TestServeStimExport_OutputRoundTrips(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	taskRepo := store.NewTaskRepo(core.Store())
	eventRepo := store.NewTaskEventRepo(core.Store())
	ctx := context.Background()
	th := &taskHandler{core: core, taskRepo: taskRepo, eventRepo: eventRepo, logger: adapter.logger}

	for _, text := range riskyStimStrings {
		tid := idgen.New()
		if err := taskRepo.Create(ctx, &store.Task{ID: tid, UserID: "sam_dev_user", StartTime: 1000, ExecutionMode: "foreground"}); err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{
			"jsonrpc": "2.0",
			"result": map[string]any{
				"taskId": tid,
				"status": map[string]any{
					"state": "working",
					"message": map[string]any{
						"parts": []any{map[string]any{"kind": "text", "text": text}},
					},
				},
			},
		}
		pb, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: idgen.New(), TaskID: tid, CreatedTime: 1001, Topic: "status", Direction: "status", Payload: string(pb),
		}); err != nil {
			t.Fatal(err)
		}

		task, getErr := taskRepo.GetByID(ctx, tid)
		if getErr != nil || task == nil {
			t.Fatalf("get task: %v", getErr)
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/tasks/"+tid+"/events?format=stim", nil)
		th.serveStimExport(rec, req, task, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("export code = %d; body: %s", rec.Code, rec.Body.String())
		}
		if loc := blockScalarHeader.FindString(rec.Body.String()); loc != "" {
			t.Errorf("export emitted a block scalar (%q) for %q", strings.TrimSpace(loc), text)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("exported .stim is not parseable YAML (the original bug) for %q: %v\n%s", text, err, rec.Body.String())
		}
		flow, ok := doc["invocation_flow"].([]any)
		if !ok || len(flow) == 0 {
			t.Fatalf("exported .stim missing invocation_flow: %#v", doc["invocation_flow"])
		}
		result := flow[0].(map[string]any)["payload"].(map[string]any)["result"].(map[string]any)
		parts := result["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)
		part, ok := parts[0].(map[string]any)
		if !ok {
			t.Fatalf("part 0 is not a mapping for %q: %#v", text, parts[0])
		}
		raw, present := part["text"]
		if !present {
			t.Fatalf("exported part has no text key for %q: %#v", text, part)
		}
		got, isStr := raw.(string)
		if !isStr {
			t.Fatalf("exported text is %T, not a string, for %q", raw, text)
		}
		if got != text {
			t.Errorf("exported text = %q, want %q", got, text)
		}
	}
}
