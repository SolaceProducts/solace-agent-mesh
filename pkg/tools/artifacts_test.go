// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type mockArtifactService struct {
	mu               sync.Mutex
	artifacts        map[string]*mockArtifact
	failSaves        map[string]int
	failListVersions map[string]bool
	failLoads        map[string]bool
}

type mockArtifact struct {
	versions []mockVersion
}

type mockVersion struct {
	content  []byte
	mimeType string
	metadata *artifact.VersionMetadata
}

func newMockArtifactService() *mockArtifactService {
	return &mockArtifactService{artifacts: make(map[string]*mockArtifact)}
}

func locKey(loc artifact.Locator) string {
	return loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
}

func scopePrefix(scope artifact.KeyScope) string {
	return scope.AppName + "/" + scope.UserID + "/" + scope.SessionID + "/"
}

func (s *mockArtifactService) Save(_ context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.failSaves[loc.Filename]; n > 0 {
		s.failSaves[loc.Filename] = n - 1
		return 0, fmt.Errorf("injected save failure for %q", loc.Filename)
	}
	key := locKey(loc)
	a, exists := s.artifacts[key]
	if !exists {
		a = &mockArtifact{}
		s.artifacts[key] = a
	}
	v := mockVersion{content: append([]byte(nil), content...), mimeType: mimeType}
	a.versions = append(a.versions, v)
	return len(a.versions) - 1, nil
}

func (s *mockArtifactService) Load(_ context.Context, loc artifact.Locator, version *int) (content []byte, mimeType string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key0 := locKey(loc)
	if s.failLoads[loc.Filename] {
		if a, ok := s.artifacts[key0]; ok && len(a.versions) > 0 {
			v := a.versions[len(a.versions)-1]
			return v.content, v.mimeType, errors.New("load: store degraded")
		}
		return nil, "", errors.New("load: store unavailable")
	}
	key := locKey(loc)
	a, exists := s.artifacts[key]
	if !exists || len(a.versions) == 0 {
		return nil, "", nil
	}
	idx := len(a.versions) - 1
	if version != nil {
		idx = *version
		if idx < 0 || idx >= len(a.versions) {
			return nil, "", fmt.Errorf("version %d not found", idx)
		}
	}
	v := a.versions[idx]
	return append([]byte(nil), v.content...), v.mimeType, nil
}

func (s *mockArtifactService) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	data, mime, err := s.Load(ctx, loc, version)
	if err != nil {
		return nil, "", 0, err
	}
	if data == nil {
		return nil, "", 0, nil
	}
	return io.NopCloser(bytes.NewReader(data)), mime, int64(len(data)), nil
}

func (s *mockArtifactService) Delete(_ context.Context, loc artifact.Locator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.artifacts, locKey(loc))
	return nil
}

func (s *mockArtifactService) ListKeys(_ context.Context, scope artifact.KeyScope) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := scopePrefix(scope)
	var keys []string
	for k := range s.artifacts {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k[len(prefix):])
		}
	}
	slices.Sort(keys)
	return keys, nil
}

func (s *mockArtifactService) ListVersions(_ context.Context, loc artifact.Locator) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failListVersions[loc.Filename] {
		return nil, errors.New("list versions: store unavailable")
	}
	a, exists := s.artifacts[locKey(loc)]
	if !exists {
		return nil, nil
	}
	versions := make([]int, len(a.versions))
	for i := range a.versions {
		versions[i] = i
	}
	return versions, nil
}

func (s *mockArtifactService) GetVersionMetadata(_ context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, exists := s.artifacts[locKey(loc)]
	if !exists || len(a.versions) == 0 {
		return nil, nil
	}
	idx := len(a.versions) - 1
	if version != nil {
		idx = *version
	}
	if idx < 0 || idx >= len(a.versions) {
		return nil, nil
	}
	return a.versions[idx].metadata, nil
}

func (s *mockArtifactService) SaveMetadata(_ context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, exists := s.artifacts[locKey(loc)]
	if !exists || version >= len(a.versions) {
		return fmt.Errorf("artifact or version not found")
	}
	a.versions[version].metadata = meta
	return nil
}

func (s *mockArtifactService) SaveVersion(_ context.Context, _ artifact.Locator, _ int, _ []byte, _ string) error {
	return nil
}

func newTestToolCtx(svc artifact.Service) *tool.Context {
	return &tool.Context{
		UserID:    "user-1",
		SessionID: "session-1",
		AgentName: "test-agent",
		ArtifactService: &artifact.ScopedService{
			Inner:     svc,
			Scope:     "agent",
			Namespace: "test-ns",
		},
	}
}

func saveTestArtifact(ctx context.Context, svc *mockArtifactService, toolCtx *tool.Context, filename, content, mimeType string) {
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", filename)
	_, _ = svc.Save(ctx, loc, []byte(content), mimeType)
}

func TestListArtifactsEmpty(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	lt := &ListArtifactsTool{}

	result, err := lt.Execute(context.Background(), nil, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Message != "Found 0 artifacts." {
		t.Errorf("message = %q", result.Message)
	}
}

func TestListArtifactsWithEntries(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "data.csv")
	_, _ = svc.Save(context.Background(), loc, []byte("a,b,c"), "text/csv")
	_ = svc.SaveMetadata(context.Background(), loc, 0, &artifact.VersionMetadata{
		MIMEType:  "text/csv",
		SizeBytes: 5,
	})

	lt := &ListArtifactsTool{}
	result, err := lt.Execute(context.Background(), nil, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data == nil {
		t.Fatal("data is nil")
	}
	artifacts, ok := result.Data["artifacts"].([]map[string]any)
	if !ok {
		t.Fatalf("artifacts type = %T", result.Data["artifacts"])
	}
	if len(artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(artifacts))
	}
	if artifacts[0]["filename"] != "data.csv" {
		t.Errorf("filename = %v", artifacts[0]["filename"])
	}
	versions, ok := artifacts[0]["versions"].([]int)
	if !ok {
		t.Fatalf("versions type = %T, want []int", artifacts[0]["versions"])
	}
	if len(versions) != 1 || versions[0] != 0 {
		t.Errorf("versions = %v, want [0]", versions)
	}
	metaSummary, ok := artifacts[0]["metadata_summary"].(map[string]any)
	if !ok {
		t.Fatalf("metadata_summary type = %T", artifacts[0]["metadata_summary"])
	}
	if metaSummary["type"] != "text/csv" {
		t.Errorf("metadata_summary.type = %v", metaSummary["type"])
	}
}

func TestLoadArtifactContent(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "test.txt")
	_, _ = svc.Save(context.Background(), loc, []byte("hello world"), "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "test.txt",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data["content"] != "hello world" {
		t.Errorf("content = %v", result.Data["content"])
	}
	if result.Data["mime_type"] != "text/plain" {
		t.Errorf("mime_type = %v", result.Data["mime_type"])
	}
}

func TestLoadArtifactMetadataOnly(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "data.json")
	_, _ = svc.Save(context.Background(), loc, []byte(`{"key": "value"}`), "application/json")
	_ = svc.SaveMetadata(context.Background(), loc, 0, &artifact.VersionMetadata{
		MIMEType:    "application/json",
		SizeBytes:   16,
		Description: "Test JSON data",
	})

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename":           "data.json",
		"version":            float64(0),
		"load_metadata_only": true,
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data["description"] != "Test JSON data" {
		t.Errorf("description = %v", result.Data["description"])
	}
	if _, hasContent := result.Data["content"]; hasContent {
		t.Error("load_metadata_only should not include content")
	}
}

func TestLoadArtifactNotFound(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "nonexistent.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
	if result.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ErrorCodeArtifactNotFound)
	}
}

func TestLoadArtifactMissingFilename(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
}

func TestLoadArtifactWithLineNumbers(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "code.py")
	_, _ = svc.Save(context.Background(), loc, []byte("line1\nline2\nline3"), "text/x-python")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename":             "code.py",
		"version":              float64(0),
		"include_line_numbers": true,
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	expected := "1\tline1\n2\tline2\n3\tline3"
	if result.Data["content"] != expected {
		t.Errorf("content = %q, want %q", result.Data["content"], expected)
	}
}

func TestLoadArtifactWithMaxContentLength(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "big.txt")
	longContent := make([]byte, 500)
	for i := range longContent {
		longContent[i] = 'A'
	}
	_, _ = svc.Save(context.Background(), loc, longContent, "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename":           "big.txt",
		"version":            float64(0),
		"max_content_length": float64(200),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	content := result.Data["content"].(string)
	if len(content) <= 200 {
		t.Error("expected truncation note to be appended")
	}
	if content[:200] != string(longContent[:200]) {
		t.Error("truncated content doesn't match original prefix")
	}
}

func TestDeleteArtifactWithConfirmation(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	saveTestArtifact(context.Background(), svc, toolCtx, "temp.txt", "temporary", "text/plain")

	dt := &DeleteArtifactTool{}

	result, err := dt.Execute(context.Background(), map[string]any{
		"filename": "temp.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusPartial {
		t.Errorf("status = %q, want partial", result.Status)
	}
	if result.Data["confirmation_required"] != true {
		t.Error("expected confirmation_required = true")
	}

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "temp.txt")
	content, _, _ := svc.Load(context.Background(), loc, nil)
	if content == nil {
		t.Error("artifact should still exist before confirmation")
	}

	result, err = dt.Execute(context.Background(), map[string]any{
		"filename":       "temp.txt",
		"confirm_delete": true,
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}

	content, _, _ = svc.Load(context.Background(), loc, nil)
	if content != nil {
		t.Error("artifact should be deleted")
	}
}

func TestDeleteArtifactRejectsVersionSpecific(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	dt := &DeleteArtifactTool{}
	result, err := dt.Execute(context.Background(), map[string]any{
		"filename": "test.txt",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
}

func TestDeleteArtifactMissingFilename(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	dt := &DeleteArtifactTool{}
	result, err := dt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want error", result.Status)
	}
}

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"data.csv", "text/csv"},
		{"config.json", "application/json"},
		{"doc.md", "text/markdown"},
		{"script.py", "text/x-python"},
		{"main.go", "text/x-go"},
		{"image.png", "image/png"},
		{"unknown", "text/plain; charset=utf-8"},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			got := DetectMIMEType(tc.filename, []byte("some text content"))
			if got != tc.want {
				t.Errorf("DetectMIMEType(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

func TestToolInterfaceCompliance(t *testing.T) {
	var _ tool.BuiltinTool = &ListArtifactsTool{}
	var _ tool.BuiltinTool = &LoadArtifactTool{}
	var _ tool.BuiltinTool = &DeleteArtifactTool{}
}

func TestLoadArtifactSpecificVersion(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	saveTestArtifact(context.Background(), svc, toolCtx, "data.txt", "v0 content", "text/plain")
	saveTestArtifact(context.Background(), svc, toolCtx, "data.txt", "v1 content", "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "data.txt",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["content"] != "v0 content" {
		t.Errorf("content = %v, want v0 content", result.Data["content"])
	}
}

func TestAddLineNumbers(t *testing.T) {
	input := "first\nsecond\nthird"
	got := addLineNumbers(input)
	want := "1\tfirst\n2\tsecond\n3\tthird"
	if got != want {
		t.Errorf("addLineNumbers = %q, want %q", got, want)
	}
}

func TestAddLineNumbersSingleLine(t *testing.T) {
	got := addLineNumbers("hello")
	want := "1\thello"
	if got != want {
		t.Errorf("addLineNumbers = %q, want %q", got, want)
	}
}

func TestAddLineNumbersFrom(t *testing.T) {
	input := "line a\nline b\nline c"
	got := addLineNumbersFrom(input, 10)
	want := "10\tline a\n11\tline b\n12\tline c"
	if got != want {
		t.Errorf("addLineNumbersFrom = %q, want %q", got, want)
	}
}

func TestLoadArtifactWithOffset(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	content := "line1\nline2\nline3\nline4\nline5"
	saveTestArtifact(context.Background(), svc, toolCtx, "code.txt", content, "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "code.txt",
		"version":  float64(0),
		"offset":   float64(2),
		"limit":    float64(2),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data["content"] != "line2\nline3" {
		t.Errorf("content = %q, want %q", result.Data["content"], "line2\nline3")
	}
	if result.Data["total_lines"] != 5 {
		t.Errorf("total_lines = %v, want 5", result.Data["total_lines"])
	}
}

func TestLoadArtifactWithOffsetAndLineNumbers(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	content := "aaa\nbbb\nccc\nddd\neee"
	saveTestArtifact(context.Background(), svc, toolCtx, "data.txt", content, "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename":             "data.txt",
		"version":              float64(0),
		"offset":               float64(3),
		"limit":                float64(2),
		"include_line_numbers": true,
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	expected := "3\tccc\n4\tddd"
	if result.Data["content"] != expected {
		t.Errorf("content = %q, want %q", result.Data["content"], expected)
	}
}

func TestLoadArtifactOffsetBeyondEnd(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	saveTestArtifact(context.Background(), svc, toolCtx, "small.txt", "one\ntwo", "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "small.txt",
		"version":  float64(0),
		"offset":   float64(100),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data["content"] != "" {
		t.Errorf("content = %q, want empty", result.Data["content"])
	}
}

func TestLoadArtifactLimitOnly(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	content := "a\nb\nc\nd\ne"
	saveTestArtifact(context.Background(), svc, toolCtx, "lines.txt", content, "text/plain")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "lines.txt",
		"version":  float64(0),
		"limit":    float64(3),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["content"] != "a\nb\nc" {
		t.Errorf("content = %q, want %q", result.Data["content"], "a\nb\nc")
	}
	if result.Data["total_lines"] != 5 {
		t.Errorf("total_lines = %v, want 5", result.Data["total_lines"])
	}
}

func TestLoadArtifactImage(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	imgData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	saveTestArtifact(context.Background(), svc, toolCtx, "photo.png", string(imgData), "image/png")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "photo.png",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if len(result.ImageBlocks) != 1 {
		t.Fatalf("ImageBlocks len = %d, want 1", len(result.ImageBlocks))
	}
	if result.ImageBlocks[0].MIMEType != "image/png" {
		t.Errorf("ImageBlocks[0].MIMEType = %q", result.ImageBlocks[0].MIMEType)
	}
	if len(result.ImageBlocks[0].Data) != len(imgData) {
		t.Errorf("ImageBlocks[0].Data len = %d, want %d", len(result.ImageBlocks[0].Data), len(imgData))
	}
	contentStr, _ := result.Data["content"].(string)
	if contentStr == "" || strings.Contains(contentStr, "not displayed") {
		t.Errorf("content = %q, should indicate image is loaded", contentStr)
	}
}

func TestLoadArtifactBinaryNonImage(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	saveTestArtifact(context.Background(), svc, toolCtx, "data.bin", "binary stuff", "application/octet-stream")

	lt := &LoadArtifactTool{}
	result, err := lt.Execute(context.Background(), map[string]any{
		"filename": "data.bin",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImageBlocks) != 0 {
		t.Errorf("ImageBlocks len = %d, want 0", len(result.ImageBlocks))
	}
	contentStr, _ := result.Data["content"].(string)
	if !strings.Contains(contentStr, "not displayed") {
		t.Errorf("content = %q, should say not displayed", contentStr)
	}
}

func TestIsSupportedImageType(t *testing.T) {
	supported := []string{"image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif"}
	for _, mt := range supported {
		if !isSupportedImageType(mt) {
			t.Errorf("isSupportedImageType(%q) = false, want true", mt)
		}
	}
	unsupported := []string{"image/svg+xml", "image/tiff", "application/pdf", "text/plain"}
	for _, mt := range unsupported {
		if isSupportedImageType(mt) {
			t.Errorf("isSupportedImageType(%q) = true, want false", mt)
		}
	}
}

func TestFormatVersionRange(t *testing.T) {
	tests := []struct {
		versions []int
		want     string
	}{
		{nil, ""},
		{[]int{0}, "0"},
		{[]int{0, 1}, "0, 1"},
		{[]int{0, 1, 2}, "0, 1, 2"},
		{[]int{0, 1, 2, 3}, "0-3"},
		{[]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, "0-9"},
	}
	for _, tt := range tests {
		got := formatVersionRange(tt.versions)
		if got != tt.want {
			t.Errorf("formatVersionRange(%v) = %q, want %q", tt.versions, got, tt.want)
		}
	}
}

func TestVersionHint(t *testing.T) {
	v := 0
	if hint := versionHint([]int{0, 1}, &v, 1); hint != "" {
		t.Errorf("expected no hint with explicit version, got %q", hint)
	}

	if hint := versionHint([]int{0}, nil, 0); hint != "" {
		t.Errorf("expected no hint with single version, got %q", hint)
	}

	hint := versionHint([]int{0, 1}, nil, 1)
	if hint == "" {
		t.Fatal("expected hint for multi-version artifact")
	}
	if !strings.Contains(hint, "version 1") {
		t.Errorf("hint should mention loaded version, got %q", hint)
	}
	if !strings.Contains(hint, "0, 1") {
		t.Errorf("hint should list versions, got %q", hint)
	}

	hint = versionHint([]int{0, 1, 2, 3, 4, 5}, nil, 5)
	if !strings.Contains(hint, "0-5") {
		t.Errorf("hint should use range format for 6 versions, got %q", hint)
	}
}

func TestBinaryArtifactAdviceIsToolAgnostic(t *testing.T) {
	for _, tc := range []struct{ name, filename, mimeType string }{
		{"pdf", "report.pdf", "application/pdf"},
		{"xlsx", "sales.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"docx", "memo.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"legacy xls", "old.xls", "application/vnd.ms-excel"},
		{"archive with no reader in the mesh", "bundle.zip", "application/zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", tc.filename)
			_, _ = svc.Save(context.Background(), loc, []byte("\x00\x01binary"), tc.mimeType)

			lt := &LoadArtifactTool{}
			result, err := lt.Execute(context.Background(), map[string]any{"filename": tc.filename}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			advice, _ := result.Data["message_to_llm"].(string)
			if advice == "" {
				t.Fatalf("no message_to_llm for %s; the model is left to guess", tc.mimeType)
			}
			if !strings.Contains(advice, "could not be read") {
				t.Errorf("advice drops the give-up instruction, which is the part aimed at the "+
					"measured failure: %q", advice)
			}
			if !strings.Contains(advice, "run it on this artifact by filename") {
				t.Errorf("advice lost the actionable instruction and is now only discouragement: %q", advice)
			}
			if !strings.Contains(advice, "designed to read or convert") {
				t.Errorf("advice no longer scopes to a purpose-built tool, so a code sandbox "+
					"satisfies it: %q", advice)
			}
			if !strings.Contains(advice, "contents") {
				t.Errorf("advice no longer says \"contents\", so nothing holds it to wording that "+
					"covers artifacts with no text: %q", advice)
			}
			if strings.Contains(advice, "text") {
				t.Errorf("advice presupposes text, but it also reaches images and audio: %q", advice)
			}
			if !strings.Contains(advice, "parse the file's bytes") {
				t.Errorf("advice no longer warns against parsing the bytes: %q", advice)
			}
			if !strings.Contains(advice, "code sandbox") {
				t.Errorf("advice no longer names the sandbox as the thing not to do, which is "+
					"the measured failure: %q", advice)
			}
			for _, name := range []string{
				"convert_pdf_to_markdown", "convert_file_to_markdown", "pdf_extract_text",
				"libreoffice_convert", "xlsx_read", "artifact_grep", "execute_python_code",
			} {
				if strings.Contains(advice, name) {
					t.Errorf("advice names the tool %q; naming a tool reintroduces the registry-drift "+
						"class this deliberately avoids: %q", name, advice)
				}
			}
			if c, _ := result.Data["content"].(string); !strings.Contains(c, "Content not displayed") {
				t.Errorf("content = %q, want the binary summary retained", c)
			}
		})
	}
}

func TestBinaryArtifactResultImageSizeGate(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")

	atLimit := binaryArtifactResult(binaryArtifactInfo{
		filename: "chart.png", mimeType: "image/png",
		version: 0, sizeBytes: maxImageSizeBytes, content: png,
	})
	if len(atLimit.ImageBlocks) != 1 {
		t.Fatalf("image at the size limit produced %d image blocks, want 1", len(atLimit.ImageBlocks))
	}
	if got := atLimit.ImageBlocks[0].MIMEType; got != "image/png" {
		t.Errorf("image block MIME = %q, want image/png", got)
	}
	if !bytes.Equal(atLimit.ImageBlocks[0].Data, png) {
		t.Error("image block does not carry the artifact bytes")
	}

	over := binaryArtifactResult(binaryArtifactInfo{
		filename: "huge.png", mimeType: "image/png",
		version: 0, sizeBytes: maxImageSizeBytes + 1, content: png,
	})
	if len(over.ImageBlocks) != 0 {
		t.Errorf("image over the size limit produced %d image blocks, want 0 — it must fall "+
			"through to the binary summary rather than be sent to the vision API",
			len(over.ImageBlocks))
	}
	if c, _ := over.Data["content"].(string); !strings.Contains(c, "Content not displayed") {
		t.Errorf("content = %q, want the binary summary for an oversized image", c)
	}
}

func TestBinaryArtifactResultPayloadFields(t *testing.T) {
	result := binaryArtifactResult(binaryArtifactInfo{
		filename: "old.xls", mimeType: "application/vnd.ms-excel",
		version: 7, sizeBytes: 4242, content: []byte("\x00\x01binary"),
	})
	if got := result.Data["version"]; got != 7 {
		t.Errorf("version = %v, want 7", got)
	}
	if got := result.Data["size_bytes"]; got != 4242 {
		t.Errorf("size_bytes = %v, want 4242", got)
	}
	if got := result.Data["mime_type"]; got != "application/vnd.ms-excel" {
		t.Errorf("mime_type = %v, want application/vnd.ms-excel", got)
	}
	if got := result.Data["filename"]; got != "old.xls" {
		t.Errorf("filename = %v, want old.xls", got)
	}
	if got := result.Data["message_to_llm"]; got != binaryExtractionAdvice {
		t.Errorf("message_to_llm = %v, want the binary advice", got)
	}
}

func TestLoadArtifactRefusedArgumentCodes(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]any
		wantMessage string
	}{
		{"filename missing", map[string]any{}, "filename is required"},
		{"negative version from JSON", map[string]any{"filename": "notes.txt", "version": float64(-1)}, "must be a positive integer"},
		{"negative int version", map[string]any{"filename": "notes.txt", "version": -1}, "must be a positive integer"},
		{"negative int64 version", map[string]any{"filename": "notes.txt", "version": int64(-1)}, "must be a positive integer"},
		{"pinned version conflicts with version", map[string]any{"filename": "notes.txt:1", "version": float64(2)}, "pass one or the other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			saveTestArtifact(t.Context(), svc, toolCtx, "notes.txt", "hello\n", "text/plain")

			result, err := (&LoadArtifactTool{}).Execute(t.Context(), tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, tc.wantMessage) {
				t.Fatalf("result = %s: %q, want an error containing %q", result.Status, result.Message, tc.wantMessage)
			}
			if want := tool.DispatchErrorCode(taxonomy.DispatchClassValidation); result.ErrorCode != want {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
			}
		})
	}
}
