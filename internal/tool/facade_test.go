// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type memArtifactService struct {
	data map[string][]byte
}

func newMemArtifactService() *memArtifactService {
	return &memArtifactService{data: make(map[string][]byte)}
}

func (m *memArtifactService) key(loc artifact.Locator) string {
	return loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
}

func (m *memArtifactService) Save(_ context.Context, loc artifact.Locator, content []byte, _ string) (int, error) {
	m.data[m.key(loc)] = content
	return 0, nil
}

func (m *memArtifactService) Load(_ context.Context, loc artifact.Locator, _ *int) (content []byte, mimeType string, err error) {
	if d, ok := m.data[m.key(loc)]; ok {
		return d, "text/plain", nil
	}
	return nil, "", nil
}

func (m *memArtifactService) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	data, mime, err := m.Load(ctx, loc, version)
	if err != nil {
		return nil, "", 0, err
	}
	if data == nil {
		return nil, "", 0, nil
	}
	return io.NopCloser(bytes.NewReader(data)), mime, int64(len(data)), nil
}

func (m *memArtifactService) Delete(_ context.Context, _ artifact.Locator) error { return nil }

func (m *memArtifactService) ListKeys(_ context.Context, scope artifact.KeyScope) ([]string, error) {
	prefix := scope.AppName + "/" + scope.UserID + "/" + scope.SessionID + "/"
	var keys []string
	for k := range m.data {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k[len(prefix):])
		}
	}
	return keys, nil
}

func (m *memArtifactService) ListVersions(_ context.Context, _ artifact.Locator) ([]int, error) {
	return []int{0}, nil
}

func (m *memArtifactService) GetVersionMetadata(_ context.Context, _ artifact.Locator, _ *int) (*artifact.VersionMetadata, error) {
	return &artifact.VersionMetadata{MIMEType: "text/plain"}, nil
}

func (m *memArtifactService) SaveMetadata(_ context.Context, _ artifact.Locator, _ int, _ *artifact.VersionMetadata) error {
	return nil
}
func (m *memArtifactService) SaveVersion(_ context.Context, _ artifact.Locator, _ int, _ []byte, _ string) error {
	return nil
}

func testContext() *Context {
	return &Context{
		UserID:    "user-1",
		SessionID: "sess-1",
		AgentName: "test-agent",
		ToolConfig: map[string]any{
			"api_key": "secret",
			"timeout": 30,
		},
	}
}

func testContextWithArtifacts() (*Context, *memArtifactService) {
	mem := newMemArtifactService()
	ctx := testContext()
	ctx.ArtifactService = &artifact.ScopedService{
		Inner:     mem,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	return ctx, mem
}

func TestFacadeProperties(t *testing.T) {
	f := testContext().Facade()
	if f.SessionID() != "sess-1" {
		t.Errorf("SessionID = %q", f.SessionID())
	}
	if f.UserID() != "user-1" {
		t.Errorf("UserID = %q", f.UserID())
	}
	if f.AgentName() != "test-agent" {
		t.Errorf("AgentName = %q", f.AgentName())
	}
}

func TestFacadeConfig(t *testing.T) {
	f := testContext().Facade()
	if f.Config("api_key", "").(string) != "secret" {
		t.Errorf("Config(api_key) = %v", f.Config("api_key", ""))
	}
	if f.Config("missing", "default") != "default" {
		t.Errorf("Config(missing) = %v", f.Config("missing", "default"))
	}
}

func TestFacadeConfigNilMap(t *testing.T) {
	ctx := &Context{}
	f := ctx.Facade()
	if f.Config("key", "default") != "default" {
		t.Errorf("Config with nil map should return default")
	}
}

func TestFacadeSendStatus(t *testing.T) {
	var got string
	ctx := testContext()
	ctx.SendStatus = func(msg string) error {
		got = msg
		return nil
	}

	f := ctx.Facade()
	if err := f.SendStatus("working..."); err != nil {
		t.Fatal(err)
	}
	if got != "working..." {
		t.Errorf("SendStatus message = %q", got)
	}
}

func TestFacadeSendStatusNilFunc(t *testing.T) {
	f := testContext().Facade()
	if err := f.SendStatus("test"); err != nil {
		t.Errorf("SendStatus with nil func should return nil, got %v", err)
	}
}

func TestFacadeSendSignal(t *testing.T) {
	var got any
	ctx := testContext()
	ctx.SendSignal = func(signal any) error {
		got = signal
		return nil
	}

	f := ctx.Facade()
	if err := f.SendSignal(map[string]any{"type": "progress"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("SendSignal not called")
	}
}

func TestFacadeNoArtifactService(t *testing.T) {
	f := testContext().Facade()

	_, _, err := f.LoadArtifact(context.Background(), "file.txt", nil)
	if err != ErrNoArtifactService {
		t.Errorf("LoadArtifact err = %v, want ErrNoArtifactService", err)
	}

	_, err = f.ListArtifacts(context.Background())
	if err != ErrNoArtifactService {
		t.Errorf("ListArtifacts err = %v", err)
	}

	_, err = f.LoadArtifactMetadata(context.Background(), "file.txt", nil)
	if err != ErrNoArtifactService {
		t.Errorf("LoadArtifactMetadata err = %v", err)
	}
}

func TestFacadeLoadArtifact(t *testing.T) {
	ctx, mem := testContextWithArtifacts()
	loc := ctx.ArtifactService.Resolve(ctx.AgentName, ctx.UserID, ctx.SessionID, "data.csv")
	if _, err := mem.Save(context.Background(), loc, []byte("a,b,c"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	f := ctx.Facade()
	content, mime, err := f.LoadArtifact(context.Background(), "data.csv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b,c" {
		t.Errorf("content = %q", content)
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q", mime)
	}
}

func TestFacadeListArtifacts(t *testing.T) {
	ctx, mem := testContextWithArtifacts()
	for _, name := range []string{"a.txt", "b.txt"} {
		loc := ctx.ArtifactService.Resolve(ctx.AgentName, ctx.UserID, ctx.SessionID, name)
		if _, err := mem.Save(context.Background(), loc, []byte("data"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}

	f := ctx.Facade()
	files, err := f.ListArtifacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("ListArtifacts returned %d files, want 2", len(files))
	}
}
