// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func buildZIPWithEntry(t *testing.T, name string, body []byte, mode os.FileMode) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
	hdr.SetMode(mode)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("create zip entry %q: %v", name, err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write zip entry %q: %v", name, err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestValidateAndDetectRuntime_WindowsPathTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		zipName string
	}{
		{"backslash dotdot", `..\evil`},
		{"embedded backslash dotdot", `foo\..\evil`},
		{"mixed slash dotdot", `foo/..\\evil`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			zb := buildZIPWithEntry(t, tc.zipName, []byte("bad"), 0o644)
			_, err := validateAndDetectRuntime(zb)
			if err == nil {
				t.Fatalf("expected error for path %q", tc.zipName)
			}
		})
	}
}

func TestValidateAndDetectRuntime_SymlinkRejected(t *testing.T) {
	t.Parallel()
	zb := buildZIPWithEntry(t, "link", []byte("/etc/passwd"), os.ModeSymlink|0o777)
	_, err := validateAndDetectRuntime(zb)
	if err == nil {
		t.Fatal("expected error for symlink entry")
	}
}

func TestValidateAndDetectRuntime_ValidManifestYAML(t *testing.T) {
	t.Parallel()
	zb := buildZIP(t, map[string][]byte{
		"mypkg/manifest.yaml": []byte("name: mypkg\nversion: \"1.0\"\n"),
		"mypkg/tool.py":       []byte("x=1"),
	})
	runtime, err := validateAndDetectRuntime(zb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime != "python" {
		t.Errorf("runtime = %q, want python", runtime)
	}
}

func TestValidateAndDetectRuntime_ValidToolJSON(t *testing.T) {
	t.Parallel()
	zb := buildZIP(t, map[string][]byte{
		"mypkg/tool.json": []byte(`{"name":"mypkg","description":"ok"}`),
		"mypkg/tool.py":   []byte("x=1"),
	})
	runtime, err := validateAndDetectRuntime(zb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runtime != "python" {
		t.Errorf("runtime = %q, want python", runtime)
	}
}

func TestValidateAndDetectRuntime_ValidManifestWithParameters(t *testing.T) {
	t.Parallel()
	manifest := `name: mypkg
version: "1.0"
parameters:
  - name: foo
    type: string
  - name: bar
    type: integer
`
	zb := buildZIP(t, map[string][]byte{
		"mypkg/manifest.yaml": []byte(manifest),
		"mypkg/tool.py":       []byte("x=1"),
	})
	_, err := validateAndDetectRuntime(zb)
	if err != nil {
		t.Fatalf("unexpected error for valid manifest with parameters: %v", err)
	}
}

func TestToolsetService_Upload_ReuploadInUseOverwrites(t *testing.T) {
	t.Parallel()

	t.Run("re-upload of package used by an agent overwrites the stored zip", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		p := f.createPackage(ctx, t, "inuse_pkg")

		initialZIP := buildZIP(t, map[string][]byte{
			"inuse_pkg/manifest.yaml": []byte("name: inuse_pkg\nversion: \"1.0\"\n"),
			"inuse_pkg/tool.py":       []byte("x=1"),
		})
		if err := f.svc.Upload(ctx, p.ID, "pkg.zip", initialZIP, "alice"); err != nil {
			t.Fatalf("initial upload: %v", err)
		}

		db := f.repo.DB()
		agentID := idgen.New()
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO agents (id, name, description, type, created_by, updated_by, created_time, updated_time)
			 VALUES ('%s', 'my-agent', 'seed agent for in-use re-upload', 'standard', 'test', 'test', 0, 0)`, agentID)); err != nil {
			t.Fatalf("seed agent: %v", err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO agent_toolsets (agent_id, toolset) VALUES ('%s', 'inuse_pkg')`, agentID)); err != nil {
			t.Fatalf("seed toolset ref: %v", err)
		}

		newZIP := buildZIP(t, map[string][]byte{
			"inuse_pkg/manifest.yaml": []byte("name: inuse_pkg\nversion: \"2.0\"\n"),
			"inuse_pkg/tool.py":       []byte("y=2"),
		})
		if err := f.svc.Upload(ctx, p.ID, "pkg2.zip", newZIP, "alice"); err != nil {
			t.Fatalf("re-upload of in-use package: %v", err)
		}

		got, _ := f.repo.GetByID(ctx, p.ID)
		if got.ZipFilename == nil || *got.ZipFilename != "pkg2.zip" {
			t.Errorf("ZipFilename = %v, want pkg2.zip", got.ZipFilename)
		}
	})

	t.Run("first upload without existing zip always allowed", func(t *testing.T) {
		t.Parallel()
		f := newToolsetFixture(t)
		ctx := t.Context()

		p := f.createPackage(ctx, t, "fresh_pkg")
		zipBytes := buildZIP(t, map[string][]byte{
			"fresh_pkg/manifest.yaml": []byte("name: fresh_pkg\nversion: \"1.0\"\n"),
			"fresh_pkg/tool.py":       []byte("x=1"),
		})
		if err := f.svc.Upload(ctx, p.ID, "pkg.zip", zipBytes, "alice"); err != nil {
			t.Fatalf("Upload (no prior zip): %v", err)
		}
	})
}

func TestParseAgentYAML_Valid(t *testing.T) {
	t.Parallel()

	yamlDoc := `
apps:
  - app_config:
      display_name: "My Agent"
      instruction: "You are a helpful assistant that can answer questions."
      agent_card:
        description: "A helpful assistant"
        defaultInputModes: [text]
        defaultOutputModes: [text]
      tools:
        - toolset: builtin_artifact_tools
        - toolset: builtin_web_request_tools
      model_provider: [my-model]
`
	req, err := ParseAgentYAML([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseAgentYAML: %v", err)
	}
	if req.Name != "My Agent" {
		t.Errorf("Name = %q, want My Agent", req.Name)
	}
	if req.Description != "A helpful assistant" {
		t.Errorf("Description = %q, want A helpful assistant", req.Description)
	}
	if len(req.Toolsets) != 2 {
		t.Errorf("len(Toolsets) = %d, want 2", len(req.Toolsets))
	}
	if req.SystemPrompt == "" {
		t.Error("SystemPrompt should be populated")
	}
}

func TestParseAgentYAML_PrefersDisplayNameOverAgentName(t *testing.T) {
	t.Parallel()

	yamlDoc := `
apps:
  - app_config:
      agent_name: my_agent
      display_name: "My Pretty Agent"
      instruction: "You are a helpful assistant that can answer questions."
      agent_card:
        description: "A helpful assistant"
        defaultInputModes: [text]
        defaultOutputModes: [text]
`
	req, err := ParseAgentYAML([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseAgentYAML: %v", err)
	}
	if req.Name != "My Pretty Agent" {
		t.Errorf("Name = %q, want 'My Pretty Agent'", req.Name)
	}

	slugOnly := `
apps:
  - app_config:
      agent_name: orchestrator
      instruction: "You are a helpful assistant that can answer questions."
      agent_card:
        description: "x"
`
	req2, err := ParseAgentYAML([]byte(slugOnly))
	if err != nil {
		t.Fatalf("ParseAgentYAML slugOnly: %v", err)
	}
	if req2.Name != "orchestrator" {
		t.Errorf("Name = %q, want orchestrator", req2.Name)
	}
}

func TestParseAgentYAML_SkillsWithoutNameSkipped(t *testing.T) {
	t.Parallel()

	yamlDoc := `
apps:
  - app_config:
      display_name: "Skills Agent"
      instruction: "You are a helpful assistant that can answer questions."
      agent_card:
        description: "Has a malformed skill"
        defaultInputModes: [text]
        defaultOutputModes: [text]
        skills:
          - id: nameless
            description: "no name on this one"
          - name: ValidSkill
            description: "this one is fine"
`
	req, err := ParseAgentYAML([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("ParseAgentYAML: %v", err)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("expected 1 valid skill (nameless dropped), got %d: %+v", len(req.Skills), req.Skills)
	}
	if req.Skills[0].Name != "ValidSkill" {
		t.Errorf("kept skill should be ValidSkill, got %q", req.Skills[0].Name)
	}
}

func TestParseAgentYAML_MissingDisplayName(t *testing.T) {
	t.Parallel()
	yamlDoc := `
apps:
  - app_config:
      instruction: "some prompt"
`
	_, err := ParseAgentYAML([]byte(yamlDoc))
	if err == nil {
		t.Fatal("expected ValidationError for missing display_name")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestParseAgentYAML_MissingAppConfig(t *testing.T) {
	t.Parallel()
	yamlDoc := `
apps:
  - some_other_key: value
`
	_, err := ParseAgentYAML([]byte(yamlDoc))
	if err == nil {
		t.Fatal("expected ValidationError for missing app_config block")
	}
}

func TestParseAgentYAML_MalformedYAML(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentYAML([]byte(":: not valid yaml ::"))
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestValidateToolsetsReady(t *testing.T) {
	t.Parallel()

	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	ctx := t.Context()

	pkg := &store.ToolPackage{
		Name:            "my-pkg",
		DiscoveryStatus: "pending",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}

	t.Run("pending package rejected", func(t *testing.T) {
		err := validateToolsetsReady(ctx, []string{pkg.Name}, repo)
		if err == nil {
			t.Fatal("expected ValidationError for pending package")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("expected *ValidationError, got %T: %v", err, err)
		}
		if !errors.Is(err, ErrDiscoveryNotReady) {
			t.Errorf("a pending package must be marked ErrDiscoveryNotReady so a retrying caller can tell it will pass once discovery ends: %v", err)
		}
	})

	t.Run("failed package rejected", func(t *testing.T) {
		errJSON := `["discovery error"]`
		if updateErr := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "failed", &errJSON); updateErr != nil {
			t.Fatalf("update status: %v", updateErr)
		}
		err := validateToolsetsReady(ctx, []string{pkg.Name}, repo)
		if err == nil {
			t.Fatal("expected ValidationError for failed package")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("expected *ValidationError, got %T: %v", err, err)
		}
		if errors.Is(err, ErrDiscoveryNotReady) {
			t.Errorf("a failed package will never become ready, so it must not be marked ErrDiscoveryNotReady: %v", err)
		}
	})

	t.Run("ready package accepted", func(t *testing.T) {
		if updateErr := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); updateErr != nil {
			t.Fatalf("update status: %v", updateErr)
		}
		if err := validateToolsetsReady(ctx, []string{pkg.Name}, repo); err != nil {
			t.Errorf("unexpected error for ready package: %v", err)
		}
	})

	t.Run("builtin group skipped", func(t *testing.T) {
		if err := validateToolsetsReady(ctx, []string{"web"}, repo); err != nil {
			t.Errorf("unexpected error for builtin group: %v", err)
		}
	})

	t.Run("nil repo skips check", func(t *testing.T) {
		if err := validateToolsetsReady(ctx, []string{"anything"}, nil); err != nil {
			t.Errorf("unexpected error with nil repo: %v", err)
		}
	})
}
