// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

const zipSidecar = "template:\n  file: report.html\n  output_filename: out.html\n  mime_type: text/html\n  description: Quarterly\n"
const zipBody = "<h1>@@title@@</h1>«artifact_content:data.json»"

func TestBuildReadTemplateZip_RoundTrip(t *testing.T) {
	good, err := BuildTemplateZip("report.html", zipBody, zipSidecar)
	if err != nil {
		t.Fatalf("BuildTemplateZip: %v", err)
	}
	name, body, sidecar, err := ReadTemplateZip(good)
	if err != nil {
		t.Fatalf("ReadTemplateZip: %v", err)
	}
	if name != "report.html" || body != zipBody || sidecar != zipSidecar {
		t.Errorf("name=%q body=%q sidecar=%q", name, body, sidecar)
	}
}

func TestBuildTemplateZip_RejectsNonBareFilename(t *testing.T) {
	if _, err := BuildTemplateZip("../evil.html", zipBody, zipSidecar); err == nil {
		t.Error("want error for non-bare filename")
	}
}

func TestReadTemplateZip_Rejections(t *testing.T) {
	t.Run("not a zip", func(t *testing.T) {
		if _, _, _, err := ReadTemplateZip([]byte("not a zip archive")); err == nil {
			t.Error("want error for non-zip bytes")
		}
	})

	rawZip := func(t *testing.T, entries map[string]string) []byte {
		t.Helper()
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range entries {
			fw, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	t.Run("entry outside assets/", func(t *testing.T) {
		bad := rawZip(t, map[string]string{"report.html": zipBody, "report.template.yaml": zipSidecar})
		if _, _, _, err := ReadTemplateZip(bad); err == nil {
			t.Error("want error for entries not under assets/")
		}
	})

	t.Run("mismatched sidecar name", func(t *testing.T) {
		bad := rawZip(t, map[string]string{"assets/report.html": zipBody, "assets/other.template.yaml": zipSidecar})
		if _, _, _, err := ReadTemplateZip(bad); err == nil {
			t.Error("want error when sidecar name does not match body")
		}
	})

	t.Run("wrong entry count", func(t *testing.T) {
		bad := rawZip(t, map[string]string{"assets/report.html": zipBody})
		if _, _, _, err := ReadTemplateZip(bad); err == nil {
			t.Error("want error for a lone entry")
		}
	})

	t.Run("nested path inside assets/ rejected", func(t *testing.T) {
		bad := rawZip(t, map[string]string{"assets/sub/report.html": zipBody, "assets/report.template.yaml": zipSidecar})
		if _, _, _, err := ReadTemplateZip(bad); err == nil {
			t.Error("want error for a nested entry under assets/")
		}
	})

	t.Run("oversized archive rejected", func(t *testing.T) {
		huge := strings.Repeat("x", maxTemplateZipTotal+1)
		bad := rawZip(t, map[string]string{"assets/report.html": huge, "assets/report.template.yaml": zipSidecar})
		if _, _, _, err := ReadTemplateZip(bad); err == nil {
			t.Error("want error for an archive exceeding the size cap")
		}
	})
}

func TestScanTemplates_SingleSamt(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")
	samt, err := BuildTemplateZip("report.html", zipBody, zipSidecar)
	if err != nil {
		t.Fatal(err)
	}
	writeBytes(t, filepath.Join(assetsDir, "report.html.samt"), samt)
	writeFile(t, filepath.Join(assetsDir, "logo.txt"), "logo")

	got := ScanTemplates(assetsDir)
	if len(got) != 1 {
		t.Fatalf("templates = %+v", got)
	}
	if got[0].Name != "report.html" || got[0].OutputFilename != "out.html" || got[0].Asset != "assets/report.html.samt" {
		t.Errorf("template info = %+v", got[0])
	}
}
