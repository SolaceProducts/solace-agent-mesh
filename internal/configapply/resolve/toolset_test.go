// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
)

const toolsetYAML = `kind: toolset
name: demo
description: "Demo toolset"
`

func toolsetManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	return &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "demo"}},
	}}
}

func minimalZipBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestResolveToolsets_AuthorFlow(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/demo.yaml":            toolsetYAML,
		"toolsets/demo/src/go.mod":      "module demo\n",
		"toolsets/demo/src/cmd/main.go": "package main\nfunc main() {}\n",
	})

	got, err := ResolveToolsets(root, toolsetManifest(t))
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d toolsets; want 1", len(got))
	}
	if got[0].Name != "demo" || got[0].Description != "Demo toolset" {
		t.Errorf("identity = %s / %q", got[0].Name, got[0].Description)
	}
	wantSource := filepath.Join(root, "toolsets", "demo", "src")
	if got[0].SourceDir != wantSource {
		t.Errorf("SourceDir = %q, want %q", got[0].SourceDir, wantSource)
	}
	if got[0].ZipPath != "" {
		t.Errorf("ZipPath = %q, want empty (author flow)", got[0].ZipPath)
	}
	wantHeader := filepath.Join(root, "toolsets", "demo.yaml")
	if got[0].SourceFile != wantHeader {
		t.Errorf("SourceFile = %q, want %q", got[0].SourceFile, wantHeader)
	}
}

func TestResolveToolsets_MirrorFlow(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "toolsets/demo.yaml", toolsetYAML)
	writeBytes(t, root, "toolsets/demo/demo.zip", minimalZipBytes(t))

	got, err := ResolveToolsets(root, toolsetManifest(t))
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d toolsets; want 1", len(got))
	}
	wantZip := filepath.Join(root, "toolsets", "demo", "demo.zip")
	if got[0].ZipPath != wantZip {
		t.Errorf("ZipPath = %q, want %q", got[0].ZipPath, wantZip)
	}
	if got[0].SourceDir != "" {
		t.Errorf("SourceDir = %q, want empty (mirror flow)", got[0].SourceDir)
	}
}

func TestResolveToolsets_BothSrcAndZip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "toolsets/demo.yaml", toolsetYAML)
	writeFile(t, root, "toolsets/demo/src/go.mod", "module demo\n")
	writeBytes(t, root, "toolsets/demo/demo.zip", minimalZipBytes(t))

	_, err := ResolveToolsets(root, toolsetManifest(t))
	if err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "both") || !strings.Contains(msg, "delete one") {
		t.Errorf("error %q does not explain the collision", msg)
	}
	if !strings.Contains(msg, "src") || !strings.Contains(msg, "demo.zip") {
		t.Errorf("error %q should reference both src/ and demo.zip", msg)
	}
}

func TestResolveToolsets_MissingBundle(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/demo.yaml": toolsetYAML,
	})

	_, err := ResolveToolsets(root, toolsetManifest(t))
	if err == nil {
		t.Fatal("expected missing-bundle error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no bundle source") {
		t.Errorf("error %q does not mention missing bundle source", msg)
	}
	if !strings.Contains(msg, "author flow") || !strings.Contains(msg, "mirror flow") {
		t.Errorf("error %q should hint at both workflows", msg)
	}
}

func TestResolveToolsets_UnknownTopLevelKeyRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/demo.yaml":       toolsetYAML + "config: {}\n",
		"toolsets/demo/src/go.mod": "module demo\n",
	})

	_, err := ResolveToolsets(root, toolsetManifest(t))
	if err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
	for _, want := range []string{filepath.Join("toolsets", "demo.yaml"), `"config"`, "spec.config"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveToolsets_MissingMetadata(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/demo/src/go.mod": "module demo\n",
	})

	_, err := ResolveToolsets(root, toolsetManifest(t))
	if err == nil {
		t.Fatal("expected missing-metadata error, got nil")
	}
	if !strings.Contains(err.Error(), "no metadata") {
		t.Errorf("error %q does not mention metadata", err.Error())
	}
	if !strings.Contains(err.Error(), filepath.Join("toolsets", "demo.yaml")) {
		t.Errorf("error %q does not name the expected path", err.Error())
	}
}

func TestResolveToolsets_NoEntries(t *testing.T) {
	root := writeRepo(t, map[string]string{})
	got, err := ResolveToolsets(root, &manifest.Manifest{})
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 resources, got %d", len(got))
	}
}

func TestResolveToolsets_ConfigBlockAndEnvVarSubstitution(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://api.openai.com/v1")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")

	root := writeRepo(t, map[string]string{
		"toolsets/openai.yaml": `
kind: toolset
name: openai
description: "OpenAI tools"
spec:
  config:
    api_base: ${OPENAI_API_BASE, https://fallback}
    api_key: ${OPENAI_API_KEY}
    temperature: 0.7
`,
		"toolsets/openai/src/tool.py": "# placeholder",
	})

	m := &manifest.Manifest{
		Resources: map[string][]manifest.ResourceEntry{
			"toolsets": {{Name: "openai"}},
		},
	}
	got, err := ResolveToolsets(root, m)
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d toolsets; want 1", len(got))
	}
	r := got[0]
	if r.Name != "openai" {
		t.Errorf("name = %q; want openai", r.Name)
	}
	if r.Config == nil {
		t.Fatalf("Config is nil; expected a populated map")
	}
	if r.Config["api_base"] != "https://api.openai.com/v1" {
		t.Errorf("api_base = %v; want substituted value from env", r.Config["api_base"])
	}
	if r.Config["api_key"] != "sk-from-env" {
		t.Errorf("api_key = %v; want substituted value from env", r.Config["api_key"])
	}
	if r.Config["temperature"] != 0.7 {
		t.Errorf("temperature = %v; want 0.7", r.Config["temperature"])
	}
}

func TestResolveToolsets_ConfigEnvFallback(t *testing.T) {
	if err := os.Unsetenv("MISSING_API_BASE"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	root := writeRepo(t, map[string]string{
		"toolsets/x.yaml": `
kind: toolset
name: x
spec:
  config:
    api_base: ${MISSING_API_BASE, https://default.example.com}
`,
		"toolsets/x/src/tool.py": "# placeholder",
	})

	m := &manifest.Manifest{
		Resources: map[string][]manifest.ResourceEntry{
			"toolsets": {{Name: "x"}},
		},
	}
	got, err := ResolveToolsets(root, m)
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if got[0].Config["api_base"] != "https://default.example.com" {
		t.Errorf("api_base = %v; want default substitution", got[0].Config["api_base"])
	}
}

func TestResolveToolsets_NoConfigBlock(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/y.yaml":        `kind: toolset` + "\n" + `name: y` + "\n",
		"toolsets/y/src/tool.py": "# placeholder",
	})

	m := &manifest.Manifest{
		Resources: map[string][]manifest.ResourceEntry{
			"toolsets": {{Name: "y"}},
		},
	}
	got, err := ResolveToolsets(root, m)
	if err != nil {
		t.Fatalf("ResolveToolsets: %v", err)
	}
	if got[0].Config != nil {
		t.Errorf("Config = %v; want nil (absent)", got[0].Config)
	}
}

func importSource(t *testing.T, m *manifest.Manifest, name, externalDir string) {
	t.Helper()
	if m.Sources == nil {
		m.Sources = map[string]sources.Source{}
	}
	m.Sources[name] = sources.Source{
		Raw:    "file://" + externalDir,
		Scheme: sources.SchemeFile,
		URL:    externalDir,
	}
}

func TestResolver_Toolsets_LocalRegression(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/demo.yaml":            toolsetYAML,
		"toolsets/demo/src/go.mod":      "module demo\n",
		"toolsets/demo/src/cmd/main.go": "package main\nfunc main() {}\n",
	})

	got, err := New(root, toolsetManifest(t), Options{}).Toolsets(context.Background())
	if err != nil {
		t.Fatalf("Toolsets: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Fatalf("got %+v; want 1 toolset named demo", got)
	}
	if got[0].Provenance != nil {
		t.Errorf("local toolset got Provenance=%+v; want nil", got[0].Provenance)
	}
	wantSource := filepath.Join(root, "toolsets", "demo", "src")
	if got[0].SourceDir != wantSource {
		t.Errorf("SourceDir = %q, want %q", got[0].SourceDir, wantSource)
	}
}

func TestResolver_Toolsets_Imported(t *testing.T) {
	external := writeRepo(t, map[string]string{
		"toolsets/sales.yaml": `kind: toolset
name: sales
description: "Salesforce tools from bizops"
`,
		"toolsets/sales/src/go.mod":      "module sales\n",
		"toolsets/sales/src/cmd/main.go": "package main\nfunc main() {}\n",
	})
	root := writeRepo(t, map[string]string{})

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "sales", Source: "bizops"}},
	}}
	importSource(t, m, "bizops", external)

	got, err := New(root, m, Options{}).Toolsets(context.Background())
	if err != nil {
		t.Fatalf("Toolsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d toolsets; want 1", len(got))
	}
	rt := got[0]
	if rt.Name != "sales" {
		t.Errorf("Name = %q; want sales", rt.Name)
	}
	if rt.Description != "Salesforce tools from bizops" {
		t.Errorf("Description = %q", rt.Description)
	}
	wantSrc := filepath.Join(external, "toolsets", "sales", "src")
	if rt.SourceDir != wantSrc {
		t.Errorf("SourceDir = %q, want %q (points into imported tree)", rt.SourceDir, wantSrc)
	}
	wantDir := filepath.Join(external, "toolsets", "sales")
	if rt.ToolsetDir != wantDir {
		t.Errorf("ToolsetDir = %q, want %q", rt.ToolsetDir, wantDir)
	}
	if rt.Provenance == nil {
		t.Fatalf("Provenance is nil; want populated for imports")
	}
	if rt.Provenance.Name != "bizops" {
		t.Errorf("Provenance.Name = %q, want bizops", rt.Provenance.Name)
	}
	if rt.Provenance.OriginalName != "sales" {
		t.Errorf("Provenance.OriginalName = %q, want sales", rt.Provenance.OriginalName)
	}
}

func TestResolver_Toolsets_AliasedImport(t *testing.T) {
	external := writeRepo(t, map[string]string{
		"toolsets/sales.yaml": `kind: toolset
name: sales
description: "Salesforce tools"
`,
		"toolsets/sales/src/main.py": "# placeholder\n",
	})
	root := writeRepo(t, map[string]string{})

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "bizops_sales", From: "sales", Source: "bizops"}},
	}}
	importSource(t, m, "bizops", external)

	got, err := New(root, m, Options{}).Toolsets(context.Background())
	if err != nil {
		t.Fatalf("Toolsets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d toolsets; want 1", len(got))
	}
	rt := got[0]
	if rt.Name != "bizops_sales" {
		t.Errorf("Name = %q; want bizops_sales (alias applied)", rt.Name)
	}
	wantSrc := filepath.Join(external, "toolsets", "sales", "src")
	if rt.SourceDir != wantSrc {
		t.Errorf("SourceDir = %q, want %q", rt.SourceDir, wantSrc)
	}
	if rt.Provenance == nil || rt.Provenance.OriginalName != "sales" {
		t.Errorf("Provenance.OriginalName = %+v; want sales", rt.Provenance)
	}
}

func TestResolver_Toolsets_AliasedImport_MirrorFlow(t *testing.T) {
	external := t.TempDir()
	writeFile(t, external, "toolsets/sales.yaml", `kind: toolset
name: sales
description: "Salesforce tools (zip)"
`)
	writeBytes(t, external, "toolsets/sales/sales.zip", minimalZipBytes(t))
	root := writeRepo(t, map[string]string{})

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "bizops_sales", From: "sales", Source: "bizops"}},
	}}
	importSource(t, m, "bizops", external)

	got, err := New(root, m, Options{}).Toolsets(context.Background())
	if err != nil {
		t.Fatalf("Toolsets: %v", err)
	}
	wantZip := filepath.Join(external, "toolsets", "sales", "sales.zip")
	if got[0].ZipPath != wantZip {
		t.Errorf("ZipPath = %q, want %q (must use bare from-name)", got[0].ZipPath, wantZip)
	}
}

func TestResolver_Toolsets_Collision_LocalVsImport(t *testing.T) {
	external := writeRepo(t, map[string]string{
		"toolsets/sales.yaml": `kind: toolset
name: sales
`,
		"toolsets/sales/src/x.py": "# placeholder\n",
	})
	root := writeRepo(t, map[string]string{
		"toolsets/sales.yaml":     "kind: toolset\nname: sales\n",
		"toolsets/sales/src/x.py": "# placeholder\n",
	})

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {
			{Name: "sales"},
			{Name: "sales", Source: "bizops"},
		},
	}}
	importSource(t, m, "bizops", external)

	_, err := New(root, m, Options{}).Toolsets(context.Background())
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sales") || !strings.Contains(msg, "claimed by both") {
		t.Errorf("error %q does not explain the collision", msg)
	}
	if !strings.Contains(msg, "alias the import") {
		t.Errorf("error %q should hint at aliasing as a fix", msg)
	}
}

func TestResolver_Toolsets_Import_MissingMetadata(t *testing.T) {
	external := writeRepo(t, map[string]string{
		"toolsets/sales/src/main.py": "# placeholder\n",
	})
	root := writeRepo(t, map[string]string{})

	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "sales", Source: "bizops"}},
	}}
	importSource(t, m, "bizops", external)

	_, err := New(root, m, Options{}).Toolsets(context.Background())
	if err == nil {
		t.Fatal("expected missing-metadata error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sales") || !strings.Contains(msg, "not found under source") {
		t.Errorf("error %q does not name the missing toolset / source", msg)
	}
}

func TestResolveToolsets_ImportRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"toolsets/sales.yaml":        "kind: toolset\nname: sales\n",
		"toolsets/sales/src/main.py": "# placeholder\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"toolsets": {{Name: "sales", Source: "bizops"}},
	}}
	_, err := ResolveToolsets(root, m)
	if err == nil {
		t.Fatal("expected import-not-supported error")
	}
	if !strings.Contains(err.Error(), "resolve.New") {
		t.Errorf("error should direct callers to resolve.New, got %v", err)
	}
}

func writeFile(t *testing.T, root, relPath, body string) {
	t.Helper()
	writeBytes(t, root, relPath, []byte(body))
}

func writeBytes(t *testing.T, root, relPath string, body []byte) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}
