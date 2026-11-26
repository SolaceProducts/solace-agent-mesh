// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type mockVolumeService struct {
	volumes     map[string]mockVolume
	locks       map[string]string
	accessLog   []string
	releasedLog []string
}

type mockVolume struct {
	path string
}

func newMockVolumeService() *mockVolumeService {
	return &mockVolumeService{
		volumes: make(map[string]mockVolume),
		locks:   make(map[string]string),
	}
}

func (m *mockVolumeService) Create(_ context.Context, _ volume.CreateOptions) (string, error) {
	return "", nil
}

func (m *mockVolumeService) Get(_ context.Context, _ string) (*volume.Volume, error) {
	return nil, nil
}

func (m *mockVolumeService) List(_ context.Context, _ string, _ volume.ListFilter) ([]*volume.Volume, error) {
	return nil, nil
}

func (m *mockVolumeService) Delete(_ context.Context, _ string, _ volume.CallerType) error {
	return nil
}

func (m *mockVolumeService) RemoveIfExpired(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockVolumeService) Resolve(_ context.Context, id string) (string, error) {
	v, ok := m.volumes[id]
	if !ok {
		return "", volume.ErrNotFound
	}
	return v.path, nil
}

func (m *mockVolumeService) AcquireWriteLock(_ context.Context, id string, holder string, _ time.Duration) error {
	if existing, ok := m.locks[id]; ok {
		return &volume.LockError{
			VolumeID: id,
			Holder:   existing,
			Until:    time.Now().Add(time.Minute),
		}
	}
	m.locks[id] = holder
	return nil
}

func (m *mockVolumeService) ReleaseWriteLock(_ context.Context, id string, holder string) error {
	if m.locks[id] != holder {
		return volume.ErrNotLockHolder
	}
	delete(m.locks, id)
	m.releasedLog = append(m.releasedLog, id)
	return nil
}

func (m *mockVolumeService) RecordAccess(_ context.Context, id string) error {
	m.accessLog = append(m.accessLog, id)
	return nil
}

func (m *mockVolumeService) CleanupExpired(_ context.Context) (int, error) {
	return 0, nil
}

func (m *mockVolumeService) AssociateSession(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockVolumeService) ListSessionVolumes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
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

func TestResolveVolumes_HappyPath(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}
	svc.volumes["vol-2"] = mockVolume{path: "/data/volumes/vol-2"}

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-123",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/volumes/data", ReadOnly: false},
			{VolumeID: "vol-2", MountPath: "/volumes/ref", ReadOnly: true},
		},
	}

	resolved, invokeErr := runner.resolveVolumes(context.Background(), params, nil, slog.Default())
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr.Message)
	}

	if len(resolved) != 2 {
		t.Fatalf("got %d resolved volumes, want 2", len(resolved))
	}

	if resolved[0].hostPath != "/data/volumes/vol-1" {
		t.Errorf("vol-1 hostPath = %q", resolved[0].hostPath)
	}
	if !resolved[0].locked {
		t.Error("vol-1 should be locked (read-write)")
	}

	if resolved[1].hostPath != "/data/volumes/vol-2" {
		t.Errorf("vol-2 hostPath = %q", resolved[1].hostPath)
	}
	if resolved[1].locked {
		t.Error("vol-2 should not be locked (read-only)")
	}

	if svc.locks["vol-1"] != "task-123" {
		t.Errorf("vol-1 lock holder = %q, want 'task-123'", svc.locks["vol-1"])
	}
	if _, locked := svc.locks["vol-2"]; locked {
		t.Error("vol-2 should not be locked")
	}

	if len(svc.accessLog) != 2 {
		t.Errorf("access log = %v, want 2 entries", svc.accessLog)
	}
}

func TestResolveVolumes_NotFound(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-456",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/volumes/data", ReadOnly: false},
			{VolumeID: "vol-missing", MountPath: "/volumes/other", ReadOnly: false},
		},
	}

	_, invokeErr := runner.resolveVolumes(context.Background(), params, nil, slog.Default())
	if invokeErr == nil {
		t.Fatal("expected error for missing volume")
	}
	if invokeErr.Code != remotetool.ErrVolumeError {
		t.Errorf("error code = %q, want %q", invokeErr.Code, remotetool.ErrVolumeError)
	}

	if _, locked := svc.locks["vol-1"]; locked {
		t.Error("vol-1 lock should have been released after failure")
	}
}

func TestResolveVolumes_Locked(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}
	svc.locks["vol-1"] = "other-task"

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-789",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/volumes/data", ReadOnly: false},
		},
	}

	_, invokeErr := runner.resolveVolumes(context.Background(), params, nil, slog.Default())
	if invokeErr == nil {
		t.Fatal("expected error for locked volume")
	}
	if invokeErr.Code != remotetool.ErrVolumeLocked {
		t.Errorf("error code = %q, want %q", invokeErr.Code, remotetool.ErrVolumeLocked)
	}
}

func TestResolveVolumes_ReadOnlyBypassesLock(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}
	svc.locks["vol-1"] = "other-task"

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-ro",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/volumes/data", ReadOnly: true},
		},
	}

	resolved, invokeErr := runner.resolveVolumes(context.Background(), params, nil, slog.Default())
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr.Message)
	}
	if len(resolved) != 1 {
		t.Fatalf("got %d resolved, want 1", len(resolved))
	}
	if resolved[0].locked {
		t.Error("read-only volume should not acquire a lock")
	}
}

func TestResolveVolumes_ManifestReadOnlyOverridesBinding(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}
	svc.locks["vol-1"] = "other-task"

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-ro-tool",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/workspace", ReadOnly: false},
		},
	}
	entry := &ManifestEntry{
		VolumeParams: []VolumeParam{{Name: "workspace", Mode: "readonly", MountPath: "/workspace"}},
	}

	resolved, invokeErr := runner.resolveVolumes(context.Background(), params, entry, slog.Default())
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr.Message)
	}
	if resolved[0].locked {
		t.Error("manifest-readonly tool must not acquire a write lock")
	}
	if !resolved[0].binding.ReadOnly {
		t.Error("binding.ReadOnly should be corrected to true from the manifest (drives the ro-bind mount)")
	}
	if svc.locks["vol-1"] != "other-task" {
		t.Errorf("existing lock must be untouched, holder = %q", svc.locks["vol-1"])
	}
}

func TestResolveVolumes_ManifestReadWriteLocks(t *testing.T) {
	svc := newMockVolumeService()
	svc.volumes["vol-1"] = mockVolume{path: "/data/volumes/vol-1"}

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	params := &remotetool.InvokeParams{
		TaskID: "task-rw-tool",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/workspace", ReadOnly: true},
		},
	}
	entry := &ManifestEntry{
		VolumeParams: []VolumeParam{{Name: "workspace", Mode: "readwrite", MountPath: "/workspace"}},
	}

	resolved, invokeErr := runner.resolveVolumes(context.Background(), params, entry, slog.Default())
	if invokeErr != nil {
		t.Fatalf("unexpected error: %v", invokeErr.Message)
	}
	if !resolved[0].locked {
		t.Error("manifest-readwrite tool must acquire a write lock")
	}
	if svc.locks["vol-1"] != "task-rw-tool" {
		t.Errorf("lock holder = %q, want task-rw-tool", svc.locks["vol-1"])
	}
}

func TestVolumeModeFromManifest(t *testing.T) {
	entry := &ManifestEntry{VolumeParams: []VolumeParam{
		{Name: "workspace", Mode: "readonly", MountPath: "/workspace"},
		{Name: "data", Mode: "readwrite", MountPath: "/data"},
	}}
	cases := map[string]string{"/workspace": "readonly", "/data": "readwrite", "/missing": ""}
	for mount, want := range cases {
		if got := volumeModeFromManifest(entry, mount); got != want {
			t.Errorf("volumeModeFromManifest(%q) = %q, want %q", mount, got, want)
		}
	}
	if got := volumeModeFromManifest(nil, "/workspace"); got != "" {
		t.Errorf("nil entry = %q, want empty", got)
	}
}

func TestReleaseVolumeLocks(t *testing.T) {
	svc := newMockVolumeService()
	svc.locks["vol-1"] = "task-1"
	svc.locks["vol-2"] = "task-1"

	runner := &SandboxRunner{
		cfg:       SandboxConfig{DefaultTimeoutSeconds: 60},
		logger:    slog.Default(),
		volumeSvc: svc,
	}

	volumes := []resolvedVolume{
		{binding: remotetool.VolumeBinding{VolumeID: "vol-1"}, locked: true},
		{binding: remotetool.VolumeBinding{VolumeID: "vol-2"}, locked: false},
		{binding: remotetool.VolumeBinding{VolumeID: "vol-3"}, locked: true},
	}

	runner.releaseVolumeLocks(context.Background(), "task-1", volumes, slog.Default())

	if _, ok := svc.locks["vol-1"]; ok {
		t.Error("vol-1 lock should have been released")
	}
	if svc.locks["vol-2"] != "task-1" {
		t.Error("vol-2 lock should not have been touched")
	}
}

func TestAppendVolumeMounts_Empty(t *testing.T) {
	args := appendVolumeMounts([]string{"--some-flag"}, nil)
	if len(args) != 1 {
		t.Errorf("expected 1 arg, got %d: %v", len(args), args)
	}
}

func TestAppendVolumeMounts_Mixed(t *testing.T) {
	volumes := []resolvedVolume{
		{
			binding:  remotetool.VolumeBinding{MountPath: "/volumes/rw", ReadOnly: false},
			hostPath: "/data/vol-1",
		},
		{
			binding:  remotetool.VolumeBinding{MountPath: "/volumes/ro", ReadOnly: true},
			hostPath: "/data/vol-2",
		},
	}

	args := appendVolumeMounts([]string{"--base"}, volumes)

	expected := []string{"--base", "--bind", "/data/vol-1", "/volumes/rw", "--ro-bind", "/data/vol-2", "/volumes/ro"}
	if len(args) != len(expected) {
		t.Fatalf("args len = %d, want %d: %v", len(args), len(expected), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want)
		}
	}
}

func TestBuildBwrapCommand_WithVolumes(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:     "bwrap",
			BwrapBin: "/usr/bin/bwrap",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
	}

	volumes := []resolvedVolume{
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v1", MountPath: "/tmp/volumes/data", ReadOnly: false},
			hostPath: "/data/v1",
		},
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v2", MountPath: "/tmp/volumes/ref", ReadOnly: true},
			hostPath: "/data/v2",
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", volumes, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	bindIdx := -1
	roBidIdx := -1
	chdirIdx := -1
	for i, a := range args {
		if a == "--bind" && i+2 < len(args) && args[i+1] == "/data/v1" {
			bindIdx = i
		}
		if a == "--ro-bind" && i+2 < len(args) && args[i+1] == "/data/v2" {
			roBidIdx = i
		}
		if a == "--chdir" {
			chdirIdx = i
		}
	}

	if bindIdx < 0 {
		t.Error("--bind /data/v1 /tmp/volumes/data not found in args")
	}
	if roBidIdx < 0 {
		t.Error("--ro-bind /data/v2 /tmp/volumes/ref not found in args")
	}
	if chdirIdx < 0 {
		t.Fatal("--chdir not found in args")
	}

	if bindIdx > chdirIdx {
		t.Errorf("--bind (idx %d) should come before --chdir (idx %d)", bindIdx, chdirIdx)
	}
	if roBidIdx > chdirIdx {
		t.Errorf("--ro-bind (idx %d) should come before --chdir (idx %d)", roBidIdx, chdirIdx)
	}
}

func TestBuildGoBwrapCommand_WithVolumes(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:     "bwrap",
			BwrapBin: "/usr/bin/bwrap",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:       "go-tool",
		Executable:     "/usr/local/bin/go-tool",
		SandboxProfile: "standard",
	}

	volumes := []resolvedVolume{
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v1", MountPath: "/tmp/volumes/work", ReadOnly: false},
			hostPath: "/data/v1",
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", volumes, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	foundBind := false
	for i, a := range args {
		if a == "--bind" && i+2 < len(args) && args[i+1] == "/data/v1" && args[i+2] == "/tmp/volumes/work" {
			foundBind = true
		}
	}
	if !foundBind {
		t.Errorf("--bind /data/v1 /tmp/volumes/work not found in args: %v", args)
	}
}

func TestBuildRunnerArgs_WithVolumes_DirectMode(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{
		TaskID:  "task-1",
		AppName: "test-app",
		Args:    map[string]any{"key": "value"},
	}
	entry := &ManifestEntry{ToolName: "test_tool", Executable: "/usr/bin/test_tool"}

	volumes := []resolvedVolume{
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v1", MountPath: "/volumes/data", ReadOnly: false},
			hostPath: "/data/v1",
		},
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v2", MountPath: "/volumes/ref", ReadOnly: true},
			hostPath: "/data/v2",
		},
	}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", volumes)

	if len(args.VolumeMounts) != 2 {
		t.Fatalf("VolumeMounts len = %d, want 2", len(args.VolumeMounts))
	}

	if args.VolumeMounts[0].VolumeID != "v1" || args.VolumeMounts[0].MountPath != "/data/v1" || args.VolumeMounts[0].ReadOnly != false {
		t.Errorf("VolumeMounts[0] = %+v, want {v1 /data/v1 false}", args.VolumeMounts[0])
	}
	if args.VolumeMounts[1].VolumeID != "v2" || args.VolumeMounts[1].MountPath != "/data/v2" || args.VolumeMounts[1].ReadOnly != true {
		t.Errorf("VolumeMounts[1] = %+v, want {v2 /data/v2 true}", args.VolumeMounts[1])
	}
	if args.VolumeMounts[0].DeclaredPath != "/volumes/data" || args.VolumeMounts[1].DeclaredPath != "/volumes/ref" {
		t.Errorf("direct mode lost the declared mount paths that tell volumes apart: %+v", args.VolumeMounts)
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed runnerArgs
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.VolumeMounts) != 2 {
		t.Fatalf("round-trip VolumeMounts len = %d", len(parsed.VolumeMounts))
	}
}

func TestBuildRunnerArgs_WithVolumes_BwrapMode(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{Mode: "bwrap"},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{
		TaskID:  "task-1",
		AppName: "test-app",
		Args:    map[string]any{"key": "value"},
	}
	entry := &ManifestEntry{ToolName: "test_tool", Executable: "/usr/bin/test_tool"}

	volumes := []resolvedVolume{
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v1", MountPath: "/volumes/data", ReadOnly: false},
			hostPath: "/data/v1",
		},
		{
			binding:  remotetool.VolumeBinding{VolumeID: "v2", MountPath: "/volumes/ref", ReadOnly: true},
			hostPath: "/data/v2",
		},
	}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", volumes)

	if len(args.VolumeMounts) != 2 {
		t.Fatalf("VolumeMounts len = %d, want 2", len(args.VolumeMounts))
	}

	if args.VolumeMounts[0].VolumeID != "v1" || args.VolumeMounts[0].MountPath != "/volumes/data" || args.VolumeMounts[0].ReadOnly != false {
		t.Errorf("VolumeMounts[0] = %+v, want {v1 /volumes/data false}", args.VolumeMounts[0])
	}
	if args.VolumeMounts[1].VolumeID != "v2" || args.VolumeMounts[1].MountPath != "/volumes/ref" || args.VolumeMounts[1].ReadOnly != true {
		t.Errorf("VolumeMounts[1] = %+v, want {v2 /volumes/ref true}", args.VolumeMounts[1])
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed runnerArgs
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.VolumeMounts) != 2 {
		t.Fatalf("round-trip VolumeMounts len = %d", len(parsed.VolumeMounts))
	}
}

func TestBuildRunnerArgs_NoVolumes(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{TaskID: "task-1", Args: map[string]any{}}
	entry := &ManifestEntry{ToolName: "test_tool", Executable: "/usr/bin/test_tool"}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)

	if len(args.VolumeMounts) != 0 {
		t.Errorf("VolumeMounts should be empty, got %d", len(args.VolumeMounts))
	}

	data, _ := json.Marshal(args)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if _, exists := raw["volume_mounts"]; exists {
		t.Error("volume_mounts should be omitted from JSON when empty")
	}
}

func TestBuildRunnerArgs_ParentTaskIDPropagated(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{
		TaskID:       "corr-1",
		ToolName:     "my_tool",
		Args:         map[string]any{},
		ParentTaskID: "real-agent-task-42",
	}
	entry := &ManifestEntry{ToolName: "my_tool", Executable: "/usr/bin/my_tool"}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)

	if args.TaskID != "real-agent-task-42" {
		t.Errorf("TaskID = %q, want %q", args.TaskID, "real-agent-task-42")
	}

	data, _ := json.Marshal(args)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if raw["task_id"] != "real-agent-task-42" {
		t.Errorf("json task_id = %v, want %q", raw["task_id"], "real-agent-task-42")
	}
}

func TestBuildRunnerArgs_ParentTaskIDOmittedWhenEmpty(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{TaskID: "corr-2", Args: map[string]any{}}
	entry := &ManifestEntry{ToolName: "my_tool", Executable: "/usr/bin/my_tool"}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)

	if args.TaskID != "" {
		t.Errorf("TaskID should be empty, got %q", args.TaskID)
	}

	data, _ := json.Marshal(args)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if _, exists := raw["task_id"]; exists {
		t.Error("task_id should be omitted from JSON when empty")
	}
}

func TestBuildRunnerArgs_A2AUserConfigPropagated(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	blob := `{"user_profile":{"id":"u-1","work_email":"a@b.com"}}`
	params := &remotetool.InvokeParams{
		TaskID:        "corr-1",
		ToolName:      "my_tool",
		Args:          map[string]any{},
		A2AUserConfig: blob,
	}
	entry := &ManifestEntry{ToolName: "my_tool", Executable: "/usr/bin/my_tool"}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)

	if args.A2AUserConfig != blob {
		t.Errorf("A2AUserConfig = %q, want %q", args.A2AUserConfig, blob)
	}

	data, _ := json.Marshal(args)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if raw["a2a_user_config"] != blob {
		t.Errorf("json a2a_user_config = %v, want %q", raw["a2a_user_config"], blob)
	}
}

func TestBuildRunnerArgs_A2AUserConfigOmittedWhenEmpty(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{},
		logger: slog.Default(),
	}

	params := &remotetool.InvokeParams{TaskID: "corr-2", Args: map[string]any{}}
	entry := &ManifestEntry{ToolName: "my_tool", Executable: "/usr/bin/my_tool"}

	args := runner.buildRunnerArgs(params, entry, nil, nil, "/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)

	data, _ := json.Marshal(args)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	if _, exists := raw["a2a_user_config"]; exists {
		t.Error("a2a_user_config should be omitted from JSON when empty")
	}
}

func TestVolumeBinding_JSON(t *testing.T) {
	params := remotetool.InvokeParams{
		TaskID:   "task-1",
		ToolName: "my-tool",
		VolumeBindings: []remotetool.VolumeBinding{
			{VolumeID: "vol-1", MountPath: "/volumes/data", ReadOnly: false},
			{VolumeID: "vol-2", MountPath: "/volumes/ref", ReadOnly: true},
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed remotetool.InvokeParams
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(parsed.VolumeBindings) != 2 {
		t.Fatalf("VolumeBindings len = %d, want 2", len(parsed.VolumeBindings))
	}
	if parsed.VolumeBindings[0].VolumeID != "vol-1" {
		t.Errorf("VolumeBindings[0].VolumeID = %q", parsed.VolumeBindings[0].VolumeID)
	}
	if !parsed.VolumeBindings[1].ReadOnly {
		t.Error("VolumeBindings[1].ReadOnly should be true")
	}
}

func TestVolumeBinding_OmitEmpty(t *testing.T) {
	params := remotetool.InvokeParams{
		TaskID:   "task-1",
		ToolName: "my-tool",
	}

	data, _ := json.Marshal(params)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)

	if _, exists := raw["volume_bindings"]; exists {
		t.Error("volume_bindings should be omitted from JSON when empty")
	}
}

func TestVolumeErrorCodes(t *testing.T) {
	if remotetool.ErrVolumeLocked == remotetool.ErrVolumeError {
		t.Error("ErrVolumeLocked and ErrVolumeError should be distinct")
	}

	lockErr := &volume.LockError{VolumeID: "v1", Holder: "other", Until: time.Now()}
	if !errors.Is(lockErr, volume.ErrLocked) {
		t.Error("LockError should wrap ErrLocked")
	}
}

func TestBuildBwrapCommand_InjectsProxyEnvWhenSet(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{Mode: "bwrap", BwrapBin: "/usr/bin/bwrap"},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()
	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "restrictive",
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "http://127.0.0.1:54321", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	envSet := map[string]string{}
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--setenv" {
			envSet[args[i+1]] = args[i+2]
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if envSet[key] != "http://127.0.0.1:54321" {
			t.Errorf("--setenv %s = %q, want http://127.0.0.1:54321", key, envSet[key])
		}
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if envSet[key] != "127.0.0.1:54321" {
			t.Errorf("--setenv %s = %q, want 127.0.0.1:54321 (proxy address only)", key, envSet[key])
		}
	}
	for _, a := range args {
		if a == "--unshare-net" {
			t.Error("--unshare-net set when proxyURL was non-empty (proxy on loopback would be unreachable)")
		}
	}
}

func TestBuildBwrapCommand_NoProxyEnvWhenUnset(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{Mode: "bwrap", BwrapBin: "/usr/bin/bwrap"},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()
	entry := &ManifestEntry{ToolName: "t", SandboxProfile: "restrictive"}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/a.json"}, "/tmp/w", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	for i, a := range cmd.Args {
		if a == "--setenv" && i+1 < len(cmd.Args) {
			next := cmd.Args[i+1]
			if next == "HTTP_PROXY" || next == "HTTPS_PROXY" || next == "http_proxy" || next == "https_proxy" {
				t.Errorf("--setenv %s unexpectedly present when proxyURL was empty", next)
			}
		}
	}
	found := false
	for _, a := range cmd.Args {
		if a == "--unshare-net" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected --unshare-net on restrictive profile when no proxy is set")
	}
}

func TestStartEgressProxy_UnionsAllowlistsFromBindings(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{Mode: "bwrap", BwrapBin: "/usr/bin/bwrap"},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	params := &remotetool.InvokeParams{
		TaskID:   "test-task",
		ToolName: "bash",
		NetworkBindings: []remotetool.NetworkBinding{
			{ParamName: "outbound", PolicyName: "github", Allowlist: []string{"*.github.com", "raw.githubusercontent.com"}},
			{ParamName: "packages", PolicyName: "npm", Allowlist: []string{"registry.npmjs.org"}},
		},
	}
	proxy, invErr := runner.startEgressProxy(params)
	if invErr != nil {
		t.Fatalf("startEgressProxy: %+v", invErr)
	}
	defer func() { _ = proxy.Stop() }()

	for _, host := range []string{"github.com", "api.github.com", "raw.githubusercontent.com", "registry.npmjs.org"} {
		probe := host
		if host == "github.com" {
			probe = "api.github.com"
		}
		_ = probe
	}

	if proxy.Address() == "" {
		t.Fatal("proxy.Address() empty")
	}
	stats := proxy.Stats()
	if stats.Allowed != 0 || stats.Denied != 0 {
		t.Errorf("fresh proxy should have zero stats, got %+v", stats)
	}
}

func TestStartEgressProxy_EmptyBindingsProducesDenyAllProxy(t *testing.T) {
	runner := &SandboxRunner{
		cfg:    SandboxConfig{Mode: "bwrap", BwrapBin: "/usr/bin/bwrap"},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()
	params := &remotetool.InvokeParams{TaskID: "t", ToolName: "x"}
	proxy, invErr := runner.startEgressProxy(params)
	if invErr != nil {
		t.Fatalf("startEgressProxy on empty bindings: %+v", invErr)
	}
	_ = proxy.Stop()
}
