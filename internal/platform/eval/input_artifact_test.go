// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type inputArtifactFixture struct {
	svc        *RunService
	examples   *store.EvalExampleRepo
	rows       *store.EvalExampleArtifactRepo
	results    *store.EvalRunResultRepo
	scoped     *artifact.ScopedService
	exampleSvc *ExampleArtifactService
	example    *store.EvalExample
	resultID   string
	db         *store.DB
	files      *FileDeletes
}

const fixtureExpectedContent = "the reference answer\n"

func newInputArtifactFixture(t *testing.T, content []byte) *inputArtifactFixture {
	t.Helper()
	db := store.TestDB(t)
	ctx := t.Context()

	datasets := store.NewEvalDatasetRepo(db)
	examples := store.NewEvalExampleRepo(db)
	artifactRows := store.NewEvalExampleArtifactRepo(db)
	experiments := store.NewEvalExperimentRepo(db)
	runs := store.NewEvalRunRepo(db)
	results := store.NewEvalRunResultRepo(db)

	ds := &store.EvalDataset{Name: "ds", CreatedBy: "u", UpdatedBy: "u"}
	if err := datasets.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "review the attached"}
	if err := examples.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &store.EvalExperiment{Name: "e", DatasetID: &ds.ID, TargetAgent: "a", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := experiments.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Model: "m", StartedBy: "u"}
	if err := runs.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	scoped := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	files := NewFileDeletes(nil, scoped, artifactRows)
	svc := NewExampleArtifactService(ExampleArtifactServiceParams{
		Examples:     examples,
		ArtifactRows: artifactRows,
		Artifacts:    scoped,
		FileDeletes:  files,
		Config:       DefaultExampleArtifactConfig(),
	})
	if _, err := svc.Set(ctx, ex.ID, store.EvalArtifactRoleInput, "config.yaml", "text/plain", content, ""); err != nil {
		t.Fatalf("upload input file: %v", err)
	}
	if _, err := svc.Set(ctx, ex.ID, store.EvalArtifactRoleExpected, "reference.md", "text/markdown",
		[]byte(fixtureExpectedContent), ""); err != nil {
		t.Fatalf("upload expected file: %v", err)
	}
	row, err := examples.GetByID(ctx, ex.ID)
	if err != nil {
		t.Fatalf("reload example: %v", err)
	}

	inputMeta := artifactMetadataForExample(row, store.EvalArtifactRoleInput, nil)
	expectedMeta := artifactMetadataForExample(row, store.EvalArtifactRoleExpected, nil)
	if inputMeta == nil || expectedMeta == nil {
		t.Fatal("artifactMetadataForExample returned nil for an example carrying both files")
	}
	res := &store.EvalRunResult{RunID: run.ID, ExampleID: row.ID, TrialIndex: 0,
		InputArtifactMetadata: inputMeta, ExpectedArtifactMetadata: expectedMeta}
	if err := results.Create(ctx, res); err != nil {
		t.Fatalf("create result: %v", err)
	}

	return &inputArtifactFixture{
		svc:        NewRunService(RunServiceParams{Results: results, Artifacts: scoped}),
		examples:   examples,
		rows:       artifactRows,
		results:    results,
		scoped:     scoped,
		exampleSvc: svc,
		example:    row,
		resultID:   res.ID,
		db:         db,
		files:      files,
	}
}

func TestRunService_GetRunResultInputArtifact(t *testing.T) {
	t.Run("loads the recorded bytes", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("name: sam\n"))
		content, mimeType, err := f.svc.GetRunResultInputArtifact(t.Context(), f.resultID, "config.yaml", nil)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(content) != "name: sam\n" {
			t.Errorf("content = %q, want the uploaded bytes", content)
		}
		if mimeType == "" {
			t.Error("mimeType is empty, want the recorded type")
		}
	})

	t.Run("a replace that reuses version 0 is not served as the historical bytes", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("original bytes"))
		ctx := t.Context()

		replaced, err := f.exampleSvc.Set(ctx, f.example.ID, store.EvalArtifactRoleInput,
			"config.yaml", "text/plain", []byte("replacement!!"), "")
		if err != nil {
			t.Fatalf("replace: %v", err)
		}
		if replaced.Version != 0 {
			t.Fatalf("replacement version = %d; this test only means something when the version resets to 0", replaced.Version)
		}

		runDueFileDeletes(t, f.db, f.files)
		content, _, err := f.svc.GetRunResultInputArtifact(ctx, f.resultID, "config.yaml", nil)
		if err == nil {
			t.Fatalf("get succeeded and returned %q, want not-found on the hash mismatch", content)
		}
		_ = asNotFoundError(t, err)
	})

	t.Run("clearing the example's file makes the historical download not-found", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("original bytes"))
		ctx := t.Context()

		if err := f.exampleSvc.Clear(ctx, f.example.ID, store.EvalArtifactRoleInput); err != nil {
			t.Fatalf("clear: %v", err)
		}
		runDueFileDeletes(t, f.db, f.files)
		if _, _, err := f.svc.GetRunResultInputArtifact(ctx, f.resultID, "config.yaml", nil); err == nil {
			t.Fatal("get succeeded, want not-found once the bytes are gone")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("a wrong filename or version", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("bytes"))
		ctx := t.Context()
		seven := 7
		for name, call := range map[string]func() error{
			"other filename": func() error {
				_, _, err := f.svc.GetRunResultInputArtifact(ctx, f.resultID, "other.yaml", nil)
				return err
			},
			"unrecorded version": func() error {
				_, _, err := f.svc.GetRunResultInputArtifact(ctx, f.resultID, "config.yaml", &seven)
				return err
			},
		} {
			if err := call(); err == nil {
				t.Errorf("%s: succeeded, want not-found", name)
			} else {
				_ = asNotFoundError(t, err)
			}
		}
	})

	t.Run("a trial with no input file", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("bytes"))
		bare := &store.EvalRunResult{
			RunID:      mustResultRunID(t, f),
			ExampleID:  f.example.ID,
			TrialIndex: 1,
		}
		if err := f.results.Create(t.Context(), bare); err != nil {
			t.Fatalf("create bare result: %v", err)
		}
		if _, _, err := f.svc.GetRunResultInputArtifact(t.Context(), bare.ID, "config.yaml", nil); err == nil {
			t.Fatal("get succeeded, want not-found")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("no artifact service", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("bytes"))
		bare := NewRunService(RunServiceParams{Results: f.results})
		if _, _, err := bare.GetRunResultInputArtifact(t.Context(), f.resultID, "config.yaml", nil); err == nil {
			t.Fatal("get succeeded, want not-found without an artifact store")
		} else {
			_ = asNotFoundError(t, err)
		}
	})
}

func TestRunService_GetRunResultExpectedArtifact(t *testing.T) {
	t.Run("loads the recorded bytes", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("input bytes"))
		content, _, err := f.svc.GetRunResultExpectedArtifact(t.Context(), f.resultID, "reference.md", nil)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(content) != fixtureExpectedContent {
			t.Errorf("content = %q, want the expected file's bytes", content)
		}
	})

	t.Run("the input filename is not served from the expected column", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("input bytes"))
		if _, _, err := f.svc.GetRunResultExpectedArtifact(t.Context(), f.resultID, "config.yaml", nil); err == nil {
			t.Fatal("get succeeded, want not-found — the roles must not cross")
		} else {
			_ = asNotFoundError(t, err)
		}
	})

	t.Run("a replaced expected file is not served as the historical bytes", func(t *testing.T) {
		f := newInputArtifactFixture(t, []byte("input bytes"))
		ctx := t.Context()
		if _, err := f.exampleSvc.Set(ctx, f.example.ID, store.EvalArtifactRoleExpected,
			"reference.md", "text/markdown", []byte("a different reference\n"), ""); err != nil {
			t.Fatalf("replace: %v", err)
		}
		runDueFileDeletes(t, f.db, f.files)
		if _, _, err := f.svc.GetRunResultExpectedArtifact(ctx, f.resultID, "reference.md", nil); err == nil {
			t.Fatal("get succeeded, want not-found on the hash mismatch")
		} else {
			_ = asNotFoundError(t, err)
		}
	})
}

func mustResultRunID(t *testing.T, f *inputArtifactFixture) string {
	t.Helper()
	row, err := f.results.GetByID(t.Context(), f.resultID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	return row.RunID
}

func exampleWithArtifacts(id string, rows ...*store.EvalExampleArtifact) *store.EvalExample {
	return &store.EvalExample{ID: id, Prompt: "p", Artifacts: rows}
}

func inputRowFixture(exampleID string) *store.EvalExampleArtifact {
	return &store.EvalExampleArtifact{
		ExampleID: exampleID, Role: store.EvalArtifactRoleInput,
		Filename: "config.yaml", MIMEType: "application/x-yaml", SizeBytes: 12,
		Version: 0, SessionID: "eval-example-" + exampleID + "-input", ContentHash: "abc123",
	}
}

func TestArtifactMetadataForExample(t *testing.T) {
	t.Parallel()

	row := exampleWithArtifacts("abc", inputRowFixture("abc"))

	t.Run("the file is recorded snake_case", func(t *testing.T) {
		got := artifactMetadataForExample(row, store.EvalArtifactRoleInput, nil)
		if got == nil {
			t.Fatal("got nil, want the example's file recorded")
		}
		for _, want := range []string{
			`"filename":"config.yaml"`, `"version":0`, `"mime_type":"application/x-yaml"`,
			`"size_bytes":12`, `"content_hash":"abc123"`, `"artifact_session_id":"eval-example-abc-input"`,
		} {
			if !strings.Contains(*got, want) {
				t.Errorf("metadata %s missing %s", *got, want)
			}
		}
	})

	t.Run("only the requested role is recorded", func(t *testing.T) {
		if got := artifactMetadataForExample(row, store.EvalArtifactRoleExpected, nil); got != nil {
			t.Errorf("got %q, want nil — the example has no expected file", *got)
		}
	})

	t.Run("text-only example and nil row record nothing", func(t *testing.T) {
		if got := artifactMetadataForExample(&store.EvalExample{ID: "x", Prompt: "p"}, store.EvalArtifactRoleInput, nil); got != nil {
			t.Errorf("got %q, want nil for a text-only example", *got)
		}
		if got := artifactMetadataForExample(nil, store.EvalArtifactRoleInput, nil); got != nil {
			t.Errorf("got %q, want nil for a nil example", *got)
		}
	})
}

func TestArtifactFromMetadata(t *testing.T) {
	t.Parallel()

	valid := `{"filename":"a.txt","version":3,"mime_type":"text/plain","size_bytes":5,"content_hash":"h","artifact_session_id":"s"}`
	rec := artifactFromMetadata(&valid, "res-1", nil)
	if rec == nil {
		t.Fatal("valid metadata decoded to nil")
	}
	if rec.Filename != "a.txt" || rec.Version != 3 || rec.SessionID != "s" || rec.ContentHash != "h" {
		t.Errorf("rec = %+v, want the decoded fields", rec)
	}

	empty, corrupt, noFilename := "", `{"filename":`, `{"version":1}`
	for name, in := range map[string]*string{
		"nil":         nil,
		"empty":       &empty,
		"corrupt":     &corrupt,
		"no filename": &noFilename,
	} {
		if got := artifactFromMetadata(in, "res-1", nil); got != nil {
			t.Errorf("%s decoded to %+v, want nil", name, got)
		}
	}
}

func TestArtifactCSVCell(t *testing.T) {
	t.Parallel()

	meta := `{"filename":"config.yaml","version":2}`
	if got := artifactCSVCell(&meta, "res-1", nil); got != "config.yaml@2" {
		t.Errorf("cell = %q, want config.yaml@2", got)
	}
	if got := artifactCSVCell(nil, "res-1", nil); got != "" {
		t.Errorf("cell = %q, want empty for a trial with no file", got)
	}
}

func TestConfigSnapshotArtifact(t *testing.T) {
	t.Parallel()

	if got := configSnapshotArtifact(&store.EvalExample{ID: "abc"}, store.EvalArtifactRoleInput); got != nil {
		t.Errorf("got %v, want a true nil for a text-only example", got)
	}

	raw := configSnapshotArtifact(exampleWithArtifacts("abc", inputRowFixture("abc")), store.EvalArtifactRoleInput)
	got, ok := raw.(*exampleArtifactRecord)
	if !ok {
		t.Fatalf("got %T, want the artifact record", raw)
	}
	if got.Filename != "config.yaml" || got.Version != 0 || got.ContentHash != "abc123" {
		t.Errorf("record = %+v, want the example's artifact fields", got)
	}
	if got.SessionID != "eval-example-abc-input" {
		t.Errorf("artifactSessionID = %v, want eval-example-abc-input", got.SessionID)
	}
}

func TestBuildConfigSnapshotCarriesTheExampleArtifacts(t *testing.T) {
	t.Parallel()

	exp := &store.EvalExperiment{ID: "exp-1", Name: "e", TargetAgent: "a"}
	withBoth := exampleWithArtifacts("with-file",
		inputRowFixture("with-file"),
		&store.EvalExampleArtifact{
			ExampleID: "with-file", Role: store.EvalArtifactRoleExpected,
			Filename: "reference.md", MIMEType: "text/markdown", SizeBytes: 7,
			Version: 0, SessionID: "eval-example-with-file-expected", ContentHash: "ref999",
		})
	withBoth.SequenceNumber = 1
	textOnly := &store.EvalExample{ID: "text-only", SequenceNumber: 2, Prompt: "just text"}
	examples := []*store.EvalExample{withBoth, textOnly}

	snapshot := buildConfigSnapshot(exp, "ds-1", "ds", examples, nil)
	dataset, ok := snapshot["dataset"].(map[string]any)
	if !ok {
		t.Fatalf("dataset = %T, want a map", snapshot["dataset"])
	}
	dicts, ok := dataset["examples"].([]map[string]any)
	if !ok {
		t.Fatalf("examples = %T, want a slice of maps", dataset["examples"])
	}
	if len(dicts) != 2 {
		t.Fatalf("examples = %d, want 2", len(dicts))
	}

	art, ok := dicts[0]["artifact"].(*exampleArtifactRecord)
	if !ok {
		t.Fatalf("examples[0].artifact = %T, want the artifact record", dicts[0]["artifact"])
	}
	if art.Filename != "config.yaml" || art.Version != 0 || art.SizeBytes != 12 {
		t.Errorf("artifact = %+v, want the example's input file", art)
	}
	if art.ContentHash != "abc123" || art.SessionID != "eval-example-with-file-input" {
		t.Errorf("artifact = %+v, want the hash and session id a historical download needs", art)
	}
	expArt, ok := dicts[0]["expected_artifact"].(*exampleArtifactRecord)
	if !ok {
		t.Fatalf("examples[0].expected_artifact = %T, want the artifact record", dicts[0]["expected_artifact"])
	}
	if expArt.Filename != "reference.md" || expArt.ContentHash != "ref999" {
		t.Errorf("expected_artifact = %+v, want the example's expected file", expArt)
	}

	for _, key := range []string{"artifact", "expected_artifact"} {
		raw, present := dicts[1][key]
		if !present {
			t.Errorf("examples[1] has no %s key at all; want present and nil", key)
		}
		if raw != nil {
			t.Errorf("examples[1].%s = %v, want nil", key, raw)
		}
	}
}

func TestRunResultResponseCarriesExampleArtifacts(t *testing.T) {
	t.Parallel()

	meta := `{"filename":"config.yaml","version":0,"mime_type":"application/x-yaml","size_bytes":12}`
	expectedMeta := `{"filename":"reference.md","version":0,"mime_type":"text/markdown","size_bytes":7}`
	withFile := runResultResponseFromRow(&store.EvalRunResultWithScores{
		EvalRunResult: &store.EvalRunResult{ID: "res-1",
			InputArtifactMetadata: &meta, ExpectedArtifactMetadata: &expectedMeta},
	}, nil)
	if withFile.InputArtifact == nil {
		t.Fatal("inputArtifact is nil, want the recorded file")
	}
	if withFile.InputArtifact.Filename != "config.yaml" || withFile.InputArtifact.Version != 0 {
		t.Errorf("inputArtifact = %+v, want config.yaml at version 0", withFile.InputArtifact)
	}
	if withFile.InputArtifact.MimeType != "application/x-yaml" || withFile.InputArtifact.Size != 12 {
		t.Errorf("inputArtifact = %+v, want the recorded mime type and size", withFile.InputArtifact)
	}
	if withFile.ExpectedArtifact == nil || withFile.ExpectedArtifact.Filename != "reference.md" {
		t.Errorf("expectedArtifact = %+v, want reference.md", withFile.ExpectedArtifact)
	}

	textOnly := runResultResponseFromRow(&store.EvalRunResultWithScores{
		EvalRunResult: &store.EvalRunResult{ID: "res-2"},
	}, nil)
	if textOnly.InputArtifact != nil || textOnly.ExpectedArtifact != nil {
		t.Errorf("artifacts = (%+v, %+v), want nil for a trial with no files",
			textOnly.InputArtifact, textOnly.ExpectedArtifact)
	}
}
