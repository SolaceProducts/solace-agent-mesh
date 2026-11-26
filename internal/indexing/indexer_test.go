// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func projectSources(t *testing.T, artSvc *artifact.ScopedService, userID, projectID string) []SourceFile {
	t.Helper()
	ctx := t.Context()
	session := "project-" + projectID
	keys, err := artSvc.Inner.ListKeys(ctx, artifact.KeyScope{AppName: artSvc.Namespace, UserID: userID, SessionID: session})
	if err != nil {
		t.Fatalf("list project files: %v", err)
	}
	versionsByName := make(map[string][]int, len(keys))
	for _, key := range keys {
		versions, err := artSvc.Inner.ListVersions(ctx, artSvc.Resolve("", userID, session, key))
		if err != nil {
			t.Fatalf("list versions of %s: %v", key, err)
		}
		versionsByName[key] = versions
	}
	return ProjectSourceFiles(versionsByName)
}

func newTestArtSvc() (*artifact.ScopedService, *memory.Store) {
	store := memory.New()
	return &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test-ns",
	}, store
}

func TestBuildProjectIndex_ReportsPerFileConversionFailure(t *testing.T) {
	artSvc, store := newTestArtSvc()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const (
		userID    = "alice"
		projectID = "proj-fail"
	)
	session := "project-" + projectID

	badLoc := artSvc.Resolve("", userID, session, "broken.docx")
	if _, err := store.Save(ctx, badLoc, []byte("not a real docx"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); err != nil {
		t.Fatalf("seed bad docx: %v", err)
	}
	goodLoc := artSvc.Resolve("", userID, session, "notes.txt")
	if _, err := store.Save(ctx, goodLoc, []byte("pluto is a dwarf planet"), "text/plain"); err != nil {
		t.Fatalf("seed good txt: %v", err)
	}

	var failedFiles []string
	zip, err := BuildProjectIndex(ctx, projectID, userID, artSvc, projectSources(t, artSvc, userID, projectID), nil, logger,
		func(filename, errMsg string) {
			if errMsg == "" {
				t.Errorf("expected non-empty error message for %q", filename)
			}
			failedFiles = append(failedFiles, filename)
		})
	if err != nil {
		t.Fatalf("BuildProjectIndex returned fatal error: %v", err)
	}
	if len(zip) == 0 {
		t.Fatal("expected a non-empty index (good.txt should still index)")
	}
	if len(failedFiles) != 1 || failedFiles[0] != "broken.docx" {
		t.Fatalf("expected onConversionFailure for [broken.docx], got %v", failedFiles)
	}
}

func TestBuildProjectIndex_SkipsNonTextBinary(t *testing.T) {
	artSvc, store := newTestArtSvc()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const (
		userID    = "alice"
		projectID = "proj-img"
	)
	png := []byte("\x89PNG\r\n\x1a\nrest-of-image-bytes")
	loc := artSvc.Resolve("", userID, "project-"+projectID, "logo.png")
	if _, err := store.Save(ctx, loc, png, "image/png"); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	var failures int
	zip, err := BuildProjectIndex(ctx, projectID, userID, artSvc, projectSources(t, artSvc, userID, projectID), nil, logger,
		func(string, string) { failures++ })
	if err != nil {
		t.Fatalf("BuildProjectIndex: %v", err)
	}
	sources, found, err := ReadSources(zip)
	if err != nil || !found {
		t.Fatalf("read sources: found=%v err=%v", found, err)
	}
	if sources.IndexedFiles != 0 || len(sources.Files) != 1 || sources.Files[0].Filename != "logo.png" {
		t.Errorf("sources = %+v, want logo.png listed and nothing indexed", sources)
	}
	if failures != 0 {
		t.Errorf("skipping a binary must not be reported as a failure, got %d", failures)
	}
}

func TestBuildProjectIndex_NilCallbackIsSafe(t *testing.T) {
	artSvc, store := newTestArtSvc()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const (
		userID    = "bob"
		projectID = "proj-nilcb"
	)
	session := "project-" + projectID

	badLoc := artSvc.Resolve("", userID, session, "broken.docx")
	if _, err := store.Save(ctx, badLoc, []byte("garbage"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); err != nil {
		t.Fatalf("seed bad docx: %v", err)
	}

	if _, err := BuildProjectIndex(ctx, projectID, userID, artSvc, projectSources(t, artSvc, userID, projectID), nil, logger, nil); err != nil {
		t.Fatalf("BuildProjectIndex with nil callback errored: %v", err)
	}
}

func TestBuildProjectIndex_LeavesOutUserScopedFiles(t *testing.T) {
	artSvc := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const (
		userID    = "alice"
		projectID = "proj-user-scoped"
		secret    = "ownersecretmarker"
	)
	save := func(session, filename, content string) {
		t.Helper()
		if _, err := artSvc.Inner.Save(ctx, artSvc.Resolve("", userID, session, filename), []byte(content), "text/plain"); err != nil {
			t.Fatalf("seed %s: %v", filename, err)
		}
	}
	save("project-"+projectID, "notes.txt", "pluto is a dwarf planet")
	save("an-unrelated-session", "user:private.txt", secret+" is in a user-scoped file")

	zip, err := BuildProjectIndex(ctx, projectID, userID, artSvc, projectSources(t, artSvc, userID, projectID), nil, logger, nil)
	if err != nil {
		t.Fatalf("BuildProjectIndex: %v", err)
	}
	built, err := DeserializeFromZIP(zip)
	if err != nil {
		t.Fatalf("deserialize index: %v", err)
	}
	if built.Manifest.FileCount != 1 || built.Manifest.ChunkCount == 0 {
		t.Errorf("index holds %d files and %d chunks, want the one project file", built.Manifest.FileCount, built.Manifest.ChunkCount)
	}
	for _, chunk := range built.Manifest.Chunks {
		if chunk.Filename != "notes.txt" || strings.Contains(chunk.ChunkText, secret) {
			t.Errorf("index chunk of %q holds %q", chunk.Filename, chunk.ChunkText)
		}
	}

	t.Run("a project with only a user-scoped file has an empty index", func(t *testing.T) {
		onlyUserScoped := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
		loc := onlyUserScoped.Resolve("", userID, "an-unrelated-session", "user:private.txt")
		if _, saveErr := onlyUserScoped.Inner.Save(ctx, loc, []byte(secret), "text/plain"); saveErr != nil {
			t.Fatalf("seed: %v", saveErr)
		}
		zip, buildErr := BuildProjectIndex(ctx, projectID, userID, onlyUserScoped, projectSources(t, onlyUserScoped, userID, projectID), nil, logger, nil)
		if buildErr != nil {
			t.Fatalf("BuildProjectIndex: %v", buildErr)
		}
		sources, _, err := ReadSources(zip)
		if err != nil || sources.IndexedFiles != 0 || len(sources.Files) != 0 {
			t.Errorf("sources = %+v, err = %v; want an empty index", sources, err)
		}
	})
}
