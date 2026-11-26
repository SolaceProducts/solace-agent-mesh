// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const resultTaskID = "task-result"

func workflowResultFrames(streamedText string) []string {
	var frames []string
	if streamedText != "" {
		status, _ := json.Marshal(map[string]any{"result": map[string]any{
			"kind": "status-update", "taskId": resultTaskID,
			"status": map[string]any{"message": map[string]any{
				"parts": []any{map[string]any{"kind": "text", "text": streamedText}},
			}},
		}})
		frames = append(frames, fmt.Sprintf("event: status_update\ndata: %s\n\n", status))
	}
	final, _ := json.Marshal(map[string]any{"result": map[string]any{
		"kind": "task", "id": resultTaskID,
		"status": map[string]any{"state": "completed", "message": map[string]any{
			"parts": []any{
				map[string]any{"kind": "data", "data": map[string]any{"greetings": []any{"Hello, Alpha", "Hello, Beta"}}},
				map[string]any{"kind": "text", "text": `Workflow "batch" completed successfully.`},
			},
		}},
	}})
	return append(frames, fmt.Sprintf("event: final_response\ndata: %s\n\n", final))
}

func newWorkflowResultGateway(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == agentCardsPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"name": fakeAgentName}}})
		case r.URL.Path == messageStreamPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": resultTaskID}})
		case strings.HasPrefix(r.URL.Path, sseSubscribePrefix):
			w.Header().Set("Content-Type", "text/event-stream")
			for _, f := range frames {
				_, _ = fmt.Fprint(w, f)
			}
		case r.URL.Path == hilRespondPath:
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readResponseJSON(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "response.json"))
	if err != nil {
		t.Fatalf("response.json was not written: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response.json is not a JSON array of objects: %v\n%s", err, raw)
	}
	return got
}

func assertGreetingsResult(t *testing.T, got []map[string]any) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("response.json holds %d data parts, want 1: %v", len(got), got)
	}
	greetings, _ := got[0]["greetings"].([]any)
	if len(greetings) != 2 || greetings[0] != "Hello, Alpha" || greetings[1] != "Hello, Beta" {
		t.Errorf("response.json = %v; want the workflow's greetings result", got)
	}
}

func TestSendTask_WritesStructuredResultOfFinalTask(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	tests := []struct {
		name         string
		streamedText string
	}{
		{name: "result with no streamed text", streamedText: ""},
		{name: "result alongside streamed progress text", streamedText: "greeting Alpha"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newWorkflowResultGateway(t, workflowResultFrames(tc.streamedText))
			outDir := t.TempDir()

			var out, errOut bytes.Buffer
			err := sendTask("run the workflow", sendTaskOpts{
				URL: srv.URL, Agent: fakeAgentName, Insecure: true, Timeout: 5 * time.Second,
				NoSTIM: true, OutputDir: outDir, Out: &out, ErrOut: &errOut,
			})
			if err != nil {
				t.Fatalf("sendTask: %v\nstderr:\n%s", err, errOut.String())
			}

			assertGreetingsResult(t, readResponseJSON(t, outDir))

			text, readErr := os.ReadFile(filepath.Join(outDir, "response.txt"))
			if tc.streamedText == "" {
				if readErr == nil {
					t.Errorf("response.txt = %q; want none when nothing streamed", text)
				}
				return
			}
			if string(text) != tc.streamedText {
				t.Errorf("response.txt = %q; want only the streamed text %q", text, tc.streamedText)
			}
		})
	}
}

func TestSendTask_NoStructuredResultWritesNoResponseJSON(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	final, _ := json.Marshal(map[string]any{"result": map[string]any{
		"kind": "task", "id": resultTaskID,
		"status": map[string]any{"state": "completed", "message": map[string]any{
			"parts": []any{map[string]any{"kind": "text", "text": "plain reply"}},
		}},
	}})
	srv := newWorkflowResultGateway(t, []string{fmt.Sprintf("event: final_response\ndata: %s\n\n", final)})
	outDir := t.TempDir()

	err := sendTask("hello", sendTaskOpts{
		URL: srv.URL, Agent: fakeAgentName, Insecure: true, Timeout: 5 * time.Second,
		NoSTIM: true, OutputDir: outDir, Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("sendTask: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "response.json")); !os.IsNotExist(statErr) {
		t.Errorf("response.json exists for a text-only reply (stat err = %v)", statErr)
	}
}

func artifactNoticePart(filename string) map[string]any {
	return map[string]any{
		"kind":     "data",
		"data":     map[string]any{"filename": filename, "version": 0, "status": "success"},
		"metadata": map[string]any{"tool_name": "_notify_artifact_save"},
	}
}

func completedFrame(t *testing.T, parts ...any) string {
	t.Helper()
	final, err := json.Marshal(map[string]any{"result": map[string]any{
		"kind": "task", "id": resultTaskID,
		"status": map[string]any{"state": "completed", "message": map[string]any{"parts": parts}},
	}})
	if err != nil {
		t.Fatalf("marshal final task: %v", err)
	}
	return fmt.Sprintf("event: final_response\ndata: %s\n\n", final)
}

func runSendTask(t *testing.T, frames []string, outDir string) {
	t.Helper()
	srv := newWorkflowResultGateway(t, frames)
	err := sendTask("hello", sendTaskOpts{
		URL: srv.URL, Agent: fakeAgentName, Insecure: true, Timeout: 5 * time.Second,
		NoSTIM: true, OutputDir: outDir, Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("sendTask: %v", err)
	}
}

func TestSendTask_ArtifactNoticeIsNotAStructuredResult(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	outDir := t.TempDir()
	runSendTask(t, []string{completedFrame(t,
		artifactNoticePart("report.md"),
		map[string]any{"kind": "text", "text": "Saved the report."},
	)}, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "response.json")); !os.IsNotExist(err) {
		t.Errorf("response.json exists for an agent that only saved an artifact (stat err = %v)", err)
	}
}

func TestSendTask_ArtifactNoticeBesideResultKeepsOnlyTheResult(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	outDir := t.TempDir()
	runSendTask(t, []string{completedFrame(t,
		artifactNoticePart("report.md"),
		map[string]any{"kind": "data", "data": map[string]any{"greetings": []any{"Hello, Alpha", "Hello, Beta"}}},
		map[string]any{"kind": "text", "text": "done"},
	)}, outDir)

	assertGreetingsResult(t, readResponseJSON(t, outDir))
}

func TestSendTask_RemovesStaleResponseJSONWhenNoResult(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	outDir := t.TempDir()
	stale := filepath.Join(outDir, "response.json")
	if err := os.WriteFile(stale, []byte(`[{"old":true}]`), 0o644); err != nil {
		t.Fatalf("seed stale response.json: %v", err)
	}
	runSendTask(t, []string{completedFrame(t, map[string]any{"kind": "text", "text": "plain"})}, outDir)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale response.json from an earlier run is still there (stat err = %v)", err)
	}
}

func TestHandleHILRespond_WritesStructuredResultOfFinalTask(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	srv := newWorkflowResultGateway(t, workflowResultFrames(""))
	outDir := t.TempDir()
	hilReq := filepath.Join(t.TempDir(), "hil_request.json")
	body := `{"request_id":"req-1","task_id":"` + resultTaskID + `","agent_name":"` + fakeAgentName + `"}`
	if err := os.WriteFile(hilReq, []byte(body), 0o644); err != nil {
		t.Fatalf("write HIL request: %v", err)
	}

	var errOut bytes.Buffer
	err := handleHILRespond(sendTaskOpts{
		URL: srv.URL, Insecure: true, Timeout: 5 * time.Second, NoSTIM: true,
		OutputDir: outDir, HILApproveFile: hilReq, Out: &bytes.Buffer{}, ErrOut: &errOut,
	})
	if err != nil {
		t.Fatalf("handleHILRespond: %v\nstderr:\n%s", err, errOut.String())
	}

	assertGreetingsResult(t, readResponseJSON(t, outDir))
}

func TestSendTask_FailedTaskWritesNoResponseJSON(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	final, _ := json.Marshal(map[string]any{"result": map[string]any{
		"kind": "task", "id": resultTaskID,
		"status": map[string]any{"state": "failed", "message": map[string]any{
			"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
				"type": "structured_invocation_result", "status": "error", "error_message": "node blew up",
			}}},
		}},
	}})
	srv := newWorkflowResultGateway(t, []string{fmt.Sprintf("event: final_response\ndata: %s\n\n", final)})
	outDir := t.TempDir()

	err := sendTask("hello", sendTaskOpts{
		URL: srv.URL, Agent: fakeAgentName, Insecure: true, Timeout: 5 * time.Second,
		NoSTIM: true, OutputDir: outDir, Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{},
	})
	if !IsAlreadyReported(err) {
		t.Fatalf("sendTask error = %v; want the task-failed error", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "response.json")); !os.IsNotExist(statErr) {
		t.Errorf("response.json exists for a failed task (stat err = %v)", statErr)
	}
}
