// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type evalStub struct {
	mu        sync.Mutex
	pollCount map[string]int
	cancelled []string
	triggers  int
	interrupt sync.Once
}

var evalStubExperiments = []map[string]any{
	{"id": "exp1-id", "name": "exp1", "datasetName": "ds1", "targetAgent": "orchestrator", "latestRunStatus": "completed"},
	{"id": "exp2-id", "name": "exp2", "datasetName": "ds2", "targetAgent": "orchestrator"},
	{"id": "multi-id", "name": "multi", "datasetName": "ds3", "targetAgent": "orchestrator"},
	{"id": "multifail-id", "name": "multi-failing", "datasetName": "ds3", "targetAgent": "orchestrator"},
	{"id": "fail-id", "name": "failing", "datasetName": "ds4", "targetAgent": "orchestrator"},
	{"id": "cancel-id", "name": "cancel", "datasetName": "ds5", "targetAgent": "orchestrator"},
	{"id": "busy-id", "name": "busy", "datasetName": "ds6", "targetAgent": "orchestrator"},
	{"id": "gone-id", "name": "gone", "datasetName": "ds7", "targetAgent": "orchestrator"},
	{"id": "interrupt-id", "name": "interrupt", "datasetName": "ds8", "targetAgent": "orchestrator"},
	{"id": "warn-id", "name": "warned", "datasetName": "ds6", "targetAgent": "orchestrator"},
	{"id": "trials-id", "name": "two-trials", "datasetName": "ds8", "targetAgent": "orchestrator", "runsPerExample": 2},
	{"id": "lost-id", "name": "lost-trial", "datasetName": "ds9", "targetAgent": "orchestrator", "runsPerExample": 3},
	{"id": "warnended-id", "name": "mostly-ended", "datasetName": "ds7", "targetType": "workflow", "targetAgent": "", "targetWorkflow": "order_flow"},
	{"id": "wf-id", "name": "order-flow", "datasetName": "ds5", "targetType": "workflow", "targetAgent": "", "targetWorkflow": "order_flow"},
	{"id": "wfm-id", "name": "order-flow-models", "datasetName": "ds5", "targetType": "workflow", "targetAgent": "", "targetWorkflow": "order_flow",
		"models": []map[string]any{{"model_id": "general"}, {"model_id": "planning"}}},
}

const (
	warnedRun      = "run-warn-id"
	mostlyEndedRun = "run-warnended-id"
	lostRun        = "run-lost-id"
)

const untrackedFailedRun = "run-mf2"

func runStatus(runID string, n int) (string, int, int) {
	total := 2
	switch runID {
	case mostlyEndedRun:
		total = 7
	case lostRun:
		total = 9
	}
	switch {
	case n < 2 || strings.HasPrefix(runID, "run-i"):
		return "running", 1, total
	case strings.Contains(runID, "cancel"):
		return "cancelled", 1, total
	case runID == warnedRun || runID == mostlyEndedRun:
		return "completed_with_warnings", total, total
	default:
		return "completed", total, total
	}
}

func (s *evalStub) triggerRuns(expID string) []string {
	switch expID {
	case "multi-id":
		return []string{"run-m1", "run-m2"}
	case "multifail-id":
		return []string{"run-mf1", untrackedFailedRun}
	case "interrupt-id":
		return []string{"run-i1", "run-i2"}
	default:
		return []string{"run-" + expID}
	}
}

func (s *evalStub) interruptOn(id string) {
	if strings.HasPrefix(id, "run-i") || id == "interrupt-id" {
		s.interrupt.Do(interruptSelf)
	}
}

func (s *evalStub) cancelledRuns() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(slices.Values(s.cancelled))
}

func (s *evalStub) recordCancel(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = append(s.cancelled, runID)
}

func (s *evalStub) poll(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pollCount[key]++
	return s.pollCount[key]
}

func (s *evalStub) handlerV2() http.Handler {
	mux := http.NewServeMux()
	const base = "/api/v2/platform/evaluations"

	mux.HandleFunc("GET /api/v2/platform/agents", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, []map[string]any{})
	})
	mux.HandleFunc("GET "+base+"/experiments", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, evalStubExperiments)
	})
	mux.HandleFunc("GET "+base+"/evaluators", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, []map[string]any{{"id": "ev1", "type": "evaluator", "name": "my-judge", "evaluatorType": "llm_judge"}})
	})
	mux.HandleFunc("PUT "+base+"/experiments/{id}/trigger", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.triggers++
		s.mu.Unlock()
		expID := r.PathValue("id")
		if expID == "busy-id" {
			writeStatus(w, http.StatusConflict, map[string]any{
				"message": "A run of this experiment is already pending or running. Wait for it to finish or cancel it.", "errorId": "e-1"})
			return
		}
		w.Header().Set("Location", base+"/experiments/"+expID+"/operations/op-"+expID)
		writeStatus(w, http.StatusAccepted, map[string]any{"data": s.operation(expID, "pending")})
	})
	mux.HandleFunc("GET "+base+"/runGroups/{id}", func(w http.ResponseWriter, r *http.Request) {
		expID := strings.TrimPrefix(r.PathValue("id"), "op-")
		runs := []map[string]any{}
		for _, rid := range s.triggerRuns(expID) {
			runs = append(runs, s.v2Run(rid, expID, "pending", 0, 2))
		}
		writeData(w, map[string]any{"id": r.PathValue("id"), "type": "runGroup", "experimentId": expID, "runs": runs})
	})
	mux.HandleFunc("GET "+base+"/experiments/{experimentId}/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		expID := r.PathValue("experimentId")
		n := s.poll("op-" + expID)
		if expID == "gone-id" && n >= 2 {
			writeStatus(w, http.StatusNotFound, map[string]any{"message": "Not found", "errorId": "e-2"})
			return
		}
		s.interruptOn(expID)
		status := "inProgress"
		if n >= 2 && expID != "interrupt-id" {
			status = "succeeded"
			if expID == "cancel-id" {
				status = "cancelled"
			}
		}
		operation := s.operation(expID, status)
		if expID == "multifail-id" && status == "succeeded" {
			operation["status"] = "failed"
			operation["error"] = map[string]any{"message": "A run in this operation failed.", "errorId": untrackedFailedRun}
		}
		writeData(w, operation)
	})
	mux.HandleFunc("GET "+base+"/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		status, current, total := runStatus(id, s.poll(id))
		writeData(w, s.v2Run(id, "", status, current, total))
	})
	mux.HandleFunc("PUT "+base+"/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		s.recordCancel(r.PathValue("id"))
		writeData(w, s.v2Run(r.PathValue("id"), "", "cancelled", 0, 2))
	})
	mux.HandleFunc("GET "+base+"/runs/{id}/results", func(w http.ResponseWriter, r *http.Request) {
		writePage(w, v2Results(s.results(r.PathValue("id"))))
	})
	mux.HandleFunc("GET "+base+"/runResults/{id}/executionData", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"runId": "r", "messages": []map[string]any{{"topic": "t", "payload": map[string]any{}}}})
	})
	mux.HandleFunc("GET "+base+"/runResults/{id}/taskEvents", func(w http.ResponseWriter, r *http.Request) {
		events := []map[string]any{}
		if strings.HasSuffix(r.PathValue("id"), "-r1") {
			events = []map[string]any{{"event": "status"}, {"event": "final"}}
		}
		writeData(w, map[string]any{"events": events})
	})
	return mux
}

func (s *evalStub) operation(expID, status string) map[string]any {
	return map[string]any{"id": "op-" + expID, "type": "operation", "resourceId": expID, "operationType": "run", "status": status}
}

func (s *evalStub) v2Run(id, expID, status string, current, total int) map[string]any {
	run := map[string]any{"id": id, "type": "run", "experimentId": expID, "runGroupId": "op-" + expID, "status": status,
		"progressCurrent": current, "progressTotal": total, "model": "gpt-test", "summaryScores": []any{}, "evaluatorStats": []any{},
		"configSnapshot": nil}
	if id == lostRun {
		run["configSnapshot"] = map[string]any{"experiment": nil, "agent": nil,
			"dataset": map[string]any{"id": "ds9", "name": "ds9", "exampleCount": 3, "examples": []any{}}}
	}
	return run
}

func v2Results(results []map[string]any) []map[string]any {
	out := make([]map[string]any, len(results))
	for i, r := range results {
		row := map[string]any{"type": "runResult"}
		for k, v := range r {
			switch k {
			case "executionDataKey", "taskEventsKey":
			case "createdAt":
				row["createdTime"] = fmt.Sprintf("2026-01-01T00:00:%02d.000Z", v)
			case "scores":
				var scores []map[string]any
				for _, score := range v.([]map[string]any) {
					scores = append(scores, map[string]any{"key": score["id"], "evaluatorId": score["evaluatorId"], "score": score["score"],
						"choice": score["choice"], "passed": score["passed"], "reasoning": score["reasoning"],
						"createdTime": fmt.Sprintf("2026-01-01T00:00:%02d.000Z", score["createdAt"])})
				}
				row["scores"] = scores
			default:
				row[k] = v
			}
		}
		out[i] = row
	}
	return out
}

func writePage(w http.ResponseWriter, data any) {
	_ = writeJSON(w, map[string]any{"data": data, "meta": map[string]any{"pagination": map[string]any{"pageNumber": 1, "pageSize": 100, "nextPage": nil}}})
}

func writeStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *evalStub) results(runID string) []map[string]any {
	passed := !strings.Contains(runID, "fail")
	score := 1.0
	choice := "Yes"
	if !passed {
		score, choice = 0.0, "No"
	}
	mk := func(idx int, prompt, evKey string) map[string]any {
		var tek any
		if evKey != "" {
			tek = evKey
		}
		return map[string]any{
			"id": fmt.Sprintf("%s-r%d", runID, idx), "runId": runID, "exampleId": fmt.Sprintf("ex%d", idx),
			"trialIndex": 0, "inputPrompt": prompt, "agentResponse": "an answer",
			"executionDataKey": "ek" + runID, "taskEventsKey": tek, "durationSeconds": 1.2, "createdAt": idx,
			"scores": []map[string]any{
				{"id": fmt.Sprintf("%s-s%d", runID, idx), "evaluatorId": "ev1", "score": score, "choice": choice, "passed": passed, "reasoning": "because", "createdAt": idx},
			},
		}
	}
	if !passed {
		return []map[string]any{mk(1, "a failing prompt", "evk")}
	}
	if runID == "run-trials-id" || runID == lostRun {
		results := []map[string]any{mk(1, "first prompt", ""), mk(1, "first prompt", ""), mk(2, "second prompt", ""), mk(2, "second prompt", "")}
		for i, r := range results {
			r["id"], r["trialIndex"] = fmt.Sprintf("%s-t%d", runID, i), i%2
		}
		failed := results[3]["scores"].([]map[string]any)[0]
		failed["passed"], failed["score"], failed["choice"] = false, 0.0, "No"
		return results
	}
	if runID == mostlyEndedRun {
		partial, errored := mk(2, "second prompt", ""), mk(3, "third prompt", "")
		partial["failureKind"] = "partial_failure"
		errored["errorMessage"] = "Agent error: boom"
		results := []map[string]any{mk(1, "first prompt", "evk"), partial, errored}
		for i := 4; i <= 6; i++ {
			results = append(results, map[string]any{
				"id": fmt.Sprintf("%s-r%d", runID, i), "runId": runID, "exampleId": fmt.Sprintf("ex%d", i),
				"trialIndex": 0, "inputPrompt": "", "failureKind": "node_failed", "createdAt": i, "scores": []map[string]any{},
			})
		}
		return results
	}
	results := []map[string]any{mk(1, "first prompt", "evk"), mk(2, "second prompt", "")}
	if strings.Contains(runID, "wf") {
		results[0]["cost"] = "0.0060000000"
		results[1]["cost"] = nil
	}
	return results
}

func writeData(w http.ResponseWriter, data any) { _ = writeJSON(w, map[string]any{"data": data}) }

func newEvalStubServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := newEvalStub(t)
	return srv
}

func newEvalStub(t *testing.T) (*httptest.Server, *evalStub) {
	t.Helper()
	stub := &evalStub{pollCount: map[string]int{}}
	srv := httptest.NewServer(stub.handlerV2())
	t.Cleanup(srv.Close)
	return srv, stub
}

func runEvalCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

func TestEvalRunE2E_SingleExperimentWithTraces(t *testing.T) {
	srv := newEvalStubServer(t)
	dir := t.TempDir()
	out, errOut, err := runEvalCmd(t, "eval", "run", "exp1",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms", "--output-dir", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	for _, want := range []string{"Run run-exp1-id", "Overall pass rate: 2/2", "my-judge"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout summary missing %q:\nstdout:\n%s", want, out)
		}
	}
	for _, want := range []string{"first prompt", "Wrote traces to"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr diagnostics missing %q:\nstderr:\n%s", want, errOut)
		}
	}
	if strings.Contains(out, "first prompt") {
		t.Errorf("stdout leaked a live per-example line:\n%s", out)
	}
	if strings.Contains(out, "ev1") || strings.Contains(errOut, "ev1") {
		t.Errorf("output leaked raw evaluator id 'ev1':\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	glob := filepath.Join(dir, "exp1", "run-exp1-id")
	for _, f := range []string{"results.json", "ex01-run-exp1.execution.json", "ex01-run-exp1.events.json"} {
		if _, statErr := os.Stat(filepath.Join(glob, f)); statErr != nil {
			t.Errorf("expected trace file %s: %v", f, statErr)
		}
	}
}

func TestEvalRunE2E_MultiExperimentConcurrentPrefixed(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "exp1", "exp2",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	for _, want := range []string{"[exp1]", "[exp2]"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr live output missing prefix %q:\nstderr:\n%s", want, errOut)
		}
		if !strings.Contains(out, want) {
			t.Errorf("stdout summary missing prefix %q:\nstdout:\n%s", want, out)
		}
	}
}

func TestEvalRunE2E_MultiModelWarning(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "multi",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	want := "waits for all of them"
	if !strings.Contains(errOut, "model runs") || !strings.Contains(errOut, want) {
		t.Errorf("expected multi-model warning naming %q on stderr:\n%s", want, errOut)
	}
}

func TestEvalRunE2E_UntrackedRunFailureFailsOnV2(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "multi-failing",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil || !strings.Contains(err.Error(), untrackedFailedRun) {
		t.Fatalf("err = %v, want the failed run %s named\nstdout:\n%s\nstderr:\n%s", err, untrackedFailedRun, out, errOut)
	}
	if !strings.Contains(out, "run-mf1") {
		t.Errorf("stdout = %s, want the tracked run still summarized", out)
	}
}

func TestEvalRunE2E_ThresholdFailReturnsError(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "run", "failing",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil {
		t.Fatalf("expected non-nil error for a failing run\n%s", out)
	}
	if !strings.Contains(err.Error(), "pass rate") {
		t.Errorf("error should mention pass rate, got: %v", err)
	}
}

func TestEvalRunE2E_JSONOutput(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "run", "exp1",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("--format json output is not valid JSON: %v\n%s", jerr, out)
	}
	if obj["experiment"] != "exp1" || obj["passRate"] == nil {
		t.Errorf("unexpected json summary: %v", obj)
	}
	if _, ok := obj["consistency"]; ok {
		t.Errorf("a run with one trial per example reported consistency: %v", obj["consistency"])
	}
}

func TestEvalRunE2E_ConsistencyAcrossTrials(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "two-trials",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms", "--threshold", "0.5")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(out, "Overall pass rate: 3/4 (75.0%)\nConsistency across trials:\n  my-judge: pass^2 1/2 examples, 1 inconsistent, 0 failed every trial\n") {
		t.Errorf("text summary lacks the consistency block after the pass rate:\n%s", out)
	}

	out, _, err = runEvalCmd(t, "eval", "run", "two-trials",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms", "--threshold", "0.5", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var obj struct {
		Consistency []map[string]any `json:"consistency"`
	}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("--format json output is not valid JSON: %v\n%s", jerr, out)
	}
	want := map[string]any{"evaluatorId": "ev1", "evaluatorName": "my-judge", "examples": float64(2), "passedEvery": float64(1),
		"failedEvery": float64(0), "inconsistent": float64(1), "trialsPerExample": float64(2)}
	if len(obj.Consistency) != 1 || !maps.Equal(obj.Consistency[0], want) {
		t.Errorf("consistency = %v, want [%v]", obj.Consistency, want)
	}
}

func TestEvalRunE2E_ConsistencyCountsLostTrials(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "lost-trial",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms", "--threshold", "0.5")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(out, "  my-judge: pass^3 0/3 examples, 2 inconsistent, 1 failed every trial\n") {
		t.Errorf("summary does not count the lost trials:\n%s", out)
	}
	if n := strings.Count(out, "Consistency across trials"); n != 1 {
		t.Errorf("the consistency block printed %d times:\n%s", n, out)
	}
}

func TestEvalRunE2E_NoConsistencyForOneTrial(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "run", "exp1", "--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if strings.Contains(out, "Consistency across trials") {
		t.Errorf("a one-trial run printed a consistency block:\n%s", out)
	}
}

func TestEvalRunE2E_WatchFalse(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "run", "exp1",
		"--url", srv.URL, "--insecure", "--watch=false")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Watch disabled") || !strings.Contains(out, "run-exp1-id") {
		t.Errorf("watch=false should report the run id:\n%s", out)
	}

	out, _, err = runEvalCmd(t, "eval", "run", "exp1",
		"--url", srv.URL, "--insecure", "--watch=false", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var arr []map[string]any
	if jerr := json.Unmarshal([]byte(out), &arr); jerr != nil || len(arr) != 1 {
		t.Fatalf("watch=false --format json should emit a JSON array of 1: err=%v\n%s", jerr, out)
	}
}

func TestEvalRunE2E_RefusesTokenOverPlainHTTP(t *testing.T) {
	srv := newEvalStubServer(t)
	t.Setenv("SAM_AUTH_TOKEN", "x")
	_, _, err := runEvalCmd(t, "eval", "run", "exp1", "--url", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Fatalf("expected refusal to send token over http without --insecure, got: %v", err)
	}
}

func TestEvalListE2E(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "list", "--url", srv.URL, "--insecure")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{"NAME", "exp1", "exp2", "failing", "orchestrator"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestEvalListE2E_JSON(t *testing.T) {
	srv := newEvalStubServer(t)
	out, _, err := runEvalCmd(t, "eval", "list", "--url", srv.URL, "--insecure", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var items []map[string]any
	if jerr := json.Unmarshal([]byte(out), &items); jerr != nil {
		t.Fatalf("--format json is not a JSON array: %v\n%s", jerr, out)
	}
	if len(items) == 0 {
		t.Fatalf("expected experiments in JSON output:\n%s", out)
	}
	names := map[string]bool{}
	for _, it := range items {
		if n, ok := it["name"].(string); ok {
			names[n] = true
		}
		if _, ok := it["targetAgent"]; !ok {
			t.Errorf("item missing targetAgent field: %v", it)
		}
	}
	if !names["exp1"] {
		t.Errorf("JSON output missing experiment exp1: %v", names)
	}
}

func TestEvalRunE2E_CancelledRunFails(t *testing.T) {
	srv := newEvalStubServer(t)
	out, errOut, err := runEvalCmd(t, "eval", "run", "cancel",
		"--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil || !strings.Contains(err.Error(), "run run-cancel-id was cancelled") {
		t.Fatalf("err = %v, want the cancelled run reported\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "status=cancelled") {
		t.Errorf("stderr should show the run reaching cancelled:\n%s", errOut)
	}
}

func TestEvalRunE2E_V2TriggerConflictIsReported(t *testing.T) {
	srv := newEvalStubServer(t)
	_, _, err := runEvalCmd(t, "eval", "run", "busy", "--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil || !strings.Contains(err.Error(), `trigger experiment "busy"`) ||
		!strings.Contains(err.Error(), "status 409") || !strings.Contains(err.Error(), "already pending or running") {
		t.Fatalf("err = %v, want the 409 and its reason", err)
	}
}

func TestEvalRunE2E_V2RunDeletedWhileWaiting(t *testing.T) {
	srv := newEvalStubServer(t)
	_, _, err := runEvalCmd(t, "eval", "run", "gone", "--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil || !strings.Contains(err.Error(), "run run-gone-id was deleted while waiting for it") {
		t.Fatalf("err = %v, want the deleted run reported", err)
	}
}

func TestEvalRunE2E_V2ReadsEveryModelRunFromTheRunGroup(t *testing.T) {
	srv := newEvalStubServer(t)
	c, err := newPlatformClient(t.Context(), platformTargetFlags{url: srv.URL, insecure: true})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	trig, err := triggerExperiment(t.Context(), c, "multi")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if trig.Operation.ID != "op-multi-id" {
		t.Fatalf("operation = %+v, want the trigger's operation", trig.Operation)
	}
	var ids []string
	for _, r := range trig.Runs {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "run-m1,run-m2" {
		t.Fatalf("runs = %v, want both model runs from the run group", ids)
	}
}

func TestEvalRunE2E_InterruptCancelsTheRunsItStarted(t *testing.T) {
	if !canInterruptSelf {
		t.Skip("needs the test process to send itself SIGINT, which this OS cannot")
	}
	srv, stub := newEvalStub(t)
	_, errOut, err := runEvalCmd(t, "eval", "run", "interrupt", "--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if err == nil {
		t.Fatal("an interrupted run must not succeed")
	}
	if !strings.Contains(errOut, "received interrupt, canceling run(s)") {
		t.Errorf("stderr = %s, want the interrupt reported", errOut)
	}
	if got, want := strings.Join(stub.cancelledRuns(), ","), "run-i1,run-i2"; got != want {
		t.Errorf("cancelled = %s, want %s", got, want)
	}
}

func TestEvalRunE2E_AllowWarnings(t *testing.T) {
	srv := newEvalStubServer(t)
	run := func(args ...string) error {
		_, _, err := runEvalCmd(t, append([]string{"eval", "run", "warned", "--url", srv.URL, "--insecure", "--poll-every", "10ms"}, args...)...)
		return err
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "completed with warnings") {
		t.Errorf("without --allow-warnings: err = %v, want the warnings named", err)
	}
	if err := run("--allow-warnings"); err != nil {
		t.Errorf("with --allow-warnings and every trial scored and passing: err = %v, want none", err)
	}
	if err := run("--allow-warnings", "--watch=false"); err == nil || !strings.Contains(err.Error(), "--allow-warnings needs --watch") {
		t.Errorf("with --watch=false: err = %v, want the combination refused", err)
	}

	jsonRun := func(name string, args ...string) (map[string]any, string) {
		t.Helper()
		out, errOut, _ := runEvalCmd(t, append([]string{"eval", "run", name, "--url", srv.URL, "--insecure", "--poll-every", "10ms", "--format", "json"}, args...)...)
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("%s %v: decode %v\n%s", name, args, jerr, out)
		}
		return obj, errOut
	}
	countKeys := []string{"ungradedTrials", "ungradedByFailureKind", "erroredTrials"}

	warnedObj, _ := jsonRun("warned", "--allow-warnings")
	if warnedObj["ungradedTrials"] != float64(0) || warnedObj["erroredTrials"] != float64(0) || fmt.Sprint(warnedObj["ungradedByFailureKind"]) != "map[]" {
		t.Errorf("warned run with the flag: json summary = %v, want the counts present and empty", warnedObj)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"warned", nil},
		{"exp1", []string{"--allow-warnings"}},
	} {
		obj, errOut := jsonRun(tc.name, tc.args...)
		for _, key := range countKeys {
			if _, ok := obj[key]; ok {
				t.Errorf("%s %v: json summary carries %s, want it only once the flag counts a warned run: %v", tc.name, tc.args, key, obj)
			}
		}
		if strings.Contains(errOut, "count as failures") {
			t.Errorf("%s %v: stderr carries the broken-trials note:\n%s", tc.name, tc.args, errOut)
		}
	}
}

func TestEvalRunE2E_AllowWarningsCountsTrialsWithNoScore(t *testing.T) {
	srv := newEvalStubServer(t)
	run := func(args ...string) (string, string, error) {
		return runEvalCmd(t, append([]string{"eval", "run", "mostly-ended", "--url", srv.URL, "--insecure", "--poll-every", "10ms", "--allow-warnings"}, args...)...)
	}

	_, errOut, err := run()
	if err == nil || !strings.Contains(err.Error(), "(2/7 passed; 4 trial(s) with no score and 1 errored trial(s) count as failures)") {
		t.Errorf("default threshold: err = %v, want 2 of 7 passing with 4 ungraded and 1 errored", err)
	}
	if !strings.Contains(errOut, "of 7 trial(s), 4 produced no score (no_result 1, node_failed 3) and 1 errored; they count as failures, so --threshold is checked against 2/7 passed") {
		t.Errorf("stderr lacks the broken-trials note:\n%s", errOut)
	}

	_, plainErrOut, _ := runEvalCmd(t, "eval", "run", "mostly-ended", "--url", srv.URL, "--insecure", "--poll-every", "10ms")
	if strings.Contains(plainErrOut, "produced no score") {
		t.Errorf("without --allow-warnings the run fails on its warnings, yet stderr says trials count against --threshold:\n%s", plainErrOut)
	}

	if _, _, lowErr := run("--threshold", "0.25"); lowErr != nil {
		t.Errorf("at --threshold 0.25: err = %v, want none", lowErr)
	}

	out, _, _ := run("--format", "json")
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("--format json output is not valid JSON: %v\n%s", jerr, out)
	}
	if obj["passed"] != float64(2) || obj["total"] != float64(7) || obj["ungradedTrials"] != float64(4) || obj["erroredTrials"] != float64(1) ||
		fmt.Sprint(obj["ungradedByFailureKind"]) != "map[no_result:1 node_failed:3]" {
		t.Errorf("json summary = %v, want 2 of 7 passing, 4 ungraded and 1 errored", obj)
	}
}

func TestEvalRunE2E_MaxCostGatesOnTheKnownCosts(t *testing.T) {
	srv := newEvalStubServer(t)
	run := func(args ...string) (string, string, error) {
		return runEvalCmd(t, append([]string{"eval", "run", "order-flow", "--url", srv.URL, "--insecure", "--poll-every", "10ms"}, args...)...)
	}

	_, errOut, err := run("--max-cost", "0.005")
	if err == nil || !strings.Contains(err.Error(), "cost at least 0.006000, over --max-cost 0.005") {
		t.Errorf("over the ceiling: err = %v, want the cost breach", err)
	}
	if !strings.Contains(errOut, "cost of 1 of 2 trial(s) is unknown") {
		t.Errorf("over the ceiling: stderr lacks the unknown-cost note:\n%s", errOut)
	}

	if _, _, underErr := run("--max-cost", "0.01"); underErr != nil {
		t.Errorf("under the ceiling: err = %v, want none", underErr)
	}

	out, _, err := run("--max-cost", "0.005", "--format", "json")
	if err == nil {
		t.Error("over the ceiling in JSON mode: want an error")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("--format json output is not valid JSON: %v\n%s", jerr, out)
	}
	if obj["knownCost"] != "0.0060000000" || obj["unknownCostTrials"] != float64(1) || !strings.Contains(fmt.Sprint(obj["error"]), "over --max-cost") {
		t.Errorf("json summary = %v, want the known cost, one unknown trial and the breach", obj)
	}
}

func TestEvalRunE2E_MaxCostIsRefusedBeforeAnyRun(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"a malformed amount", []string{"order-flow", "--max-cost", "2,50"}, "not a non-negative decimal"},
		{"without watching", []string{"order-flow", "--max-cost", "1", "--watch=false"}, "--max-cost needs --watch"},
		{"an agent experiment", []string{"exp1", "--max-cost", "1"}, "applies only to workflow experiments"},
		{"an agent experiment named after a workflow one", []string{"order-flow", "exp1", "--max-cost", "1"}, "applies only to workflow experiments"},
		{"an unknown experiment", []string{"order-flow", "no-such", "--max-cost", "1"}, "no-such"},
		{"a workflow experiment with two models", []string{"order-flow-models", "--max-cost", "1"}, "runs 2 models"},
		{"an empty amount", []string{"order-flow", "--max-cost", ""}, "not a non-negative decimal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, stub := newEvalStub(t)

			_, _, err := runEvalCmd(t, append(append([]string{"eval", "run"}, tc.args...), "--url", srv.URL, "--insecure", "--poll-every", "10ms")...)

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if stub.triggers != 0 {
				t.Errorf("triggered %d run(s) before refusing the flag", stub.triggers)
			}
		})
	}
}

func TestEvalListE2E_ShowsEachExperimentsTarget(t *testing.T) {
	srv := newEvalStubServer(t)

	out, _, err := runEvalCmd(t, "eval", "list", "--url", srv.URL, "--insecure")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	rows := map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			rows[fields[0]] = fields
		}
	}
	if got := rows["NAME"]; !slices.Equal(got, []string{"NAME", "DATASET", "TARGET", "TYPE", "LATEST", "STATUS"}) {
		t.Errorf("header = %v", got)
	}
	if got := rows["order-flow"]; !slices.Equal(got, []string{"order-flow", "ds5", "order_flow", "workflow", "-"}) {
		t.Errorf("workflow row = %v", got)
	}
	if got := rows["exp1"]; !slices.Equal(got, []string{"exp1", "ds1", "orchestrator", "agent", "completed"}) {
		t.Errorf("agent row = %v", got)
	}

	out, _, err = runEvalCmd(t, "eval", "list", "--url", srv.URL, "--insecure", "--format", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	var items []map[string]any
	if jerr := json.Unmarshal([]byte(out), &items); jerr != nil {
		t.Fatalf("--format json is not a JSON array: %v\n%s", jerr, out)
	}
	byName := map[string]map[string]any{}
	for _, it := range items {
		byName[fmt.Sprint(it["name"])] = it
	}
	if wf := byName["order-flow"]; wf["targetType"] != "workflow" || wf["targetWorkflow"] != "order_flow" {
		t.Errorf("workflow item = %v", wf)
	}
	if agent := byName["exp1"]; agent["targetType"] != "agent" || agent["targetWorkflow"] != nil {
		t.Errorf("agent item = %v", agent)
	}
}
