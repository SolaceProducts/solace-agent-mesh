// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func traceOnX() []string {
	return []string{rbac.AgentReadFeedbackScope(uuidA), rbac.AgentReadTraceScope(uuidA)}
}

func threeTurnsOnX(t *testing.T, exe *GatewayExecutor) seededSession {
	t.Helper()
	return seedFeedbackSession(t, exe, []sessionTask{
		{subject: uuidA, parent: -1, start: 1000},
		{subject: uuidA, parent: -1, start: 2000},
		{subject: uuidA, parent: -1, start: 3000},
	}, 2, uuidA)
}

func seedRawEvent(t *testing.T, exe *GatewayExecutor, taskID, payload string) {
	t.Helper()
	if err := store.NewTaskEventRepo(exe.Gateways()[0].Store()).Create(t.Context(), &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, CreatedTime: time.Now().UnixMilli(), Topic: "t", Direction: "in", Payload: payload,
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func TestFeedbackSession_ForwardedPeerOnEarlierTurnRefuses(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := threeTurnsOnX(t, exe)
	seedForwardedPeerEvent(t, exe.Gateways()[0].Store(), s.taskIDs[0],
		a2a.InstanceName(a2a.KindAgent, uuidA), a2a.InstanceName(a2a.KindAgent, uuidB))

	refused, err := callFeedbackSession(t, exe, s.feedbackID, identityWith("reader", traceOnX()...))
	assertFeedbackSessionRefused(t, refused, err, s.feedbackID)

	got, err := callFeedbackSession(t, exe, s.feedbackID,
		identityWith("reader", append(traceOnX(), rbac.AgentReadTraceScope(uuidB))...))
	if err != nil || got.Refusal != "" || len(got.Turns) != 3 {
		t.Fatalf("covering the peer too: refusal=%q err=%v, want all 3 turns", got.Refusal, err)
	}
}

func TestFeedbackSession_UnnamedForwardedPeerRefuses(t *testing.T) {
	for name, payload := range map[string]string{
		"truncated json": `{"result":{"status":{"message":{"metadata":{"forwarded_from_peer":"agent_`,
		"empty name":     `{"result":{"status":{"message":{"metadata":{"forwarded_from_peer":""}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			exe := newStoreBackedActivityExec(t)
			s := threeTurnsOnX(t, exe)
			seedRawEvent(t, exe, s.taskIDs[1], payload)
			refused, err := callFeedbackSession(t, exe, s.feedbackID, identityWith("reader", traceOnX()...))
			assertFeedbackSessionRefused(t, refused, err, s.feedbackID)
		})
	}
}

func TestFeedbackSession_TreeAtDepthBoundRefuses(t *testing.T) {
	specs := []sessionTask{{subject: uuidA, parent: -1, start: 1000}}
	for d := 1; d <= store.TaskTreeMaxDepth; d++ {
		specs = append(specs, sessionTask{subject: uuidA, parent: d - 1, start: 1000 + int64(d)})
	}
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, specs, 0, uuidA)
	refused, err := callFeedbackSession(t, exe, s.feedbackID, identityWith("reader", traceOnX()...))
	assertFeedbackSessionRefused(t, refused, err, s.feedbackID)

	shallow := newStoreBackedActivityExec(t)
	ok := seedFeedbackSession(t, shallow, specs[:store.TaskTreeMaxDepth], 0, uuidA)
	got, err := callFeedbackSession(t, shallow, ok.feedbackID, identityWith("reader", traceOnX()...))
	if err != nil || got.Refusal != "" {
		t.Fatalf("one level short of the bound: refusal=%q err=%v, want it served", got.Refusal, err)
	}
}

func TestFeedbackSession_RatedTaskBeyondWalkBoundRefuses(t *testing.T) {
	specs := []sessionTask{{subject: uuidA, parent: -1, start: 1000}}
	for d := 1; d <= TraceParentChainMaxDepth+1; d++ {
		specs = append(specs, sessionTask{subject: uuidA, parent: d - 1, start: 1000 + int64(d)})
	}
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, specs, len(specs)-1, uuidA)
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	for name, caller := range map[string]*auth.Identity{
		"owner": identityWith("u1"),
		"admin": {UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
		"trace": identityWith("reader", traceOnX()...),
	} {
		refused, err := callFeedbackSession(t, exe, s.feedbackID, caller)
		if err != nil || refused.Refusal != analytics.SessionRefusalTooDeep || refused.RetentionGap {
			t.Errorf("%s: refusal=%q retentionGap=%v err=%v, want %q", name, refused.Refusal, refused.RetentionGap, err, analytics.SessionRefusalTooDeep)
		}
	}
	if strings.Contains(logs.String(), "activity access denied") {
		t.Errorf("depth limit logged as an access denial:\n%s", logs.String())
	}
}

func TestFeedbackSession_AuditLabelNamesTheRoute(t *testing.T) {
	exe := newStoreBackedActivityExec(t)
	s := seedFeedbackSession(t, exe, singleTurnOnX(), 0, uuidA)
	var logs bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logs, nil))

	if _, err := callFeedbackSessionByTask(t, exe, s.taskIDs[0], identityWith("u1")); err != nil {
		t.Fatalf("by task: %v", err)
	}
	var reports []string
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "activity read" {
			reports = append(reports, fmt.Sprint(m["report"]))
		}
	}
	if !slices.Equal(reports, []string{feedbackTaskSessionReport}) {
		t.Errorf("audit reports = %v, want [%s]", reports, feedbackTaskSessionReport)
	}
}
