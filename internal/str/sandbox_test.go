// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	memstore "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type capturingHandler struct {
	records []slog.Record
}

func (h *capturingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(name string) slog.Handler       { return h }

func (h *capturingHandler) containsValue(needle string) bool {
	for _, r := range h.records {
		r.Attrs(func(a slog.Attr) bool {
			return true
		})
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if strings.Contains(a.Value.String(), needle) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
		if strings.Contains(r.Message, needle) {
			return true
		}
	}
	return false
}

func TestPreloadArtifacts_DirectoryTraversal(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	loc := artifact.Locator{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
		Filename:  "../../etc/passwd",
	}
	_, err := store.Save(ctx, loc, []byte("secret content"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}

	scopedSvc := &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test-app",
	}

	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	inputDir := t.TempDir()

	params := &remotetool.InvokeParams{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_file": {Filename: "../../etc/passwd", Version: 0},
		},
	}

	paths, _, invokeErr := runner.preloadArtifacts(ctx, params, scopedSvc, inputDir)
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr.Message)
	}

	gotPath := paths["input_file"]
	expectedPath := filepath.Join(inputDir, "passwd")
	if gotPath != expectedPath {
		t.Errorf("path = %q, want %q", gotPath, expectedPath)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("file not found at safe path: %v", err)
	}
	if string(data) != "secret content" {
		t.Errorf("content = %q, want %q", string(data), "secret content")
	}

	unsafePath := filepath.Join(inputDir, "..", "..", "etc", "passwd")
	if _, err := os.Stat(unsafePath); err == nil {
		t.Error("file was written outside inputDir via directory traversal")
	}
}

func TestPreloadArtifacts_AbsolutePathSanitized(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	loc := artifact.Locator{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
		Filename:  "/etc/passwd",
	}
	_, err := store.Save(ctx, loc, []byte("data"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}

	scopedSvc := &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test-app",
	}

	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	inputDir := t.TempDir()

	params := &remotetool.InvokeParams{
		AppName:   "test-app",
		UserID:    "user1",
		SessionID: "sess1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_file": {Filename: "/etc/passwd", Version: 0},
		},
	}

	paths, _, invokeErr := runner.preloadArtifacts(ctx, params, scopedSvc, inputDir)
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr.Message)
	}

	gotPath := paths["input_file"]
	expectedPath := filepath.Join(inputDir, "passwd")
	if gotPath != expectedPath {
		t.Errorf("path = %q, want %q", gotPath, expectedPath)
	}
}

func TestPreloadArtifacts_PreloadedArtifactWritesInline(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	inputDir := t.TempDir()
	payload := []byte("the inline bytes")
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename":  "deck.pptx",
				"content":   base64.StdEncoding.EncodeToString(payload),
				"mime_type": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			},
		},
	}

	paths, meta, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, inputDir)
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr.Message)
	}

	gotPath := paths["input_document"]
	wantPath := filepath.Join(inputDir, "deck.pptx")
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	gotBytes, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("read inline artifact: %v", err)
	}
	if string(gotBytes) != string(payload) {
		t.Errorf("file bytes = %q, want %q", gotBytes, payload)
	}
	if got := meta["input_document"]["mime_type"]; got != "application/vnd.openxmlformats-officedocument.presentationml.presentation" {
		t.Errorf("mime_type = %v, want pptx mime", got)
	}
	if got := meta["input_document"]["filename"]; got != "deck.pptx" {
		t.Errorf("filename = %v, want deck.pptx", got)
	}
}

func TestPreloadArtifacts_PreloadedArtifactDirectoryTraversal(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	inputDir := t.TempDir()
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "../../etc/passwd",
				"content":  base64.StdEncoding.EncodeToString([]byte("evil")),
			},
		},
	}

	paths, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, inputDir)
	if invokeErr != nil {
		t.Fatalf("unexpected invoke error: %v", invokeErr.Message)
	}
	if got, want := paths["input_document"], filepath.Join(inputDir, "passwd"); got != want {
		t.Errorf("path = %q, want %q (sanitized inside inputDir)", got, want)
	}
}

func TestPreloadArtifacts_PreloadedArtifactInvalidBase64(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "deck.pptx",
				"content":  "not-valid-base64!!!",
			},
		},
	}

	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, t.TempDir())
	if invokeErr == nil {
		t.Fatal("expected invoke error for invalid base64")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %v, want %v", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
}

func TestPreloadArtifacts_PreloadedArtifactCollidesWithReference(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "deck.pptx"}
	if _, err := store.Save(ctx, loc, []byte("ref bytes"), "application/x-foo"); err != nil {
		t.Fatal(err)
	}
	scoped := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "app"}
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())

	params := &remotetool.InvokeParams{
		AppName:   "app",
		UserID:    "u1",
		SessionID: "s1",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"input_document": {Filename: "deck.pptx", Version: 0},
		},
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "other.pptx",
				"content":  base64.StdEncoding.EncodeToString([]byte("inline")),
			},
		},
	}

	_, _, invokeErr := runner.preloadArtifacts(ctx, params, scoped, t.TempDir())
	if invokeErr == nil {
		t.Fatal("expected invoke error for colliding param name")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %v, want %v", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
}

func TestPreloadArtifacts_PreloadedArtifactNotAnObject(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": "not-a-map",
		},
	}

	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, t.TempDir())
	if invokeErr == nil {
		t.Fatal("expected invoke error for non-map value")
	}
	if invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Errorf("code = %v, want %v", invokeErr.Code, remotetool.ErrInvalidRequest)
	}
}

func TestPreloadArtifacts_PreloadedArtifactMissingFilename(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"content": base64.StdEncoding.EncodeToString([]byte("x")),
			},
		},
	}
	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, t.TempDir())
	if invokeErr == nil || invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Fatalf("expected ErrInvalidRequest, got %+v", invokeErr)
	}
	if !strings.Contains(invokeErr.Message, "filename is required") {
		t.Errorf("message = %q, want a filename-required mention", invokeErr.Message)
	}
}

func TestPreloadArtifacts_PreloadedArtifactMissingContent(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "deck.pptx",
			},
		},
	}
	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, t.TempDir())
	if invokeErr == nil || invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Fatalf("expected ErrInvalidRequest, got %+v", invokeErr)
	}
	if !strings.Contains(invokeErr.Message, "content is required") {
		t.Errorf("message = %q, want a content-required mention", invokeErr.Message)
	}
}

func TestPreloadArtifacts_PreloadedArtifactWriteFails(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())

	dir := t.TempDir()
	notADir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notADir, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}

	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "deck.pptx",
				"content":  base64.StdEncoding.EncodeToString([]byte("x")),
			},
		},
	}
	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, notADir)
	if invokeErr == nil {
		t.Fatal("expected invoke error when inputDir is not a directory")
	}
	if invokeErr.Code != remotetool.ErrInternalError {
		t.Errorf("code = %v, want %v", invokeErr.Code, remotetool.ErrInternalError)
	}
}

func TestPreloadArtifacts_ArtifactReferenceWriteFails(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "app", UserID: "u", SessionID: "s", Filename: "f.bin"}
	if _, err := store.Save(ctx, loc, []byte("bytes"), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	scoped := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "app"}

	dir := t.TempDir()
	notADir := filepath.Join(dir, "blocker")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		AppName: "app", UserID: "u", SessionID: "s",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"in": {Filename: "f.bin", Version: 0},
		},
	}
	_, _, invokeErr := runner.preloadArtifacts(ctx, params, scoped, notADir)
	if invokeErr == nil {
		t.Fatal("expected write error from ArtifactReferences path")
	}
	if invokeErr.Code != remotetool.ErrArtifactError {
		t.Errorf("code = %v, want %v", invokeErr.Code, remotetool.ErrArtifactError)
	}
}

func TestPreloadArtifacts_ArtifactReferenceToUnsavedFileIsNotFound(t *testing.T) {
	scoped := &artifact.ScopedService{Inner: memstore.New(), Scope: "namespace", Namespace: "app"}
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		AppName: "app", UserID: "u", SessionID: "s",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"in": {Filename: "report.html", Version: -1},
		},
	}

	_, _, invokeErr := runner.preloadArtifacts(t.Context(), params, scoped, t.TempDir())

	want := &remotetool.InvokeError{Code: "ARTIFACT_NOT_FOUND", Message: `latest version of "report.html" not found in this session`}
	if invokeErr == nil || !strings.HasPrefix(invokeErr.Site, "internal/str/sandbox.go:") {
		t.Fatalf("invokeErr = %+v, want a site in internal/str/sandbox.go", invokeErr)
	}
	got := *invokeErr
	got.Site = ""
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("invokeErr = %+v, want %+v", got, want)
	}
}

type loadFailingStore struct {
	artifact.Service
}

func (loadFailingStore) Load(context.Context, artifact.Locator, *int) ([]byte, string, error) {
	return nil, "", errors.New("store unavailable")
}

func TestPreloadArtifacts_ArtifactReferenceLoadFailureIsArtifactError(t *testing.T) {
	scoped := &artifact.ScopedService{Inner: loadFailingStore{}, Scope: "namespace", Namespace: "app"}
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		AppName: "app", UserID: "u", SessionID: "s",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"in": {Filename: "report.html", Version: -1},
		},
	}

	_, _, invokeErr := runner.preloadArtifacts(t.Context(), params, scoped, t.TempDir())

	want := &remotetool.InvokeError{Code: "ARTIFACT_ERROR", Message: `loading artifact "report.html": store unavailable`}
	if invokeErr == nil || !strings.HasPrefix(invokeErr.Site, "internal/str/sandbox.go:") {
		t.Fatalf("invokeErr = %+v, want a site in internal/str/sandbox.go", invokeErr)
	}
	got := *invokeErr
	got.Site = ""
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("invokeErr = %+v, want %+v", got, want)
	}
}

func TestPreloadArtifacts_ArtifactReferenceEmptyMIMETypeFallsBackToGuess(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "app", UserID: "u", SessionID: "s", Filename: "deck.pptx"}
	if _, err := store.Save(ctx, loc, []byte("bytes"), ""); err != nil {
		t.Fatal(err)
	}
	scoped := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "app"}

	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		AppName: "app", UserID: "u", SessionID: "s",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"in": {Filename: "deck.pptx", Version: 0},
		},
	}
	_, meta, invokeErr := runner.preloadArtifacts(ctx, params, scoped, t.TempDir())
	if invokeErr != nil {
		t.Fatalf("unexpected error: %+v", invokeErr)
	}
	gotMIME, _ := meta["in"]["mime_type"].(string)
	if gotMIME == "" || gotMIME == "application/octet-stream" {
		t.Errorf("mime_type = %q, expected guess based on .pptx extension", gotMIME)
	}
}

func TestPreloadArtifacts_PreloadedArtifactRejectsDotDotFilename(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	params := &remotetool.InvokeParams{
		PreloadedArtifacts: map[string]any{
			"input_document": map[string]any{
				"filename": "..",
				"content":  base64.StdEncoding.EncodeToString([]byte("x")),
			},
		},
	}
	_, _, invokeErr := runner.preloadArtifacts(context.Background(), params, nil, t.TempDir())
	if invokeErr == nil || invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Fatalf("expected ErrInvalidRequest for filename='..', got %+v", invokeErr)
	}
}

func TestPreloadArtifacts_OnDiskPathCollisionRejected(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "app", UserID: "u", SessionID: "s", Filename: "deck.pptx"}
	if _, err := store.Save(ctx, loc, []byte("ref bytes"), "application/x-foo"); err != nil {
		t.Fatal(err)
	}
	scoped := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "app"}

	params := &remotetool.InvokeParams{
		AppName: "app", UserID: "u", SessionID: "s",
		ArtifactReferences: map[string]remotetool.ArtifactReference{
			"first": {Filename: "deck.pptx", Version: 0},
		},
		PreloadedArtifacts: map[string]any{
			"second": map[string]any{
				"filename": "deck.pptx",
				"content":  base64.StdEncoding.EncodeToString([]byte("inline")),
			},
		},
	}

	_, _, invokeErr := NewSandboxRunner(SandboxConfig{}, slog.Default()).
		preloadArtifacts(ctx, params, scoped, t.TempDir())
	if invokeErr == nil || invokeErr.Code != remotetool.ErrInvalidRequest {
		t.Fatalf("expected ErrInvalidRequest for path collision, got %+v", invokeErr)
	}
}

func TestSanitizeInputFilename(t *testing.T) {
	t.Parallel()
	good := []string{"deck.pptx", "../../etc/passwd", "/etc/passwd"}
	for _, in := range good {
		if _, err := sanitizeInputFilename(in); err != nil {
			t.Errorf("sanitizeInputFilename(%q) error = %v, want nil (Base resolves safely)", in, err)
		}
	}
	bad := []string{"", ".", "..", "/", "leading\x00null"}
	for _, in := range bad {
		if _, err := sanitizeInputFilename(in); err == nil {
			t.Errorf("sanitizeInputFilename(%q) error = nil, want non-nil", in)
		}
	}
}

func TestSandbox_StderrLogRedacted(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	secret := "abc123secret"
	script := writeScript(t, dir, "bearer.sh",
		`echo "Authorization: Bearer `+secret+`" >&2; exit 1`)

	handler := &capturingHandler{}
	logger := slog.New(handler)

	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   10,
		MaxConcurrentExecutions: 4,
	}, logger)

	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})
	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}

	if handler.containsValue(secret) {
		t.Errorf("raw secret %q found in structured log output", secret)
	}
}

func TestBuildCommand_PrlimitOnlyMode(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:           "prlimit-only",
			PrlimitBin:     "/usr/bin/prlimit",
			BwrapBin:       "/usr/bin/bwrap",
			DefaultProfile: "standard",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:   "mytool",
		Executable: "/tmp/mytool",
	}

	workDir := t.TempDir()
	cmd, err := runner.buildCommand(context.Background(), entry, []string{"/tmp/args.json"}, workDir, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	for _, arg := range cmd.Args {
		if arg == "/usr/bin/bwrap" {
			t.Errorf("prlimit-only mode must not invoke bwrap; args: %v", cmd.Args)
		}
	}
}

func TestBuildCommand_PrlimitOnlyMode_WithLimits(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:           "prlimit-only",
			PrlimitBin:     "/usr/bin/prlimit",
			BwrapBin:       "/usr/bin/bwrap",
			DefaultProfile: "standard",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:   "limitedtool",
		Executable: "/tmp/limitedtool",
	}

	workDir := t.TempDir()
	cmd, err := runner.buildCommand(context.Background(), entry, []string{"/tmp/args.json"}, workDir, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	if cmd.Path != "/usr/bin/prlimit" {
		t.Errorf("expected prlimit as command, got %q", cmd.Path)
	}

	foundSeparator := false
	for _, arg := range cmd.Args {
		if arg == "--" {
			foundSeparator = true
			break
		}
	}
	if !foundSeparator {
		t.Errorf("expected '--' separator in prlimit args: %v", cmd.Args)
	}
}

func TestBuildCommand_DirectMode(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "direct",
			PrlimitBin: "/usr/bin/prlimit",
			BwrapBin:   "/usr/bin/bwrap",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:   "directtool",
		Executable: "/tmp/directtool",
	}

	workDir := t.TempDir()
	cmd, err := runner.buildCommand(context.Background(), entry, []string{"/tmp/args.json"}, workDir, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	if cmd.Path == "/usr/bin/bwrap" || cmd.Path == "/usr/bin/prlimit" {
		t.Errorf("direct mode must not use bwrap or prlimit; path=%q args=%v", cmd.Path, cmd.Args)
	}
}

func TestExtractDeclaredMeta(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]declaredArtifactMeta
	}{
		{
			name: "nil result",
			in:   nil,
			want: nil,
		},
		{
			name: "no data_objects key",
			in:   map[string]any{"status": "success"},
			want: nil,
		},
		{
			name: "empty data_objects",
			in:   map[string]any{"data_objects": []any{}},
			want: nil,
		},
		{
			name: "mime + description per entry",
			in: map[string]any{
				"data_objects": []any{
					map[string]any{
						"name":        "tree.json",
						"mime_type":   "application/json",
						"description": "Org chart tree",
					},
					map[string]any{
						"name":      "tree.md",
						"mime_type": "text/markdown",
					},
				},
			},
			want: map[string]declaredArtifactMeta{
				"tree.json": {MIMEType: "application/json", Description: "Org chart tree"},
				"tree.md":   {MIMEType: "text/markdown"},
			},
		},
		{
			name: "entries without name are skipped",
			in: map[string]any{
				"data_objects": []any{
					map[string]any{"mime_type": "text/plain"},
					map[string]any{"name": "valid.txt", "mime_type": "text/plain"},
				},
			},
			want: map[string]declaredArtifactMeta{
				"valid.txt": {MIMEType: "text/plain"},
			},
		},
		{
			name: "non-map entries are skipped",
			in: map[string]any{
				"data_objects": []any{
					"not a map",
					map[string]any{"name": "ok.txt", "mime_type": "text/plain"},
				},
			},
			want: map[string]declaredArtifactMeta{
				"ok.txt": {MIMEType: "text/plain"},
			},
		},
		{
			name: "tags are carried and non-string entries skipped",
			in: map[string]any{
				"data_objects": []any{
					map[string]any{
						"name":      "deck-spec.yaml",
						"mime_type": "application/yaml",
						"tags":      []any{"__working", 42, ""},
					},
				},
			},
			want: map[string]declaredArtifactMeta{
				"deck-spec.yaml": {MIMEType: "application/yaml", Tags: []string{"__working"}},
			},
		},
		{
			name: "later entry merges into earlier same-name entry",
			in: map[string]any{
				"data_objects": []any{
					map[string]any{"name": "x", "mime_type": "text/plain"},
					map[string]any{"name": "x", "description": "added later"},
				},
			},
			want: map[string]declaredArtifactMeta{
				"x": {MIMEType: "text/plain", Description: "added later"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDeclaredMeta(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%#v)", len(got), len(tc.want), got)
			}
			for k, want := range tc.want {
				if !reflect.DeepEqual(got[k], want) {
					t.Errorf("entry %q = %+v, want %+v", k, got[k], want)
				}
			}
		})
	}
}

func TestCollectOutputArtifacts_PersistsTags(t *testing.T) {
	store := memstore.New()
	scoped := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-app"}
	runner := NewSandboxRunner(SandboxConfig{}, slog.Default())
	ctx := context.Background()

	outputDir := t.TempDir()
	specName := "deck-spec.yaml"
	if err := os.WriteFile(filepath.Join(outputDir, specName), []byte("- layout: title"), 0o600); err != nil {
		t.Fatal(err)
	}

	params := &remotetool.InvokeParams{AppName: "test-app", UserID: "user1", SessionID: "sess1"}
	declaredMeta := map[string]declaredArtifactMeta{
		specName: {MIMEType: "application/yaml", Tags: []string{a2a.ArtifactTagWorking}},
	}

	created, failed, err := runner.collectOutputArtifacts(ctx, params, scoped, outputDir, declaredMeta, slog.Default())
	if err != nil {
		t.Fatalf("collectOutputArtifacts: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("unexpected failures: %+v", failed)
	}
	if len(created) != 1 {
		t.Fatalf("created = %d, want 1", len(created))
	}

	meta := artifact.LoadCompanionMetadata(ctx, scoped, params.AppName, params.UserID, params.SessionID, specName, nil)
	if meta == nil {
		t.Fatal("companion metadata not found")
	}
	tags, ok := meta["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("companion tags = %v, want [%s]", meta["tags"], a2a.ArtifactTagWorking)
	}
}

func TestSDKTagWorkingMatchesA2A(t *testing.T) {
	if sdk.TagWorking != a2a.ArtifactTagWorking {
		t.Errorf("samtoolsdk.TagWorking = %q, want a2a.ArtifactTagWorking = %q", sdk.TagWorking, a2a.ArtifactTagWorking)
	}
}
