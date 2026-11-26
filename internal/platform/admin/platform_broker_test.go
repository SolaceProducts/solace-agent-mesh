// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type testPlatformBrokerEnv struct {
	DB        *store.DB
	SkillRepo *store.SkillRepo
	ToolRepo  *store.ToolRepo
	SkillSvc  *SkillService
	ToolSvc   *ToolService
	Broker    broker.Broker
	Tracker   *platpkg.ResourceTracker
	Namespace string
}

func setupTestPlatformWithBroker(t *testing.T) *testPlatformBrokerEnv {
	t.Helper()

	logger := slog.Default()
	namespace := "test-ns"

	db := testOpenDB(t)

	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)

	skillSvc := NewSkillService(db, skillRepo, logger)
	toolSvc := NewToolService(toolRepo, logger)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	handler := NewDBResourceEventHandler(skillSvc, toolSvc, nil, logger)
	tracker := platpkg.NewResourceTracker(brk, namespace, handler, logger)

	ctx := context.Background()
	if err := tracker.Start(ctx); err != nil {
		t.Fatalf("start tracker: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	t.Cleanup(func() {
		_ = tracker.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})

	return &testPlatformBrokerEnv{
		DB:        db,
		SkillRepo: skillRepo,
		ToolRepo:  toolRepo,
		SkillSvc:  skillSvc,
		ToolSvc:   toolSvc,
		Broker:    brk,
		Tracker:   tracker,
		Namespace: namespace,
	}
}

func publishToolInit(t *testing.T, brk broker.Broker, namespace string, params remotetool.InitParams) {
	t.Helper()
	publishJSONRPC(t, brk, remotetool.InitTopic(namespace, params.ToolName), remotetool.MethodInit, params)
}

func publishToolRemoved(t *testing.T, brk broker.Broker, namespace, toolName string) {
	t.Helper()
	params := remotetool.RemovedParams{ToolName: toolName}
	publishJSONRPC(t, brk, remotetool.RemovedTopic(namespace, toolName), remotetool.MethodRemoved, params)
}

func publishSkillInit(t *testing.T, brk broker.Broker, namespace string, params remotetool.SkillInitParams) {
	t.Helper()
	publishJSONRPC(t, brk, remotetool.SkillInitTopic(namespace, params.SkillName), remotetool.MethodSkillInit, params)
}

func publishSkillRemoved(t *testing.T, brk broker.Broker, namespace, skillName string) {
	t.Helper()
	params := remotetool.SkillRemovedParams{SkillName: skillName}
	publishJSONRPC(t, brk, remotetool.SkillRemovedTopic(namespace, skillName), remotetool.MethodSkillRemoved, params)
}

func publishToolFailed(t *testing.T, brk broker.Broker, namespace, toolName, errMsg string) {
	t.Helper()
	params := remotetool.FailedDiscoveryParams{ToolName: toolName, ErrorMessage: errMsg, ErrorCode: "SCHEMA_FAILED"}
	publishJSONRPC(t, brk, remotetool.FailedDiscoveryTopic(namespace, toolName), remotetool.MethodFailedDiscovery, params)
}

func publishJSONRPC(t *testing.T, brk broker.Broker, topic, method string, params any) {
	t.Helper()
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal JSON-RPC: %v", err)
	}
	msg := &broker.Message{Payload: payload}
	if err := brk.Publish(context.Background(), topic, msg); err != nil {
		t.Fatalf("publish to %s: %v", topic, err)
	}
}

func testLogger() *slog.Logger {
	return slog.Default()
}

func waitForDB(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("waitForDB timed out")
}
