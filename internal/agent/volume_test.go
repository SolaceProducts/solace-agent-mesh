// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type mockVolumeParamTool struct {
	name         string
	volumeParams []tool.VolumeParamInfo
}

func (m *mockVolumeParamTool) Name() string                                      { return m.name }
func (m *mockVolumeParamTool) Description() string                               { return "mock" }
func (m *mockVolumeParamTool) Parameters() *tool.JSONSchema                      { return nil }
func (m *mockVolumeParamTool) Instructions() string                              { return "" }
func (m *mockVolumeParamTool) IsLongRunning() bool                               { return false }
func (m *mockVolumeParamTool) RequiredScopes() []string                          { return nil }
func (m *mockVolumeParamTool) RawStringArgs() []string                           { return nil }
func (m *mockVolumeParamTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (m *mockVolumeParamTool) ContextParam() string                              { return "" }
func (m *mockVolumeParamTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return nil, nil
}
func (m *mockVolumeParamTool) VolumeParams() []tool.VolumeParamInfo {
	return m.volumeParams
}

var _ tool.BuiltinTool = (*mockVolumeParamTool)(nil)
var _ tool.VolumeParamProvider = (*mockVolumeParamTool)(nil)

type mockPlainTool struct {
	name string
}

func (m *mockPlainTool) Name() string                                      { return m.name }
func (m *mockPlainTool) Description() string                               { return "mock" }
func (m *mockPlainTool) Parameters() *tool.JSONSchema                      { return nil }
func (m *mockPlainTool) Instructions() string                              { return "" }
func (m *mockPlainTool) IsLongRunning() bool                               { return false }
func (m *mockPlainTool) RequiredScopes() []string                          { return nil }
func (m *mockPlainTool) RawStringArgs() []string                           { return nil }
func (m *mockPlainTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (m *mockPlainTool) ContextParam() string                              { return "" }
func (m *mockPlainTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return nil, nil
}

var _ tool.BuiltinTool = (*mockPlainTool)(nil)

type mockVolumeService struct {
	mu                    sync.Mutex
	createCalls           int
	lastOpts              volume.CreateOptions
	createID              string
	createErr             error
	volumes               []*volume.Volume
	listErr               error
	getVolume             *volume.Volume
	getErr                error
	deleteCalls           int
	listSessionVolumesErr error
	sessionVolumeIDs      []string
}

func (m *mockVolumeService) Create(_ context.Context, opts volume.CreateOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls++
	m.lastOpts = opts
	return m.createID, m.createErr
}

func (m *mockVolumeService) Get(_ context.Context, _ string) (*volume.Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getVolume, m.getErr
}

func (m *mockVolumeService) List(_ context.Context, _ string, _ volume.ListFilter) ([]*volume.Volume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volumes, m.listErr
}

func (m *mockVolumeService) Delete(_ context.Context, _ string, _ volume.CallerType) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	return nil
}

func (m *mockVolumeService) RemoveIfExpired(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockVolumeService) AcquireWriteLock(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (m *mockVolumeService) ReleaseWriteLock(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockVolumeService) RecordAccess(_ context.Context, _ string) error {
	return nil
}

func (m *mockVolumeService) Resolve(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockVolumeService) CleanupExpired(_ context.Context) (int, error) {
	return 0, nil
}

func (m *mockVolumeService) AssociateSession(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockVolumeService) ListSessionVolumes(_ context.Context, _ string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listSessionVolumesErr != nil {
		return nil, m.listSessionVolumesErr
	}
	return m.sessionVolumeIDs, nil
}

func (m *mockVolumeService) UserUsage(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVolumeService) RefreshSize(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVolumeService) RefreshAllSizes(_ context.Context) (int, error) {
	return 0, nil
}

func (m *mockVolumeService) PerUserQuotaBytes() int64 { return 0 }

var _ volume.Service = (*mockVolumeService)(nil)

func volumeTestAgent(cfg AgentConfig) *Agent {
	return New(cfg)
}

func volumeTestAgentWithVolumeSvc(cfg AgentConfig, volSvc volume.Service) *Agent {
	a := New(cfg)
	a.svc = &mockServices{volumes: volSvc}
	return a
}

func TestValidateVolumeConfigNoVolumes(t *testing.T) {
	a := volumeTestAgent(AgentConfig{Name: "test"})
	if err := a.ValidateVolumeConfig(); err != nil {
		t.Errorf("expected no error for agent without volumes, got %v", err)
	}
}

func TestValidateVolumeConfigValidStatic(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "data", Provision: "static", VolumeID: "vol-123"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"workspace": "data"},
		},
	})

	if err := a.ValidateVolumeConfig(); err != nil {
		t.Errorf("expected no error for valid config, got %v", err)
	}
}

func TestValidateVolumeConfigBindingReferencesUnknownSlot(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "data", Provision: "static", VolumeID: "vol-123"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"workspace": "nonexistent"},
		},
	})

	err := a.ValidateVolumeConfig()
	if err == nil {
		t.Fatal("expected error for binding referencing unknown slot")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention unknown slot name, got %q", err.Error())
	}
}

func TestValidateVolumeConfigBindingsWithoutSlots(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"workspace": "data"},
		},
	})

	err := a.ValidateVolumeConfig()
	if err == nil {
		t.Fatal("expected error for bindings without slots")
	}
}

func TestValidateVolumeConfigInvalidTTL(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "not-a-duration"},
		},
	})

	err := a.ValidateVolumeConfig()
	if err == nil {
		t.Fatal("expected error for invalid TTL")
	}
	if !strings.Contains(err.Error(), "invalid TTL") {
		t.Errorf("error should mention invalid TTL, got %q", err.Error())
	}
}

func TestValidateVolumeConfigValidTTL(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "24h"},
		},
	})

	if err := a.ValidateVolumeConfig(); err != nil {
		t.Errorf("expected no error for valid TTL, got %v", err)
	}
}

func TestResolveVolumeBindingsNoBindings(t *testing.T) {
	a := volumeTestAgent(AgentConfig{Name: "test"})

	bindings, err := a.resolveVolumeBindings("any-tool", nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if bindings != nil {
		t.Errorf("expected nil bindings, got %v", bindings)
	}
}

func TestResolveVolumeBindingsStatic(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "data", Provision: "static", VolumeID: "vol-abc"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"source_code": "data"},
		},
	})

	mockTool := &mockVolumeParamTool{
		name: "my-tool",
		volumeParams: []tool.VolumeParamInfo{
			{Name: "source_code", Mode: "readwrite", MountPath: "/workspace"},
		},
	}
	if err := a.tools.Register(mockTool); err != nil {
		t.Fatal(err)
	}

	bindings, err := a.resolveVolumeBindings("my-tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}

	b := bindings[0]
	if b.VolumeID != "vol-abc" {
		t.Errorf("VolumeID = %q, want %q", b.VolumeID, "vol-abc")
	}
	if b.MountPath != "/workspace" {
		t.Errorf("MountPath = %q, want %q", b.MountPath, "/workspace")
	}
	if b.ReadOnly {
		t.Error("ReadOnly should be false for readwrite mode")
	}
}

func TestResolveVolumeBindingsStaticReadonly(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "ref", Provision: "static", VolumeID: "vol-ro"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"reader": {"docs": "ref"},
		},
	})

	mockTool := &mockVolumeParamTool{
		name: "reader",
		volumeParams: []tool.VolumeParamInfo{
			{Name: "docs", Mode: "readonly", MountPath: "/data"},
		},
	}
	if err := a.tools.Register(mockTool); err != nil {
		t.Fatal(err)
	}

	bindings, err := a.resolveVolumeBindings("reader", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if !bindings[0].ReadOnly {
		t.Error("ReadOnly should be true for readonly mode")
	}
}

func TestResolveVolumeBindingsSlotNotFound(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name:        "test",
		VolumeSlots: []VolumeSlot{},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"workspace": "missing"},
		},
	})

	_, err := a.resolveVolumeBindings("my-tool", nil)
	if err == nil {
		t.Fatal("expected error for missing slot")
	}
}

func TestResolveVolumeBindingsToolWithoutVolumeParams(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "data", Provision: "static", VolumeID: "vol-xyz"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"plain-tool": {"workspace": "data"},
		},
	})

	mockTool := &mockPlainTool{name: "plain-tool"}
	if err := a.tools.Register(mockTool); err != nil {
		t.Fatal(err)
	}

	bindings, err := a.resolveVolumeBindings("plain-tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].VolumeID != "vol-xyz" {
		t.Errorf("VolumeID = %q, want %q", bindings[0].VolumeID, "vol-xyz")
	}
	if bindings[0].MountPath != "" {
		t.Errorf("MountPath should be empty, got %q", bindings[0].MountPath)
	}
}

func TestResolveAutoCreateSuccess(t *testing.T) {
	volSvc := &mockVolumeService{
		createID: "vol-new-123",
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "24h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}, volSvc)

	vrc := &volumeResolveCtx{
		ctx:    context.Background(),
		userID: "user-1",
	}

	bindings, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].VolumeID != "vol-new-123" {
		t.Errorf("VolumeID = %q, want %q", bindings[0].VolumeID, "vol-new-123")
	}

	volSvc.mu.Lock()
	if volSvc.createCalls != 1 {
		t.Errorf("expected 1 Create call, got %d", volSvc.createCalls)
	}
	if volSvc.lastOpts.CreatorType != volume.CallerAgent {
		t.Errorf("CreatorType = %q, want %q", volSvc.lastOpts.CreatorType, volume.CallerAgent)
	}
	if volSvc.lastOpts.TTL == nil || *volSvc.lastOpts.TTL != 24*time.Hour {
		t.Errorf("TTL should be 24h")
	}
	volSvc.mu.Unlock()
}

func TestResolveAutoCreateCacheHit(t *testing.T) {
	volSvc := &mockVolumeService{
		createID: "vol-new-123",
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}, volSvc)

	vrc := &volumeResolveCtx{
		ctx:    context.Background(),
		userID: "user-1",
	}

	if _, err := a.resolveVolumeBindings("my-tool", vrc); err != nil {
		t.Fatal(err)
	}

	bindings, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatal(err)
	}
	if bindings[0].VolumeID != "vol-new-123" {
		t.Errorf("cached VolumeID = %q, want %q", bindings[0].VolumeID, "vol-new-123")
	}

	volSvc.mu.Lock()
	if volSvc.createCalls != 1 {
		t.Errorf("Create should be called once (cached), got %d", volSvc.createCalls)
	}
	volSvc.mu.Unlock()
}

func TestResolveAutoCreateNoVolumeService(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	})

	vrc := &volumeResolveCtx{
		ctx:    context.Background(),
		userID: "user-1",
	}

	_, err := a.resolveVolumeBindings("my-tool", vrc)
	if err == nil {
		t.Fatal("expected error when volume service is nil")
	}
	if !strings.Contains(err.Error(), "volume service not available") {
		t.Errorf("error should mention volume service, got %q", err.Error())
	}
}

func TestResolveAutoCreateNilContext(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	})

	_, err := a.resolveVolumeBindings("my-tool", nil)
	if err == nil {
		t.Fatal("expected error with nil volumeResolveCtx")
	}
}

func deliverHILResponseAsync(a *Agent, resp *a2a.UserInputResponseData) {
	go func() {
		for range 400 {
			if id := a.pendingHILIDWithPrefix(resp.RequestID + "-"); id != "" {
				answer := *resp
				answer.RequestID = id
				if a.deliverHILResponse(&answer, hilSenderUser("user-1")) == hilDelivered {
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func (a *Agent) pendingHILIDWithPrefix(prefix string) string {
	a.pendingHILMu.Lock()
	defer a.pendingHILMu.Unlock()
	for id := range a.pendingHIL {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	return ""
}

func TestResolvePromptUserSelectExisting(t *testing.T) {
	volSvc := &mockVolumeService{
		volumes: []*volume.Volume{
			{ID: "vol-existing", Name: "My Volume", Description: "Test volume", UserID: "user-1"},
		},
		getVolume: &volume.Volume{ID: "vol-existing", Name: "My Volume", UserID: "user-1"},
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user", PromptMessage: "Select dataset"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	deliverHILResponseAsync(a, &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "volume-prompt-dataset",
		Status:    "answered",
		Data:      map[string]any{"selected_volume": "vol-existing"},
	})

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		sp:        &StatusPublisher{},
		userID:    "user-1",
		canPrompt: true,
	}

	bindings, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].VolumeID != "vol-existing" {
		t.Errorf("VolumeID = %q, want %q", bindings[0].VolumeID, "vol-existing")
	}
}

func TestResolvePromptUserNotifyRequestNamesTheChatSession(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	volSvc := &mockVolumeService{
		volumes:   []*volume.Volume{{ID: "vol-existing", Name: "My Volume", UserID: "user-1"}},
		getVolume: &volume.Volume{ID: "vol-existing", Name: "My Volume", UserID: "user-1"},
	}
	a := New(AgentConfig{
		Name:      "test-agent",
		Namespace: "test-ns",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user", PromptMessage: "Select dataset"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.volumes = volSvc
	svc.brk = brk
	svc.featureRegistry = notifyEnabledRegistry(true)
	a.svc = svc
	if err := a.Init(ctx, nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	q, err := brk.CreateQueue("t-volume-notify", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.NotifyRequestSubscription("test-ns")); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	deliverHILResponseAsync(a, &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "volume-prompt-dataset",
		Status:    "answered",
		Data:      map[string]any{"selected_volume": "vol-existing"},
	})

	sp := NewStatusPublisher(brk, StatusPublisherConfig{StatusTopic: "test-ns/a2a/v1/gateway/status/web-1/task-1"}, slog.Default())
	vrc := &volumeResolveCtx{
		ctx:       ctx,
		sp:        sp,
		userID:    "user-1",
		sessionID: "web-session-1",
		canPrompt: true,
	}
	if _, err = a.resolveVolumeBindings("my-tool", vrc); err != nil {
		t.Fatalf("resolveVolumeBindings: %v", err)
	}

	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("did not receive the volume prompt NotifyRequest: %v", err)
	}
	var req a2a.NotifyRequest
	if err = json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("unmarshal NotifyRequest: %v", err)
	}
	if got := req.Metadata["session_id"]; got != vrc.sessionID {
		t.Errorf("session_id = %v, want %q", got, vrc.sessionID)
	}
}

func TestResolvePromptUserManualEntry(t *testing.T) {
	volSvc := &mockVolumeService{
		volumes:   []*volume.Volume{},
		getVolume: &volume.Volume{ID: "vol-manual", Name: "Manual", UserID: "user-1"},
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	deliverHILResponseAsync(a, &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "volume-prompt-dataset",
		Status:    "answered",
		Data:      map[string]any{"selected_volume": "__other__", "manual_volume_id": "vol-manual"},
	})

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		sp:        &StatusPublisher{},
		userID:    "user-1",
		canPrompt: true,
	}

	bindings, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bindings[0].VolumeID != "vol-manual" {
		t.Errorf("VolumeID = %q, want %q", bindings[0].VolumeID, "vol-manual")
	}
}

func TestResolvePromptUserManualEntryRejectsCrossUserVolume(t *testing.T) {
	volSvc := &mockVolumeService{
		volumes:   []*volume.Volume{},
		getVolume: &volume.Volume{ID: "vol-someone-else", Name: "Private", UserID: "user-2"},
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	deliverHILResponseAsync(a, &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "volume-prompt-dataset",
		Status:    "answered",
		Data:      map[string]any{"selected_volume": "__other__", "manual_volume_id": "vol-someone-else"},
	})

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		sp:        &StatusPublisher{},
		userID:    "user-1",
		canPrompt: true,
	}

	_, err := a.resolveVolumeBindings("my-tool", vrc)
	if err == nil {
		t.Fatal("expected error binding another user's volume via manual_volume_id, got nil")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("error should be the non-disclosing 'not available' message, got: %v", err)
	}
}

func TestResolvePromptUserCancelled(t *testing.T) {
	volSvc := &mockVolumeService{
		volumes: []*volume.Volume{},
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	deliverHILResponseAsync(a, &a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "volume-prompt-dataset",
		Status:    "cancelled",
	})

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		sp:        &StatusPublisher{},
		userID:    "user-1",
		canPrompt: true,
	}

	_, err := a.resolveVolumeBindings("my-tool", vrc)
	if err == nil {
		t.Fatal("expected error for cancelled prompt")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error should mention cancelled, got %q", err.Error())
	}
}

func TestResolvePromptUserCannotPrompt(t *testing.T) {
	volSvc := &mockVolumeService{volumes: []*volume.Volume{}}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		sp:        &StatusPublisher{},
		userID:    "user-1",
		canPrompt: false,
	}

	_, err := a.resolveVolumeBindings("my-tool", vrc)
	if err == nil {
		t.Fatal("expected a clean error when the gateway can't prompt")
	}
	if !strings.Contains(err.Error(), "can't show an interactive volume picker") {
		t.Errorf("error = %q, want it to explain the channel can't prompt", err.Error())
	}
}

func TestResolvePromptUserCacheHit(t *testing.T) {
	volSvc := &mockVolumeService{
		volumes:   []*volume.Volume{},
		getVolume: &volume.Volume{ID: "vol-cached"},
	}
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	}, volSvc)

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		userID:    "user-1",
		sessionID: "sess-1",
	}
	a.resolvedVolumes[volumeCacheKey(vrc.sessionID, "dataset")] = "vol-cached"

	bindings, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bindings[0].VolumeID != "vol-cached" {
		t.Errorf("VolumeID = %q, want %q (from cache)", bindings[0].VolumeID, "vol-cached")
	}
}

func TestResolvePromptUserNoVolumeService(t *testing.T) {
	a := volumeTestAgent(AgentConfig{
		Name: "test",
		VolumeSlots: []VolumeSlot{
			{Name: "dataset", Provision: "prompt_user"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"input": "dataset"},
		},
	})

	vrc := &volumeResolveCtx{
		ctx:    context.Background(),
		userID: "user-1",
	}

	_, err := a.resolveVolumeBindings("my-tool", vrc)
	if err == nil {
		t.Fatal("expected error when volume service is nil")
	}
}

func TestGenerateVolumePromptSurfaceWithVolumes(t *testing.T) {
	volumes := []*volume.Volume{
		{ID: "vol-1", Name: "Project Data", Description: "Main project volume"},
		{ID: "vol-2", Name: "Scratch"},
	}

	raw, err := generateVolumePromptSurface("req-1", "Select a volume", volumes, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	var surface map[string]any
	if err := json.Unmarshal(raw, &surface); err != nil {
		t.Fatal(err)
	}

	components, _ := surface["components"].([]any)
	foundPicker := false
	for _, c := range components {
		comp, _ := c.(map[string]any)
		if comp["component"] == "ChoicePicker" {
			foundPicker = true
			opts, _ := comp["options"].([]any)
			if len(opts) != 3 {
				t.Errorf("expected 3 options, got %d", len(opts))
			}
		}
	}
	if !foundPicker {
		t.Error("expected ChoicePicker component when volumes exist")
	}
}

func TestGenerateVolumePromptSurfaceWithoutVolumes(t *testing.T) {
	raw, err := generateVolumePromptSurface("req-1", "Enter volume ID", nil, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	var surface map[string]any
	if err := json.Unmarshal(raw, &surface); err != nil {
		t.Fatal(err)
	}

	components, _ := surface["components"].([]any)
	foundPicker := false
	foundTextField := false
	for _, c := range components {
		comp, _ := c.(map[string]any)
		if comp["component"] == "ChoicePicker" {
			foundPicker = true
		}
		if comp["component"] == "TextField" {
			foundTextField = true
		}
	}
	if foundPicker {
		t.Error("ChoicePicker should not be present when no volumes exist")
	}
	if !foundTextField {
		t.Error("TextField should always be present")
	}
}

func TestGetToolVolumeParamsWithProvider(t *testing.T) {
	a := volumeTestAgent(AgentConfig{Name: "test"})

	mockTool := &mockVolumeParamTool{
		name: "vp-tool",
		volumeParams: []tool.VolumeParamInfo{
			{Name: "src", Mode: "readwrite", MountPath: "/src"},
			{Name: "out", Mode: "readonly", MountPath: "/out"},
		},
	}
	if err := a.tools.Register(mockTool); err != nil {
		t.Fatal(err)
	}

	params := a.getToolVolumeParams("vp-tool")
	if len(params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(params))
	}
	if params["src"].MountPath != "/src" {
		t.Errorf("src mount_path = %q", params["src"].MountPath)
	}
	if params["out"].Mode != "readonly" {
		t.Errorf("out mode = %q", params["out"].Mode)
	}
}

func TestGetToolVolumeParamsWithoutProvider(t *testing.T) {
	a := volumeTestAgent(AgentConfig{Name: "test"})

	mockTool := &mockPlainTool{name: "plain"}
	if err := a.tools.Register(mockTool); err != nil {
		t.Fatal(err)
	}

	params := a.getToolVolumeParams("plain")
	if params != nil {
		t.Errorf("expected nil for tool without VolumeParamProvider, got %v", params)
	}
}

func TestGetToolVolumeParamsToolNotFound(t *testing.T) {
	a := volumeTestAgent(AgentConfig{Name: "test"})
	params := a.getToolVolumeParams("nonexistent")
	if params != nil {
		t.Errorf("expected nil for nonexistent tool, got %v", params)
	}
}

func TestPrioritizeVolumesCallerContext(t *testing.T) {
	store := volume.NewMemoryStore()
	ctx := context.Background()

	id1, _ := store.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "u", CreatorType: volume.CallerHuman})
	id2, _ := store.Create(ctx, volume.CreateOptions{Name: "v2", UserID: "u", CreatorType: volume.CallerHuman})
	id3, _ := store.Create(ctx, volume.CreateOptions{Name: "v3", UserID: "u", CreatorType: volume.CallerHuman})

	a := volumeTestAgentWithVolumeSvc(AgentConfig{Name: "test"}, store)

	allVols, _ := store.List(ctx, "u", volume.ListFilter{})

	vrc := &volumeResolveCtx{
		ctx:             ctx,
		userID:          "u",
		callerVolumeIDs: []string{id3},
	}

	result := a.prioritizeVolumes(vrc, store, allVols)
	if len(result) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(result))
	}
	if result[0].ID != id3 {
		t.Errorf("expected caller volume %q first, got %q", id3, result[0].ID)
	}

	remaining := make(map[string]bool)
	for _, v := range result[1:] {
		remaining[v.ID] = true
	}
	if !remaining[id1] || !remaining[id2] {
		t.Errorf("expected remaining volumes to contain %q and %q", id1, id2)
	}
}

func TestPrioritizeVolumesSessionContext(t *testing.T) {
	store := volume.NewMemoryStore()
	ctx := context.Background()

	id1, _ := store.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "u", CreatorType: volume.CallerHuman})
	id2, _ := store.Create(ctx, volume.CreateOptions{Name: "v2", UserID: "u", CreatorType: volume.CallerHuman})
	_, _ = store.Create(ctx, volume.CreateOptions{Name: "v3", UserID: "u", CreatorType: volume.CallerHuman})

	_ = store.AssociateSession(ctx, id2, "session-A")

	a := volumeTestAgentWithVolumeSvc(AgentConfig{Name: "test"}, store)

	allVols, _ := store.List(ctx, "u", volume.ListFilter{})

	vrc := &volumeResolveCtx{
		ctx:       ctx,
		userID:    "u",
		sessionID: "session-A",
	}

	result := a.prioritizeVolumes(vrc, store, allVols)
	if len(result) != 3 {
		t.Fatalf("expected 3 volumes, got %d", len(result))
	}
	if result[0].ID != id2 {
		t.Errorf("expected session volume %q first, got %q", id2, result[0].ID)
	}

	_ = id1
}

func TestPrioritizeVolumesNoPriority(t *testing.T) {
	store := volume.NewMemoryStore()
	ctx := context.Background()

	_, _ = store.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "u", CreatorType: volume.CallerHuman})
	_, _ = store.Create(ctx, volume.CreateOptions{Name: "v2", UserID: "u", CreatorType: volume.CallerHuman})

	a := volumeTestAgentWithVolumeSvc(AgentConfig{Name: "test"}, store)

	allVols, _ := store.List(ctx, "u", volume.ListFilter{})

	vrc := &volumeResolveCtx{
		ctx:    ctx,
		userID: "u",
	}

	result := a.prioritizeVolumes(vrc, store, allVols)
	if len(result) != len(allVols) {
		t.Fatalf("expected %d volumes, got %d", len(allVols), len(result))
	}
}

func TestResolveAutoCreate_SameSessionReuses(t *testing.T) {
	store := volume.NewMemoryStore()
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}, store)

	vrc := &volumeResolveCtx{
		ctx:       context.Background(),
		userID:    "u",
		sessionID: "session-A",
	}

	b1, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	b2, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if b1[0].VolumeID != b2[0].VolumeID {
		t.Fatalf("same session expected same volume; got %s vs %s", b1[0].VolumeID, b2[0].VolumeID)
	}
	vols, _ := store.List(context.Background(), "u", volume.ListFilter{})
	if len(vols) != 1 {
		t.Fatalf("expected exactly 1 volume created for one session; got %d", len(vols))
	}
}

func TestResolveAutoCreate_DifferentSessionsGetDistinct(t *testing.T) {
	store := volume.NewMemoryStore()
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}, store)

	vrcA := &volumeResolveCtx{ctx: context.Background(), userID: "u", sessionID: "session-A"}
	vrcB := &volumeResolveCtx{ctx: context.Background(), userID: "u", sessionID: "session-B"}

	bA, err := a.resolveVolumeBindings("my-tool", vrcA)
	if err != nil {
		t.Fatalf("session A resolve: %v", err)
	}
	bB, err := a.resolveVolumeBindings("my-tool", vrcB)
	if err != nil {
		t.Fatalf("session B resolve: %v", err)
	}
	if bA[0].VolumeID == bB[0].VolumeID {
		t.Fatalf("different sessions must get distinct volumes; both got %s", bA[0].VolumeID)
	}
	vols, _ := store.List(context.Background(), "u", volume.ListFilter{})
	if len(vols) != 2 {
		t.Fatalf("expected 2 distinct volumes, got %d", len(vols))
	}
}

func TestResolveAutoCreate_RecoversAcrossAgentRestart(t *testing.T) {
	store := volume.NewMemoryStore()
	cfg := AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}
	vrc := &volumeResolveCtx{ctx: context.Background(), userID: "u", sessionID: "session-A"}

	a1 := volumeTestAgentWithVolumeSvc(cfg, store)
	b1, err := a1.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("first agent resolve: %v", err)
	}

	a2 := volumeTestAgentWithVolumeSvc(cfg, store)
	b2, err := a2.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatalf("second agent resolve: %v", err)
	}

	if b1[0].VolumeID != b2[0].VolumeID {
		t.Fatalf("agent restart must reuse the session's existing volume; got %s then %s", b1[0].VolumeID, b2[0].VolumeID)
	}
	vols, _ := store.List(context.Background(), "u", volume.ListFilter{})
	if len(vols) != 1 {
		t.Fatalf("agent restart should NOT create a duplicate; got %d volumes for one session-slot", len(vols))
	}
}

func TestResolveAutoCreate_HeadlessNoSession_OneVolumePerProcess(t *testing.T) {
	store := volume.NewMemoryStore()
	a := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "test-agent",
		VolumeSlots: []VolumeSlot{
			{Name: "scratch", Provision: "auto_create", TTL: "1h"},
		},
		ToolVolumeBindings: map[string]map[string]string{
			"my-tool": {"temp": "scratch"},
		},
	}, store)

	vrc := &volumeResolveCtx{ctx: context.Background(), userID: "u"}

	b1, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := a.resolveVolumeBindings("my-tool", vrc)
	if err != nil {
		t.Fatal(err)
	}
	if b1[0].VolumeID != b2[0].VolumeID {
		t.Fatalf("headless same-process calls should reuse; got %s vs %s", b1[0].VolumeID, b2[0].VolumeID)
	}
}

func TestResolvedVolumeIDsForSession_FiltersByPrefix(t *testing.T) {
	a := &Agent{
		resolvedVolumes: map[string]string{
			volumeCacheKey("session-A", "workspace"): "vol-aaaaaaaaaaaaaaa1",
			volumeCacheKey("session-A", "cache"):     "vol-aaaaaaaaaaaaaaa2",
			volumeCacheKey("session-B", "workspace"): "vol-bbbbbbbbbbbbbbb1",
			volumeCacheKey("", "headless-slot"):      "vol-cccccccccccccccc",
		},
	}

	gotA := a.resolvedVolumeIDsForSession("session-A")
	slices.Sort(gotA)
	wantA := []string{"vol-aaaaaaaaaaaaaaa1", "vol-aaaaaaaaaaaaaaa2"}
	if !slices.Equal(gotA, wantA) {
		t.Errorf("session-A: got %v, want %v", gotA, wantA)
	}

	gotB := a.resolvedVolumeIDsForSession("session-B")
	if !slices.Equal(gotB, []string{"vol-bbbbbbbbbbbbbbb1"}) {
		t.Errorf("session-B: got %v, want [vol-bbbbbbbbbbbbbbb1]", gotB)
	}

	gotHeadless := a.resolvedVolumeIDsForSession("")
	if !slices.Equal(gotHeadless, []string{"vol-cccccccccccccccc"}) {
		t.Errorf("headless: got %v, want [vol-cccccccccccccccc]", gotHeadless)
	}

	if got := a.resolvedVolumeIDsForSession("session-Z"); len(got) != 0 {
		t.Errorf("unknown session: got %v, want []", got)
	}
}

func TestFindExistingSessionVolumeForSlot_PropagatesListError(t *testing.T) {
	listErr := errors.New("simulated list outage")
	a := volumeTestAgentWithVolumeSvc(
		AgentConfig{Name: "test"},
		&mockVolumeService{listSessionVolumesErr: listErr},
	)
	vrc := &volumeResolveCtx{ctx: context.Background(), sessionID: "s1", userID: "u1"}
	id, ok, err := a.findExistingSessionVolumeForSlot(vrc, &VolumeSlot{Name: "workspace"})
	if err == nil {
		t.Fatal("expected error to propagate from ListSessionVolumes, got nil")
	}
	if ok || id != "" {
		t.Errorf("ok/id should be zero on error, got (%q, %v)", id, ok)
	}
}

func TestFindExistingSessionVolumeForSlot_PropagatesGetError(t *testing.T) {
	a := volumeTestAgentWithVolumeSvc(
		AgentConfig{Name: "test"},
		&mockVolumeService{
			sessionVolumeIDs: []string{"vol-aaaaaaaaaaaaaaa1"},
			getErr:           errors.New("simulated get outage"),
		},
	)
	vrc := &volumeResolveCtx{ctx: context.Background(), sessionID: "s1", userID: "u1"}
	_, ok, err := a.findExistingSessionVolumeForSlot(vrc, &VolumeSlot{Name: "workspace"})
	if err == nil {
		t.Fatal("expected error to propagate from Get, got nil")
	}
	if ok {
		t.Error("ok should be false on Get error")
	}
}

func TestFindExistingSessionVolumeForSlot_SkipsCrossUserVolumes(t *testing.T) {
	owned := &volume.Volume{
		ID:          "vol-aaaaaaaaaaaaaaa1",
		Name:        "test-workspace",
		UserID:      "other_user",
		CreatorName: "test",
	}
	a := volumeTestAgentWithVolumeSvc(
		AgentConfig{Name: "test"},
		&mockVolumeService{
			sessionVolumeIDs: []string{"vol-aaaaaaaaaaaaaaa1"},
			getVolume:        owned,
		},
	)
	vrc := &volumeResolveCtx{ctx: context.Background(), sessionID: "s1", userID: "u1"}
	id, ok, err := a.findExistingSessionVolumeForSlot(vrc, &VolumeSlot{Name: "workspace"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || id != "" {
		t.Errorf("cross-user volume should not match; got (id=%q, ok=%v)", id, ok)
	}
}
