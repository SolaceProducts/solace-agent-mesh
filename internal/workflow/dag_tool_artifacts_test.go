// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func assertCreatedArtifact(t *testing.T, output map[string]any, wantRef string, wantVersion int) {
	t.Helper()
	if output["created_artifact"] != wantRef {
		t.Fatalf("created_artifact = %#v, want %q", output["created_artifact"], wantRef)
	}
	if output["created_artifact_version"] != wantVersion {
		t.Fatalf("created_artifact_version = %#v, want %d", output["created_artifact_version"], wantVersion)
	}
	wantName := strings.TrimSuffix(wantRef, fmt.Sprintf(":%d", wantVersion))
	if output["created_artifact_name"] != wantName {
		t.Fatalf("created_artifact_name = %#v, want %q", output["created_artifact_name"], wantName)
	}
}

func TestClassifyToolResult_NamesPreSavedArtifact(t *testing.T) {
	res := tool.OK("2 records saved",
		tool.WithDataObjects(tool.DataObject{
			Name:        "CaseComment_records.csv",
			ArtifactRef: &tool.ArtifactRef{Filename: "CaseComment_records.csv", Version: 1},
		}))

	_, output, err := classifyToolResult("query_salesforce_object", res, nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "CaseComment_records.csv:1", 1)
}

func TestClassifyToolResult_NamesFrameworkSavedArtifact(t *testing.T) {
	res := tool.OK("wrote it",
		tool.WithDataObjects(tool.DataObject{
			Name:    "generated.png",
			Content: []byte("not a real png"),
		}))

	_, output, err := classifyToolResult("create_image_from_description", res, nil,
		[]savedArtifact{{name: "generated.png", version: 0}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "generated.png:0", 0)
}

func TestClassifyToolResult_OmitsWhenSeveralArtifacts(t *testing.T) {
	res := tool.OK("wrote two", tool.WithDataObjects(
		tool.DataObject{Name: "a.txt", Content: []byte("a")},
		tool.DataObject{Name: "b.txt", Content: []byte("b")},
	))
	_, output, err := classifyToolResult("some_tool", res, nil,
		[]savedArtifact{{name: "a.txt"}, {name: "b.txt"}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	for _, key := range []string{"created_artifact", "created_artifact_name", "created_artifact_version"} {
		if _, present := output[key]; present {
			t.Fatalf("%s should be absent for several artifacts, got %#v", key, output[key])
		}
	}
}

func TestClassifyToolResult_DeduplicatesAcrossBothSources(t *testing.T) {
	res := tool.OK("saved",
		tool.WithDataObjects(tool.DataObject{
			Name:        "report.csv",
			ArtifactRef: &tool.ArtifactRef{Filename: "report.csv", Version: 2},
		}))
	_, output, err := classifyToolResult("some_tool", res, nil,
		[]savedArtifact{{name: "report.csv", version: 2}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "report.csv:2", 2)
}

func TestClassifyToolResult_PinsHighestVersionOfOneName(t *testing.T) {
	_, output, err := classifyToolResult("some_tool", tool.OK("saved three times"), nil,
		[]savedArtifact{{name: "draft.md", version: 3}, {name: "draft.md", version: 5}, {name: "draft.md", version: 4}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "draft.md:5", 5)
}

func TestClassifyToolResult_PinsHighestVersionAcrossSources(t *testing.T) {
	res := tool.OK("saved",
		tool.WithDataObjects(tool.DataObject{
			Name:        "report.csv",
			ArtifactRef: &tool.ArtifactRef{Filename: "report.csv", Version: 7},
		}))
	_, output, err := classifyToolResult("some_tool", res, nil,
		[]savedArtifact{{name: "report.csv", version: 6}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "report.csv:7", 7)
}

func TestClassifyToolResult_KeepsToolsOwnVersionAndNameKeys(t *testing.T) {
	res := tool.OK("done", tool.WithData(map[string]any{
		"created_artifact_version": "tool's version",
		"created_artifact_name":    "tool's name",
	}))
	_, output, err := classifyToolResult("some_tool", res, nil,
		[]savedArtifact{{name: "out.txt", version: 2}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if output["created_artifact"] != "out.txt:2" {
		t.Fatalf("created_artifact = %#v, want out.txt:2", output["created_artifact"])
	}
	if output["created_artifact_version"] != "tool's version" || output["created_artifact_name"] != "tool's name" {
		t.Fatalf("framework overwrote the tool's keys: %#v", output)
	}
}

func TestClassifyToolResult_AbsentWhenNothingSaved(t *testing.T) {
	_, output, err := classifyToolResult("get_current_time", tool.OK("now"), nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	for _, key := range []string{"created_artifact", "created_artifact_name", "created_artifact_version"} {
		if _, present := output[key]; present {
			t.Fatalf("%s should be absent, got %#v", key, output[key])
		}
	}
}

func TestClassifyToolResult_DoesNotOverrideToolsOwnField(t *testing.T) {
	res := tool.OK("done",
		tool.WithData(map[string]any{"created_artifact": "the tool's own value"}),
		tool.WithDataObjects(tool.DataObject{
			Name:        "framework.csv",
			ArtifactRef: &tool.ArtifactRef{Filename: "framework.csv"},
		}))
	_, output, err := classifyToolResult("some_tool", res, nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if output["created_artifact"] != "the tool's own value" {
		t.Fatalf("framework overwrote the tool's field: %#v", output["created_artifact"])
	}
	if _, present := output["created_artifact_version"]; present {
		t.Fatalf("created_artifact_version must not describe a value the tool chose: %#v", output["created_artifact_version"])
	}
}

func TestSavedArtifactCollector_MakesFacadeSaveNameable(t *testing.T) {
	c := &savedArtifactCollector{}
	c.record(context.Background(), tool.ArtifactSavedInfo{Filename: "build_manifest.yaml", Version: 1})
	c.record(context.Background(), tool.ArtifactSavedInfo{Filename: ""})

	res := tool.OK("validated")
	_, output, err := classifyToolResult("validate_build_manifest", res, nil, c.artifacts())
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	assertCreatedArtifact(t, output, "build_manifest.yaml:1", 1)
}

func TestSavedArtifactCollector_ConcurrentRecords(t *testing.T) {
	c := &savedArtifactCollector{}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			c.record(context.Background(), tool.ArtifactSavedInfo{
				Filename: fmt.Sprintf("part_%02d.json", i),
			})
			_ = c.artifacts()
		})
	}
	wg.Wait()
	if got := len(c.artifacts()); got != 32 {
		t.Fatalf("collected %d artifacts, want 32", got)
	}
}
