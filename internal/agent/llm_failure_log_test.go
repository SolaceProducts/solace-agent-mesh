// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func failureLogRecord(t *testing.T, err error) map[string]any {
	t.Helper()

	var buf syncBuffer
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	rc := &taskRunContext{
		sess:   &session.Session{},
		logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	var allText replyText

	if _, outcome, retErr := agent.handleStreamCallError(
		t.Context(), state, rc, nil, err, &allText,
	); outcome != loopReturn || retErr != nil {
		t.Fatalf("expected loopReturn / nil err, got outcome=%v err=%v", outcome, retErr)
	}

	for _, raw := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(raw), &rec) != nil {
			continue
		}
		if rec["msg"] == "LLM call failed" {
			return rec
		}
	}
	t.Fatalf("no 'LLM call failed' record logged\ncaptured:\n%s", buf.String())
	return nil
}

func TestLLMFailureLogCarriesRetryCost(t *testing.T) {
	rec := failureLogRecord(t, &llm.Error{
		Message:    "<html><head><title>504 Gateway Time-out</title></head></html>",
		StatusCode: 504,
		Provider:   "bedrock",
		Attempts:   3,
		Elapsed:    901 * time.Second,
	})

	for key, want := range map[string]any{
		"attempts":       float64(3),
		"elapsed_ms":     float64(901000),
		"upstreamStatus": float64(504),
		"provider":       "bedrock",
	} {
		if got := rec[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if line, _ := json.Marshal(rec); strings.Contains(strings.ToLower(string(line)), "<html") {
		t.Errorf("provider HTML body reached the logs: %s", line)
	}
}

func TestLLMFailureLogOmitsUncountedAttempts(t *testing.T) {
	rec := failureLogRecord(t, &llm.Error{
		Message:    "stream interrupted",
		StatusCode: 500,
		Provider:   "bedrock",
	})

	for _, key := range []string{"attempts", "elapsed_ms"} {
		if got, ok := rec[key]; ok {
			t.Errorf("%s = %v, want the key absent when the retry loop never counted it", key, got)
		}
	}
	if rec["upstreamStatus"] != float64(500) || rec["provider"] != "bedrock" {
		t.Errorf("upstreamStatus/provider dropped: %v / %v", rec["upstreamStatus"], rec["provider"])
	}
}
