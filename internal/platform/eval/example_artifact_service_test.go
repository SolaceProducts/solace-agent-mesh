// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type exampleArtifactFixture struct {
	ctx      context.Context
	db       *store.DB
	svc      *ExampleArtifactService
	examples *store.EvalExampleRepo
	datasets *store.EvalDatasetRepo
	runs     *store.EvalRunRepo
	scoped   *artifact.ScopedService
	files    *FileDeletes
	saved    *savedSessions
	dataset  *store.EvalDataset
	example  *store.EvalExample
}

type savedSessions struct {
	artifact.Service
	sessions []string
}

func (s *savedSessions) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	if !slices.Contains(s.sessions, loc.SessionID) {
		s.sessions = append(s.sessions, loc.SessionID)
	}
	return s.Service.Save(ctx, loc, content, mimeType)
}

type saveFailingStore struct {
	artifact.Service
}

func (saveFailingStore) Save(context.Context, artifact.Locator, []byte, string) (int, error) {
	return 0, errors.New("artifact store unavailable")
}

func newExampleArtifactFixture(t *testing.T, cfg ExampleArtifactConfig) *exampleArtifactFixture {
	t.Helper()
	db := store.TestDB(t)
	ctx := t.Context()

	datasets := store.NewEvalDatasetRepo(db)
	examples := store.NewEvalExampleRepo(db)
	runs := store.NewEvalRunRepo(db)

	ds := &store.EvalDataset{Name: "ds", CreatedBy: "u", UpdatedBy: "u"}
	if err := datasets.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "review this"}
	if err := examples.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}

	saved := &savedSessions{Service: memory.New()}
	scoped := &artifact.ScopedService{Inner: saved, Scope: "namespace", Namespace: "ns"}
	rows := store.NewEvalExampleArtifactRepo(db)
	files := NewFileDeletes(nil, scoped, rows)
	return &exampleArtifactFixture{
		ctx: ctx,
		db:  db,
		svc: NewExampleArtifactService(ExampleArtifactServiceParams{
			Examples:     examples,
			ArtifactRows: rows,
			Artifacts:    scoped,
			FileDeletes:  files,
			Config:       cfg,
		}),
		files:    files,
		saved:    saved,
		examples: examples,
		datasets: datasets,
		runs:     runs,
		scoped:   scoped,
		dataset:  ds,
		example:  ex,
	}
}

func (f *exampleArtifactFixture) newExample(t *testing.T, seq int) *store.EvalExample {
	t.Helper()
	ex := &store.EvalExample{DatasetID: f.dataset.ID, SequenceNumber: seq, Prompt: "p"}
	if err := f.examples.Create(f.ctx, ex); err != nil {
		t.Fatalf("create example seq=%d: %v", seq, err)
	}
	return ex
}

func (f *exampleArtifactFixture) reload(t *testing.T, exampleID string) *store.EvalExample {
	t.Helper()
	row, err := f.examples.GetByID(f.ctx, exampleID)
	if err != nil {
		t.Fatalf("reload example: %v", err)
	}
	return row
}

func (f *exampleArtifactFixture) session(t *testing.T, exampleID, role string) string {
	t.Helper()
	row := f.reload(t, exampleID).ArtifactByRole(role)
	if row == nil {
		t.Fatalf("example %s has no %s file", exampleID, role)
	}
	return row.SessionID
}

func (f *exampleArtifactFixture) versions(t *testing.T, exampleID, role, filename string) []int {
	t.Helper()
	loc := f.scoped.Resolve("", EvalRunnerUserID, f.session(t, exampleID, role), filename)
	versions, err := f.scoped.Inner.ListVersions(f.ctx, loc)
	if err != nil {
		t.Fatalf("list versions of %q: %v", filename, err)
	}
	return versions
}

func (f *exampleArtifactFixture) keys(t *testing.T, exampleID, role string) []string {
	t.Helper()
	runDueFileDeletes(t, f.db, f.files)
	var keys []string
	for _, session := range f.saved.sessions {
		if !strings.HasPrefix(session, "eval-example-"+exampleID+"-"+role+"-") {
			continue
		}
		got, err := f.scoped.Inner.ListKeys(f.ctx, artifact.KeyScope{AppName: "ns", UserID: EvalRunnerUserID, SessionID: session})
		if err != nil {
			t.Fatalf("list keys: %v", err)
		}
		keys = append(keys, got...)
	}
	slices.Sort(keys)
	return keys
}

func TestExampleArtifactService_Set(t *testing.T) {
	t.Run("stores the bytes and every column", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

		resp, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", "application/octet-stream",
			[]byte("name: sam\n"), "the config under review")
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if resp.Filename != "config.yaml" {
			t.Errorf("filename = %q, want config.yaml", resp.Filename)
		}
		if resp.Version != 0 {
			t.Errorf("version = %d, want 0 for a first upload", resp.Version)
		}
		if resp.Size != 10 {
			t.Errorf("size = %d, want 10", resp.Size)
		}
		if resp.MimeType == "application/octet-stream" {
			t.Errorf("mimeType = %q, want a type derived from the filename", resp.MimeType)
		}
		if resp.ContentHash != contentHash([]byte("name: sam\n")) {
			t.Errorf("contentHash = %q, want the sha256 of the content", resp.ContentHash)
		}

		example := f.reload(t, f.example.ID)
		row := example.ArtifactByRole(store.EvalArtifactRoleInput)
		if row == nil || row.Filename != "config.yaml" {
			t.Fatalf("input artifact row = %+v, want config.yaml", row)
		}
		if row.Version != 0 {
			t.Errorf("version = %d, want 0", row.Version)
		}
		if !strings.HasPrefix(row.SessionID, "eval-example-"+f.example.ID+"-"+store.EvalArtifactRoleInput+"-") {
			t.Errorf("artifact_session_id = %q, want a per-upload location under the example and role", row.SessionID)
		}
		if row.ContentHash != resp.ContentHash {
			t.Errorf("content_hash = %q, want %q", row.ContentHash, resp.ContentHash)
		}
		if example.Prompt != "review this" {
			t.Errorf("prompt = %q, want it untouched by the upload", example.Prompt)
		}
	})

	t.Run("a byte-identical re-upload does not save a second version", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		content := []byte("same bytes")

		first, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", content, "")
		if err != nil {
			t.Fatalf("first set: %v", err)
		}
		second, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", content, "")
		if err != nil {
			t.Fatalf("second set: %v", err)
		}
		if second.Version != first.Version {
			t.Errorf("version = %d after an identical re-upload, want %d", second.Version, first.Version)
		}
		if got := f.versions(t, f.example.ID, store.EvalArtifactRoleInput, "a.txt"); len(got) != 1 {
			t.Errorf("stored versions = %v, want exactly one — a retry must not inflate the version space", got)
		}
	})

	t.Run("replacing the content leaves exactly one stored version", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("old"), ""); err != nil {
			t.Fatalf("first set: %v", err)
		}
		replaced, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("new"), "")
		if err != nil {
			t.Fatalf("replace: %v", err)
		}
		if replaced.Version != 0 {
			t.Errorf("version = %d after a replace, want 0", replaced.Version)
		}
		if got := f.versions(t, f.example.ID, store.EvalArtifactRoleInput, "a.txt"); !slices.Equal(got, []int{0}) {
			t.Errorf("stored versions = %v, want [0] — the old bytes must be destroyed", got)
		}
		content, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", nil)
		if err != nil {
			t.Fatalf("get after replace: %v", err)
		}
		if string(content) != "new" {
			t.Errorf("content = %q, want %q", content, "new")
		}
	})

	t.Run("a different filename replaces the slot rather than adding a second", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "old.txt", "text/plain", []byte("old"), ""); err != nil {
			t.Fatalf("first set: %v", err)
		}
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "new.txt", "text/plain", []byte("new"), ""); err != nil {
			t.Fatalf("second set: %v", err)
		}

		row := f.reload(t, f.example.ID).ArtifactByRole(store.EvalArtifactRoleInput)
		if row == nil || row.Filename != "new.txt" {
			t.Errorf("input artifact row = %+v, want new.txt", row)
		}
		if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); !slices.Equal(got, []string{"new.txt", "new.txt" + artifact.MetadataSuffix}) {
			t.Errorf("stored keys = %v, want only new.txt and its companion", got)
		}
		if _, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "old.txt", nil); err == nil {
			t.Error("get(old.txt) succeeded, want not-found once the slot moved on")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("rejects unsafe and reserved filenames", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

		for _, filename := range []string{
			"../escape.txt",
			"nested/dir.txt",
			"",
			"report.metadata.json",
			"report.converted.txt",
			artifact.BM25IndexFilename,
			"user:data.csv",
		} {
			_, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, filename, "text/plain", []byte("x"), "")
			if err == nil {
				t.Errorf("set(%q) succeeded, want rejection", filename)
				continue
			}
			ve := asValidationError(t, err)
			if ve.Field != "file" {
				t.Errorf("set(%q) field = %q, want file", filename, ve.Field)
			}
		}
	})

	t.Run("enforces the per-file cap", func(t *testing.T) {
		cfg := DefaultExampleArtifactConfig()
		cfg.MaxBytes = 8
		f := newExampleArtifactFixture(t, cfg)

		_, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "big.txt", "text/plain", []byte("123456789"), "")
		if err == nil {
			t.Fatal("set succeeded, want the per-file cap to reject it")
		}
		if code := asBusinessRuleErr(t, err).Code; code != "EXAMPLE_ARTIFACT_TOO_LARGE" {
			t.Errorf("code = %q, want EXAMPLE_ARTIFACT_TOO_LARGE", code)
		}
		if got := f.reload(t, f.example.ID).ArtifactByRole(store.EvalArtifactRoleInput); got != nil {
			t.Errorf("artifact row = %+v, want none after a rejected upload", got)
		}
	})

	t.Run("enforces the dataset total cap", func(t *testing.T) {
		cfg := DefaultExampleArtifactConfig()
		cfg.MaxTotalBytesPerDataset = 20
		f := newExampleArtifactFixture(t, cfg)
		other := f.newExample(t, 2)

		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("0123456789"), ""); err != nil {
			t.Fatalf("first upload: %v", err)
		}
		_, err := f.svc.Set(f.ctx, other.ID, store.EvalArtifactRoleInput, "b.txt", "text/plain", []byte("0123456789+"), "")
		if err == nil {
			t.Fatal("second upload succeeded, want the dataset cap to reject it")
		}
		if code := asBusinessRuleErr(t, err).Code; code != "DATASET_ARTIFACT_TOTAL_BYTES_CAP_REACHED" {
			t.Errorf("code = %q, want DATASET_ARTIFACT_TOTAL_BYTES_CAP_REACHED", code)
		}
	})

	t.Run("the dataset total credits the bytes being replaced", func(t *testing.T) {
		cfg := DefaultExampleArtifactConfig()
		cfg.MaxTotalBytesPerDataset = 10
		f := newExampleArtifactFixture(t, cfg)

		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("0123456789"), ""); err != nil {
			t.Fatalf("first upload: %v", err)
		}
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("9876543210"), ""); err != nil {
			t.Fatalf("replace in a full dataset: %v", err)
		}
	})

	t.Run("honours the MIME allowlist", func(t *testing.T) {
		cfg := DefaultExampleArtifactConfig()
		cfg.AllowedMIMETypes = []string{"text/plain"}
		f := newExampleArtifactFixture(t, cfg)

		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "notes.txt", "text/plain", []byte("hello"), ""); err != nil {
			t.Fatalf("allowed type rejected: %v", err)
		}
		_, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "data.csv", "text/csv", []byte("a,b\n1,2\n"), "")
		if err == nil {
			t.Fatal("set succeeded, want the allowlist to reject text/csv")
		}
		if field := asValidationError(t, err).Field; field != "file" {
			t.Errorf("field = %q, want file", field)
		}
	})

	t.Run("unknown example", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		_, err := f.svc.Set(f.ctx, "00000000-0000-0000-0000-000000000096", store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("x"), "")
		if err == nil {
			t.Fatal("set succeeded, want not-found")
		}
		_ = asNotFoundError(t, err)
	})
}

func TestExampleArtifactService_Get(t *testing.T) {
	t.Run("pins the version recorded on the row", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("v0"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}
		loc := f.scoped.Resolve("", EvalRunnerUserID, f.session(t, f.example.ID, store.EvalArtifactRoleInput), "a.txt")
		if _, err := f.scoped.Inner.Save(f.ctx, loc, []byte("v1"), "text/plain"); err != nil {
			t.Fatalf("seed a later version: %v", err)
		}

		content, mimeType, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", nil)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(content) != "v0" {
			t.Errorf("content = %q, want the recorded version's bytes %q", content, "v0")
		}
		if mimeType != "text/plain" {
			t.Errorf("mimeType = %q, want text/plain", mimeType)
		}
	})

	t.Run("an explicit version must match the recorded one", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("v0"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}

		loc := f.scoped.Resolve("", EvalRunnerUserID, f.session(t, f.example.ID, store.EvalArtifactRoleInput), "a.txt")
		if _, err := f.scoped.Inner.Save(f.ctx, loc, []byte("v1"), "text/plain"); err != nil {
			t.Fatalf("seed a later version: %v", err)
		}

		zero, one := 0, 1
		if _, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", &zero); err != nil {
			t.Errorf("get(version=0): %v", err)
		}
		if _, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", &one); err == nil {
			t.Error("get(version=1) succeeded, want not-found for a version the row does not record")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("an example with no file", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", nil); err == nil {
			t.Fatal("get succeeded, want not-found")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("unknown example", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, _, err := f.svc.Get(f.ctx, "00000000-0000-0000-0000-000000000096", store.EvalArtifactRoleInput, "a.txt", nil); err == nil {
			t.Fatal("get succeeded, want not-found")
		} else {
			_ = asNotFoundError(t, err)
		}
	})
}

func TestExampleArtifactService_Clear(t *testing.T) {
	t.Run("destroys the bytes and nulls the columns", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}

		if err := f.svc.Clear(f.ctx, f.example.ID, store.EvalArtifactRoleInput); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); len(got) != 0 {
			t.Errorf("stored keys = %v, want none — clearing destroys the bytes", got)
		}
		if row := f.reload(t, f.example.ID).ArtifactByRole(store.EvalArtifactRoleInput); row != nil {
			t.Errorf("artifact row = %+v, want none after clear", row)
		}
	})

	t.Run("is a no-op for an example with no file", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if err := f.svc.Clear(f.ctx, f.example.ID, store.EvalArtifactRoleInput); err != nil {
			t.Fatalf("clear: %v", err)
		}
	})

	t.Run("unknown example", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if err := f.svc.Clear(f.ctx, "00000000-0000-0000-0000-000000000096", store.EvalArtifactRoleInput); err == nil {
			t.Fatal("clear succeeded, want not-found")
		} else {
			_ = asNotFoundError(t, err)
		}
	})
}

func TestExampleArtifactService_NoArtifactService(t *testing.T) {
	db := store.TestDB(t)
	svc := NewExampleArtifactService(ExampleArtifactServiceParams{
		Examples: store.NewEvalExampleRepo(db),
		Config:   DefaultExampleArtifactConfig(),
	})
	ctx := t.Context()
	const anyID = "00000000-0000-0000-0000-000000000096"

	_, setErr := svc.Set(ctx, anyID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("x"), "")
	_, _, getErr := svc.Get(ctx, anyID, store.EvalArtifactRoleInput, "a.txt", nil)
	clearErr := svc.Clear(ctx, anyID, store.EvalArtifactRoleInput)

	for name, err := range map[string]error{"set": setErr, "get": getErr, "clear": clearErr} {
		if err == nil {
			t.Errorf("%s succeeded, want ARTIFACT_SERVICE_UNAVAILABLE", name)
			continue
		}
		if code := asBusinessRuleErr(t, err).Code; code != "ARTIFACT_SERVICE_UNAVAILABLE" {
			t.Errorf("%s code = %q, want ARTIFACT_SERVICE_UNAVAILABLE", name, code)
		}
	}

}

func TestNewExampleArtifactService_WarnsOnNonNamespaceScope(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	NewExampleArtifactService(ExampleArtifactServiceParams{
		Artifacts: &artifact.ScopedService{Inner: memory.New(), Scope: "agent"},
		Logger:    logger,
	})
	if !strings.Contains(buf.String(), "namespace-scoped artifact service") {
		t.Errorf("log = %q, want a warning about the artifact scope", buf.String())
	}

	buf.Reset()
	NewExampleArtifactService(ExampleArtifactServiceParams{
		Artifacts: &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"},
		Logger:    logger,
	})
	if buf.Len() != 0 {
		t.Errorf("log = %q, want silence under namespace scope", buf.String())
	}
}

func TestExampleArtifactCleanup_OnRowDeletion(t *testing.T) {
	t.Run("deleting the example", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}

		examples := NewExampleService(f.examples, f.datasets, nil).WithFileDeletes(f.files)
		if err := examples.Delete(f.ctx, f.example.ID); err != nil {
			t.Fatalf("delete example: %v", err)
		}
		if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); len(got) != 0 {
			t.Errorf("stored keys = %v, want none after the example is deleted", got)
		}
	})

	t.Run("deleting the dataset", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}

		datasets := NewDatasetService(f.datasets, f.examples, f.runs, nil).WithFileDeletes(f.files)
		if err := datasets.Delete(f.ctx, f.dataset.ID, "u"); err != nil {
			t.Fatalf("delete dataset: %v", err)
		}
		if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); len(got) != 0 {
			t.Errorf("stored keys = %v, want none after the dataset cascade", got)
		}
	})

	t.Run("reconciling the sequence away", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("bytes"), ""); err != nil {
			t.Fatalf("set: %v", err)
		}

		datasets := NewDatasetService(f.datasets, f.examples, f.runs, nil).WithFileDeletes(f.files)
		res, err := datasets.ReconcileExamples(f.ctx, f.dataset.ID,
			[]DesiredExample{{SequenceNumber: 2, Prompt: "kept"}}, "u")
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if res.Deletes != 1 {
			t.Fatalf("deletes = %d, want 1", res.Deletes)
		}
		if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); len(got) != 0 {
			t.Errorf("stored keys = %v, want none after the row is reconciled away", got)
		}
	})
}

func TestSetKeepsTheFileWhenTheReplacementCannotBeSaved(t *testing.T) {
	f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

	if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", "application/x-yaml",
		[]byte("name: sam\n"), ""); err != nil {
		t.Fatalf("first set: %v", err)
	}

	f.scoped.Inner = saveFailingStore{Service: f.scoped.Inner}
	if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", "application/x-yaml",
		[]byte("name: replaced\n"), ""); err == nil {
		t.Fatal("set = nil error, want the store failure surfaced")
	}

	content, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", nil)
	if err != nil || string(content) != "name: sam\n" {
		t.Errorf("after a failed replace get = %q, %v; want the original file", content, err)
	}
}

func TestSetRecordsItsUploadForDeletionUnlessItsRowExists(t *testing.T) {
	f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
	if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "a.txt", "text/plain", []byte("kept"), ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	events := uploadEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("upload events = %d, want 1", len(events))
	}
	if events[0].NextAttemptAt <= 0 {
		t.Error("upload event is due at once, want it delayed until the upload has saved its row")
	}
	if err := f.files.PublishOutboxEvent(f.ctx, events[0]); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := f.keys(t, f.example.ID, store.EvalArtifactRoleInput); len(got) == 0 {
		t.Fatal("the upload's own event deleted a file its row names")
	}

	orphan := store.EvalExampleFile{SessionID: evalExampleArtifactSessionID(f.example.ID, store.EvalArtifactRoleInput, "orphan"), Filename: "b.txt"}
	if _, err := f.scoped.Inner.Save(f.ctx, f.scoped.Resolve("", EvalRunnerUserID, orphan.SessionID, orphan.Filename), []byte("never named"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	event, err := uploadEvent(orphan)(store.EvalFileDeletes{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.files.PublishOutboxEvent(f.ctx, *event); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, key := range f.keys(t, f.example.ID, store.EvalArtifactRoleInput) {
		if strings.HasPrefix(key, "b.txt") {
			t.Errorf("an upload no row names was kept: %s", key)
		}
	}
}

func uploadEvents(t *testing.T, f *exampleArtifactFixture) []store.OutboxEvent {
	t.Helper()
	events, err := dbutil.QueryRows[store.OutboxEvent](f.ctx, f.db,
		`SELECT id, event_type, aggregate_id, payload, created_at, published, published_at, retry_count, last_error, next_attempt_at, dead
		 FROM outbox_events WHERE event_type = ?`, eventEvalFilesDelete)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.OutboxEvent
	for _, e := range events {
		if strings.Contains(e.Payload, `"unlessReferenced":true`) {
			out = append(out, e)
		}
	}
	return out
}

func TestSetRejectsAnUnroutableNamespaceWithoutTouchingTheExistingFile(t *testing.T) {
	f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())

	if _, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", "application/x-yaml",
		[]byte("name: sam\n"), ""); err != nil {
		t.Fatalf("first set: %v", err)
	}

	f.scoped.Namespace = "myorg/prod"
	_, err := f.svc.Set(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", "application/x-yaml",
		[]byte("name: replaced\n"), "")
	if err == nil {
		t.Fatal("set = nil error, want the unroutable namespace rejected")
	}
	var brv *BusinessRuleViolationError
	if !errors.As(err, &brv) || brv.Code != "EXAMPLE_ARTIFACT_UNROUTABLE_NAMESPACE" {
		t.Fatalf("err = %v, want EXAMPLE_ARTIFACT_UNROUTABLE_NAMESPACE", err)
	}

	got, err := f.examples.GetByID(f.ctx, f.example.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row := got.ArtifactByRole(store.EvalArtifactRoleInput); row == nil || row.Filename != "config.yaml" {
		t.Fatalf("row lost its input file to a rejected call: %+v", got.Artifacts)
	}

	f.scoped.Namespace = "ns"
	content, _, err := f.svc.Get(f.ctx, f.example.ID, store.EvalArtifactRoleInput, "config.yaml", nil)
	if err != nil {
		t.Fatalf("the original bytes are gone: %v", err)
	}
	if string(content) != "name: sam\n" {
		t.Errorf("content = %q, want the original bytes", content)
	}
}

func TestExampleArtifactService_Roles(t *testing.T) {
	const (
		roleInput    = store.EvalArtifactRoleInput
		roleExpected = store.EvalArtifactRoleExpected
	)

	t.Run("both roles coexist under the same filename", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleInput, "report.md", "text/markdown",
			[]byte("the report to review"), ""); err != nil {
			t.Fatalf("set input: %v", err)
		}
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleExpected, "report.md", "text/markdown",
			[]byte("the reference report"), ""); err != nil {
			t.Fatalf("set expected: %v", err)
		}

		input, _, err := f.svc.Get(f.ctx, f.example.ID, roleInput, "report.md", nil)
		if err != nil {
			t.Fatalf("get input: %v", err)
		}
		expected, _, err := f.svc.Get(f.ctx, f.example.ID, roleExpected, "report.md", nil)
		if err != nil {
			t.Fatalf("get expected: %v", err)
		}
		if string(input) != "the report to review" || string(expected) != "the reference report" {
			t.Errorf("bytes = (%q, %q), want each role to keep its own content", input, expected)
		}
	})

	t.Run("a binary expected file is rejected; the same binary input is accepted", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		png := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte("fakepixels")...)

		if _, err := f.svc.Set(f.ctx, f.example.ID, roleInput, "diagram.png", "image/png", png, ""); err != nil {
			t.Fatalf("binary input rejected: %v — the agent may consume any type", err)
		}
		_, err := f.svc.Set(f.ctx, f.example.ID, roleExpected, "diagram.png", "image/png", png, "")
		if err == nil {
			t.Fatal("binary expected accepted, want rejection — the judge cannot grade it")
		}
		if code := asBusinessRuleErr(t, err).Code; code != "EXPECTED_ARTIFACT_NOT_TEXT" {
			t.Errorf("code = %q, want EXPECTED_ARTIFACT_NOT_TEXT", code)
		}
	})

	t.Run("clearing one role leaves the other", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleInput, "in.txt", "text/plain", []byte("in"), ""); err != nil {
			t.Fatalf("set input: %v", err)
		}
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleExpected, "ref.txt", "text/plain", []byte("ref"), ""); err != nil {
			t.Fatalf("set expected: %v", err)
		}

		if err := f.svc.Clear(f.ctx, f.example.ID, roleInput); err != nil {
			t.Fatalf("clear input: %v", err)
		}
		if got := f.keys(t, f.example.ID, roleInput); len(got) != 0 {
			t.Errorf("input keys = %v, want none", got)
		}
		if content, _, err := f.svc.Get(f.ctx, f.example.ID, roleExpected, "ref.txt", nil); err != nil || string(content) != "ref" {
			t.Errorf("expected file after clearing input: content=%q err=%v, want it untouched", content, err)
		}
	})

	t.Run("the dataset cap sums both roles", func(t *testing.T) {
		cfg := DefaultExampleArtifactConfig()
		cfg.MaxTotalBytesPerDataset = 20
		f := newExampleArtifactFixture(t, cfg)

		if _, err := f.svc.Set(f.ctx, f.example.ID, roleInput, "a.txt", "text/plain", []byte("0123456789"), ""); err != nil {
			t.Fatalf("input upload: %v", err)
		}
		_, err := f.svc.Set(f.ctx, f.example.ID, roleExpected, "b.txt", "text/plain", []byte("0123456789+"), "")
		if err == nil {
			t.Fatal("expected upload succeeded, want the dataset cap to count the input's bytes too")
		}
		if code := asBusinessRuleErr(t, err).Code; code != "DATASET_ARTIFACT_TOTAL_BYTES_CAP_REACHED" {
			t.Errorf("code = %q, want DATASET_ARTIFACT_TOTAL_BYTES_CAP_REACHED", code)
		}
	})

	t.Run("an unknown role is rejected on every operation", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		_, setErr := f.svc.Set(f.ctx, f.example.ID, "answer", "a.txt", "text/plain", []byte("x"), "")
		_, _, getErr := f.svc.Get(f.ctx, f.example.ID, "answer", "a.txt", nil)
		clearErr := f.svc.Clear(f.ctx, f.example.ID, "answer")
		for name, err := range map[string]error{"set": setErr, "get": getErr, "clear": clearErr} {
			if err == nil {
				t.Errorf("%s accepted role %q, want a validation error", name, "answer")
				continue
			}
			if field := asValidationError(t, err).Field; field != "role" {
				t.Errorf("%s field = %q, want role", name, field)
			}
		}
	})

	t.Run("deleting the example destroys both roles' bytes", func(t *testing.T) {
		f := newExampleArtifactFixture(t, DefaultExampleArtifactConfig())
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleInput, "in.txt", "text/plain", []byte("in"), ""); err != nil {
			t.Fatalf("set input: %v", err)
		}
		if _, err := f.svc.Set(f.ctx, f.example.ID, roleExpected, "ref.txt", "text/plain", []byte("ref"), ""); err != nil {
			t.Fatalf("set expected: %v", err)
		}

		examples := NewExampleService(f.examples, f.datasets, nil).WithFileDeletes(f.files)
		if err := examples.Delete(f.ctx, f.example.ID); err != nil {
			t.Fatalf("delete example: %v", err)
		}
		for _, role := range []string{roleInput, roleExpected} {
			if got := f.keys(t, f.example.ID, role); len(got) != 0 {
				t.Errorf("%s keys = %v, want none after the example is deleted", role, got)
			}
		}
	})
}
