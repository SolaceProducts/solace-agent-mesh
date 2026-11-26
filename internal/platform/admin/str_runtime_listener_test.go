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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	strRuntimeTestNamespace = "default"
	strRuntimeTestWait      = 10 * time.Second
)

func startSTRRuntimeListener(t *testing.T, brk broker.Broker) *STRRuntimeRegistry {
	t.Helper()
	registry := NewSTRRuntimeRegistry()
	listener := NewSTRRuntimeListener(registry, brk, strRuntimeTestNamespace, slog.Default())
	if err := listener.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = listener.Stop(context.WithoutCancel(t.Context())) })
	return registry
}

func publishSTRNotification(t *testing.T, brk broker.Broker, topic, method string, params any) {
	t.Helper()
	payload, err := json.Marshal(a2a.NewRequest("", method, params))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func awaitBuildTarget(t *testing.T, registry *STRRuntimeRegistry, want ToolsetBuildTargetResponse) {
	t.Helper()
	deadline := time.Now().Add(strRuntimeTestWait)
	for {
		got, err := registry.BuildTarget()
		if err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("build target = %+v, %v; want %+v", got, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSTRRuntimeListener_EveryGWELearnsTheTargetFromAToolInit(t *testing.T) {
	brk := connectedDevBroker(t)
	gweA := startSTRRuntimeListener(t, brk)
	gweB := startSTRRuntimeListener(t, brk)

	publishSTRNotification(t, brk, remotetool.InitTopic(strRuntimeTestNamespace, "demo__tool"), remotetool.MethodInit,
		remotetool.InitParams{ToolName: "demo__tool", RuntimeOS: "linux", RuntimeArch: "amd64", RuntimePythonVersion: "3.11"})

	want := ToolsetBuildTargetResponse{OS: "linux", Arch: "amd64", Source: BuildTargetSourceSTRInit, PythonVersion: "3.11"}
	awaitBuildTarget(t, gweA, want)
	awaitBuildTarget(t, gweB, want)
}

func TestSTRRuntimeListener_LearnsTheTargetFromAFailedDiscovery(t *testing.T) {
	brk := connectedDevBroker(t)
	registry := startSTRRuntimeListener(t, brk)

	publishSTRNotification(t, brk, remotetool.FailedDiscoveryTopic(strRuntimeTestNamespace, "demo__broken"), remotetool.MethodFailedDiscovery,
		remotetool.FailedDiscoveryParams{ToolName: "demo__broken", RuntimeOS: "linux", RuntimeArch: "arm64"})

	awaitBuildTarget(t, registry, ToolsetBuildTargetResponse{OS: "linux", Arch: "arm64", Source: BuildTargetSourceSTRInit})
}
