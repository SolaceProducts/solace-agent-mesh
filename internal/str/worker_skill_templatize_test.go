// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

const templatizeSidecar = "template:\n" +
	"  file: report.html\n" +
	"  output_filename: out.html\n" +
	"  mime_type: text/html\n" +
	"  description: Demo report\n" +
	"substitutions:\n" +
	"  title:\n" +
	"    description: H1\n" +
	"    required: true\n" +
	"data_inputs:\n" +
	"  rows:\n" +
	"    description: data\n" +
	"    artifact: data.json\n" +
	"    mime_type: application/json\n" +
	"    required: true\n" +
	"    schema:\n" +
	"      type: array\n"

const templatizeBody = "<h1>@@title@@</h1>«artifact_content:data.json»"

const templatizeBodyArtifact = "report.html"

func seedDataInput(t *testing.T, svc *artifact.ScopedService) {
	t.Helper()
	loc := svc.Resolve(testAgentName, testUserID, testSessionID, "data.json")
	if _, err := svc.Inner.Save(t.Context(), loc, []byte(`[1,2,3]`), "application/json"); err != nil {
		t.Fatalf("seed data.json: %v", err)
	}
}

func seedTemplateBody(t *testing.T, svc *artifact.ScopedService) {
	t.Helper()
	loc := svc.Resolve(testAgentName, testUserID, testSessionID, templatizeBodyArtifact)
	if _, err := svc.Inner.Save(t.Context(), loc, []byte(templatizeBody), "text/html"); err != nil {
		t.Fatalf("seed template body: %v", err)
	}
}

func templatizeParams(tool string, extra map[string]any) *remotetool.InvokeParams {
	args := map[string]any{"body_artifact": templatizeBodyArtifact, "sidecar": templatizeSidecar}
	maps.Copy(args, extra)
	return &remotetool.InvokeParams{
		ToolName: tool, AppName: testAgentName, UserID: testUserID, SessionID: testSessionID, Args: args,
	}
}

func TestVerifyTemplateCandidate(t *testing.T) {
	t.Run("clean render saves the preview as an artifact, never inline", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedDataInput(t, svc)
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger(), embedResolveCheck: func(_ context.Context, candidate string, _ *artifact.ScopedService, _, _, _ string) (string, error) {
			return strings.ReplaceAll(candidate, "«artifact_content:data.json:0»", "[1,2,3]"), nil
		}}
		res, ierr := w.executeVerifyTemplateCandidate(t.Context(), templatizeParams(toolVerifyTemplateCandidate, map[string]any{"substitutions": map[string]any{"title": "Q3"}}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != true || res["resolved"] != true {
			t.Fatalf("res: %+v", res)
		}
		if _, ok := res["rendered"]; ok {
			t.Errorf("verify must not return rendered HTML inline: %+v", res)
		}
		previewName, _ := res["preview_artifact"].(string)
		if previewName == "" {
			t.Fatalf("preview_artifact missing: %+v", res)
		}
		loc := svc.Resolve(testAgentName, testUserID, testSessionID, previewName)
		content, _, err := svc.Inner.Load(t.Context(), loc, nil)
		if err != nil {
			t.Fatalf("load preview artifact: %v", err)
		}
		rendered := string(content)
		if !strings.Contains(rendered, "<h1>Q3</h1>") || !strings.Contains(rendered, "[1,2,3]") {
			t.Errorf("saved preview = %q", rendered)
		}
	})

	t.Run("substitution failure returns ok=false", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedDataInput(t, svc)
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger(), embedResolveCheck: func(_ context.Context, c string, _ *artifact.ScopedService, _, _, _ string) (string, error) {
			return c, nil
		}}
		res, ierr := w.executeVerifyTemplateCandidate(t.Context(), templatizeParams(toolVerifyTemplateCandidate, nil), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != false {
			t.Fatalf("want ok=false, res: %+v", res)
		}
	})

	t.Run("resolution marker returns ok=false", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedDataInput(t, svc)
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger(), embedResolveCheck: func(_ context.Context, _ string, _ *artifact.ScopedService, _, _, _ string) (string, error) {
			return "oops [Embed Error: artifact not found]", nil
		}}
		res, ierr := w.executeVerifyTemplateCandidate(t.Context(), templatizeParams(toolVerifyTemplateCandidate, map[string]any{"substitutions": map[string]any{"title": "Q3"}}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != false {
			t.Fatalf("want ok=false, res: %+v", res)
		}
	})

	t.Run("unwired checker returns unrendered ok", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedDataInput(t, svc)
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger()}
		res, ierr := w.executeVerifyTemplateCandidate(t.Context(), templatizeParams(toolVerifyTemplateCandidate, map[string]any{"substitutions": map[string]any{"title": "Q3"}}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != true || res["resolved"] != false {
			t.Fatalf("res: %+v", res)
		}
	})

	t.Run("missing required args rejected", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		_, ierr := w.executeVerifyTemplateCandidate(t.Context(), &remotetool.InvokeParams{ToolName: toolVerifyTemplateCandidate, Args: map[string]any{"sidecar": templatizeSidecar}}, svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})
}

func TestPackageTemplate(t *testing.T) {
	t.Run("produces a zip bundle with embeds preserved", func(t *testing.T) {
		svc, store := newTestScopedSvc()
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger()}
		res, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, nil), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["filename"] != "report.html.samt" {
			t.Fatalf("filename = %v", res["filename"])
		}

		loc := svc.Resolve(testAgentName, testUserID, testSessionID, "report.html.samt")
		content, mime, err := store.Load(t.Context(), loc, nil)
		if err != nil {
			t.Fatalf("load zip: %v", err)
		}
		if mime != "application/vnd.sam-template+zip" {
			t.Errorf("mime = %q, want application/vnd.sam-template+zip", mime)
		}

		zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			t.Fatalf("open zip: %v", err)
		}
		got := map[string]string{}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open entry %q: %v", f.Name, err)
			}
			b, _ := io.ReadAll(rc)
			if err := rc.Close(); err != nil {
				t.Fatalf("close entry %q: %v", f.Name, err)
			}
			got[f.Name] = string(b)
		}
		body, ok := got["assets/report.html"]
		if !ok {
			t.Fatalf("zip entries = %v", keysOf(got))
		}
		if !strings.Contains(body, "«artifact_content:data.json»") {
			t.Errorf("embed must be preserved raw in the zip: %q", body)
		}
		if _, ok := got["assets/report.template.yaml"]; !ok {
			t.Errorf("sidecar entry missing: %v", keysOf(got))
		}
	})

	t.Run("rejects unsafe template.file (traversal / path / separators)", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger()}
		for _, file := range []string{"sub/report.html", "..", ".", `..\evil.html`, "a/../b.html", ""} {
			bad := "template:\n  file: " + file + "\n  mime_type: text/html\n"
			_, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, map[string]any{"sidecar": bad}), svc)
			if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
				t.Errorf("template.file %q should be rejected, got %+v", file, ierr)
			}
		}
	})

	t.Run("rejects binary mime_type", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger()}
		bad := "template:\n  file: report.bin\n  mime_type: application/octet-stream\n"
		_, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, map[string]any{"sidecar": bad}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request for binary mime, got %+v", ierr)
		}
	})

	t.Run("custom output_filename gets .samt suffix", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger()}
		res, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, map[string]any{"output_filename": "my_bundle"}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["filename"] != "my_bundle.samt" {
			t.Errorf("filename = %v", res["filename"])
		}
	})
}

func TestHasTemplatesSkill(t *testing.T) {
	dir := t.TempDir()
	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if (&Worker{skillRegistry: reg}).hasTemplatesSkill() {
		t.Error("hasTemplatesSkill should be false with no skills")
	}

	mustWrite(t, filepath.Join(dir, "sam-templates", "SKILL.md"), "---\nname: sam-templates\ndescription: d\n---\nbody")
	reg2 := NewSkillRegistry(dir)
	if err := reg2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !(&Worker{skillRegistry: reg2}).hasTemplatesSkill() {
		t.Error("hasTemplatesSkill should be true when the sam-templates skill is present")
	}
}

func TestRegisterBuiltinSkillTools_TemplateToolsAlwaysRegistered(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "other", "SKILL.md"), "---\nname: other\ndescription: d\n---\nbody")
	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	w := &Worker{skillRegistry: reg, manifest: NewManifest("", newDiscardLogger())}
	w.registerBuiltinSkillTools()
	for _, name := range []string{
		toolInstantiateTemplate, toolReadTemplate,
		toolVerifyTemplateCandidate, toolPackageTemplate,
	} {
		if w.manifest.GetTool(name) == nil {
			t.Errorf("template tool %q should register even without the sam-templates skill present", name)
		}
	}
}

func packagedTemplate(t *testing.T) (*Worker, *artifact.ScopedService) {
	t.Helper()
	svc, _ := newTestScopedSvc()
	seedTemplateBody(t, svc)
	w := &Worker{logger: newDiscardLogger()}
	if _, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, nil), svc); ierr != nil {
		t.Fatalf("package: %+v", ierr)
	}
	return w, svc
}

func readTemplateParams(extra map[string]any) *remotetool.InvokeParams {
	args := map[string]any{"source_artifact": "report.html.samt"}
	maps.Copy(args, extra)
	return &remotetool.InvokeParams{
		ToolName: toolReadTemplate, AppName: testAgentName, UserID: testUserID, SessionID: testSessionID, Args: args,
	}
}

func TestExecuteReadTemplate(t *testing.T) {
	t.Run("returns sidecar only by default", func(t *testing.T) {
		w, svc := packagedTemplate(t)
		res, ierr := w.executeReadTemplate(t.Context(), readTemplateParams(nil), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != true || res["template_file"] != "report.html" {
			t.Fatalf("res: %+v", res)
		}
		if _, hasBody := res["body"]; hasBody {
			t.Errorf("body must be omitted unless include_body: %+v", res)
		}
		if s, _ := res["sidecar"].(string); !strings.Contains(s, "data_inputs") {
			t.Errorf("sidecar = %q", s)
		}
	})

	t.Run("include_body returns small body inline", func(t *testing.T) {
		w, svc := packagedTemplate(t)
		res, ierr := w.executeReadTemplate(t.Context(), readTemplateParams(map[string]any{"include_body": true}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if b, _ := res["body"].(string); !strings.Contains(b, "«artifact_content:data.json»") {
			t.Errorf("body = %v", res["body"])
		}
	})

	t.Run("include_body spills a large body to an artifact", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		bigBody := "<h1>@@title@@</h1>" + strings.Repeat("x", maxInlineTemplateBodyBytes+100)
		zipb, err := skillruntime.BuildTemplateZip("report.html", bigBody, templatizeSidecar)
		if err != nil {
			t.Fatal(err)
		}
		store := w.newScopedStore(readTemplateParams(nil), svc)
		if _, err := store.Save(t.Context(), "big.samt", zipb, skillruntime.TemplateArtifactMIME, "big"); err != nil {
			t.Fatal(err)
		}
		res, ierr := w.executeReadTemplate(t.Context(), readTemplateParams(map[string]any{"source_artifact": "big.samt", "include_body": true}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if _, hasInline := res["body"]; hasInline {
			t.Errorf("large body must not be inlined: %+v", res)
		}
		if res["body_artifact"] == nil {
			t.Errorf("expected body spilled to an artifact: %+v", res)
		}
	})

	t.Run("missing artifact rejected", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		_, ierr := w.executeReadTemplate(t.Context(), readTemplateParams(nil), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})

	t.Run("neither source_artifact nor skill_name+asset rejected", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		_, ierr := w.executeReadTemplate(t.Context(), &remotetool.InvokeParams{ToolName: toolReadTemplate, AppName: testAgentName, UserID: testUserID, SessionID: testSessionID, Args: map[string]any{}}, svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})
}

func skillWithBundledTemplate(t *testing.T) (*Worker, *artifact.ScopedService) {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "rpt")
	mustWrite(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: rpt\ndescription: d\n---\nbody")
	samt, err := skillruntime.BuildTemplateZip("report.html", templatizeBody, templatizeSidecar)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteBytes(t, filepath.Join(skillDir, "assets", "report.html.samt"), samt)
	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	svc, _ := newTestScopedSvc()
	return &Worker{skillRegistry: reg, logger: newDiscardLogger()}, svc
}

func skillReadTemplateParams(extra map[string]any) *remotetool.InvokeParams {
	args := map[string]any{"skill_name": "rpt", "asset": "assets/report.html.samt"}
	maps.Copy(args, extra)
	return &remotetool.InvokeParams{
		ToolName: toolReadTemplate, AppName: testAgentName, UserID: testUserID, SessionID: testSessionID, Args: args,
	}
}

func TestExecuteReadTemplate_SkillBundled(t *testing.T) {
	t.Run("reads a skill-bundled .samt contract", func(t *testing.T) {
		w, svc := skillWithBundledTemplate(t)
		res, ierr := w.executeReadTemplate(t.Context(), skillReadTemplateParams(nil), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["ok"] != true || res["template_file"] != "report.html" {
			t.Fatalf("res: %+v", res)
		}
		if s, _ := res["sidecar"].(string); !strings.Contains(s, "data_inputs") {
			t.Errorf("sidecar = %q", s)
		}
	})

	t.Run("include_body returns the bundled body", func(t *testing.T) {
		w, svc := skillWithBundledTemplate(t)
		res, ierr := w.executeReadTemplate(t.Context(), skillReadTemplateParams(map[string]any{"include_body": true}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if b, _ := res["body"].(string); !strings.Contains(b, "«artifact_content:data.json»") {
			t.Errorf("body = %v", res["body"])
		}
	})

	t.Run("missing bundled asset rejected", func(t *testing.T) {
		w, svc := skillWithBundledTemplate(t)
		_, ierr := w.executeReadTemplate(t.Context(), skillReadTemplateParams(map[string]any{"asset": "assets/nope.samt"}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})

	t.Run("non-.samt asset rejected", func(t *testing.T) {
		w, svc := skillWithBundledTemplate(t)
		_, ierr := w.executeReadTemplate(t.Context(), skillReadTemplateParams(map[string]any{"asset": "report.html"}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})

	t.Run("unknown skill rejected", func(t *testing.T) {
		w, svc := skillWithBundledTemplate(t)
		_, ierr := w.executeReadTemplate(t.Context(), skillReadTemplateParams(map[string]any{"skill_name": "ghost"}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})
}

func TestExecuteBuiltinSkillTool_TemplateDispatch(t *testing.T) {
	svc, _ := newTestScopedSvc()
	seedTemplateBody(t, svc)
	reg := NewSkillRegistry(t.TempDir())
	if err := reg.Load(); err != nil {
		t.Fatalf("load registry: %v", err)
	}
	w := &Worker{logger: newDiscardLogger(), skillRegistry: reg}

	pkgRes, ierr := w.executeBuiltinSkillTool(t.Context(), templatizeParams(toolPackageTemplate, nil), svc)
	if ierr != nil {
		t.Fatalf("dispatch package: %+v", ierr)
	}
	if pkgRes.ToolResult["filename"] != "report.html.samt" {
		t.Fatalf("package result: %+v", pkgRes.ToolResult)
	}

	readRes, ierr := w.executeBuiltinSkillTool(t.Context(), readTemplateParams(nil), svc)
	if ierr != nil {
		t.Fatalf("dispatch read: %+v", ierr)
	}
	if readRes.ToolResult["template_file"] != "report.html" {
		t.Fatalf("read result: %+v", readRes.ToolResult)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
