// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package plotlytool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type fakeRenderer struct {
	out        []byte
	err        error
	gotFigJSON string
	gotFormat  string
}

func (f *fakeRenderer) Render(_ context.Context, figJSON, format string, _ int) ([]byte, error) {
	f.gotFigJSON = figJSON
	f.gotFormat = format
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func withFakeRenderer(t *testing.T, fr renderer) {
	t.Helper()
	prev := defaultRenderer
	defaultRenderer = fr
	t.Cleanup(func() { defaultRenderer = prev })
}

const sampleJSON = `{"data":[{"x":[1,2,3],"y":[4,5,6],"type":"bar"}],"layout":{"title":"Test"}}`

func strptr(s string) *string { return &s }

func TestNormalizeConfig(t *testing.T) {
	t.Run("json passthrough", func(t *testing.T) {
		got, err := normalizeConfig(sampleJSON, "json")
		if err != nil {
			t.Fatalf("normalizeConfig: %v", err)
		}
		if got != sampleJSON {
			t.Errorf("json should pass through unchanged, got %q", got)
		}
	})

	t.Run("yaml converted to json", func(t *testing.T) {
		yamlCfg := "data:\n  - x: [1, 2, 3]\n    y: [4, 5, 6]\n    type: bar\nlayout:\n  title: Test\n"
		got, err := normalizeConfig(yamlCfg, "yaml")
		if err != nil {
			t.Fatalf("normalizeConfig: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(got), &doc); err != nil {
			t.Fatalf("yaml did not convert to valid JSON: %v (%q)", err, got)
		}
		if _, ok := doc["data"]; !ok {
			t.Errorf("converted JSON missing data key: %q", got)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		if _, err := normalizeConfig("   ", "json"); err == nil {
			t.Error("expected error for empty content")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		if _, err := normalizeConfig("{not valid", "json"); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("yaml scalar not mapping", func(t *testing.T) {
		if _, err := normalizeConfig("just a string", "yaml"); err == nil {
			t.Error("expected error for non-mapping YAML")
		}
	})

	t.Run("unknown format", func(t *testing.T) {
		if _, err := normalizeConfig(sampleJSON, "toml"); err == nil {
			t.Error("expected error for unknown config_format")
		}
	})
}

func TestEnsureExtension(t *testing.T) {
	cases := []struct{ in, format, want string }{
		{"chart", "png", "chart.png"},
		{"chart.png", "png", "chart.png"},
		{"CHART.PNG", "png", "CHART.PNG"},
		{"chart", "svg", "chart.svg"},
		{"chart.png", "svg", "chart.png.svg"},
		{"chart", "jpg", "chart.jpg"},
		{"chart", "pdf", "chart.pdf"},
	}
	for _, c := range cases {
		if got := ensureExtension(c.in, c.format); got != c.want {
			t.Errorf("ensureExtension(%q, %q) = %q, want %q", c.in, c.format, got, c.want)
		}
	}
}

func TestRenderFormat(t *testing.T) {
	if got := renderFormat("jpg"); got != "jpeg" {
		t.Errorf("renderFormat(jpg) = %q, want jpeg", got)
	}
	for _, f := range []string{"png", "jpeg", "webp", "svg", "pdf"} {
		if got := renderFormat(f); got != f {
			t.Errorf("renderFormat(%q) = %q, want unchanged", f, got)
		}
	}
}

func TestHandle_HappyPath_PNG(t *testing.T) {
	fr := &fakeRenderer{out: []byte{0x89, 0x50, 0x4E, 0x47}}
	withFakeRenderer(t, fr)

	res, err := handle(context.Background(), Params{
		ConfigContent:  sampleJSON,
		ConfigFormat:   "json",
		OutputFilename: "revenue",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %v: %s", res.Status, res.Message)
	}
	if fr.gotFormat != "png" {
		t.Errorf("renderer format = %q, want png (default)", fr.gotFormat)
	}
	if fr.gotFigJSON != sampleJSON {
		t.Errorf("renderer figJSON = %q, want %q", fr.gotFigJSON, sampleJSON)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(res.DataObjects))
	}
	obj := res.DataObjects[0]
	if obj.Name != "revenue.png" {
		t.Errorf("filename = %q, want revenue.png", obj.Name)
	}
	if obj.MIMEType != "image/png" {
		t.Errorf("mime = %q, want image/png", obj.MIMEType)
	}
	if obj.Disposition != sdk.DispositionArtifact {
		t.Errorf("disposition = %q, want artifact", obj.Disposition)
	}
	if obj.Metadata["generation_tool"] != toolName {
		t.Errorf("generation_tool = %v, want %q", obj.Metadata["generation_tool"], toolName)
	}
	if obj.Metadata["output_format"] != "png" || obj.Metadata["source_format"] != "json" {
		t.Errorf("metadata format keys wrong: %v", obj.Metadata)
	}
}

func TestHandle_SVG(t *testing.T) {
	fr := &fakeRenderer{out: []byte("<svg></svg>")}
	withFakeRenderer(t, fr)

	res, _ := handle(context.Background(), Params{
		ConfigContent:  sampleJSON,
		ConfigFormat:   "json",
		OutputFilename: "chart",
		OutputFormat:   strptr("svg"),
	}, &sdk.ToolContext{})
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success: %s", res.Message)
	}
	obj := res.DataObjects[0]
	if obj.Name != "chart.svg" || obj.MIMEType != "image/svg+xml" {
		t.Errorf("svg output wrong: name=%q mime=%q", obj.Name, obj.MIMEType)
	}
	if fr.gotFormat != "svg" {
		t.Errorf("renderer format = %q, want svg", fr.gotFormat)
	}
}

func TestHandle_JPG_MapsToJpeg(t *testing.T) {
	fr := &fakeRenderer{out: []byte{0xFF, 0xD8}}
	withFakeRenderer(t, fr)

	res, _ := handle(context.Background(), Params{
		ConfigContent:  sampleJSON,
		ConfigFormat:   "json",
		OutputFilename: "chart",
		OutputFormat:   strptr("jpg"),
	}, &sdk.ToolContext{})
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success: %s", res.Message)
	}
	obj := res.DataObjects[0]
	if obj.Name != "chart.jpg" {
		t.Errorf("filename = %q, want chart.jpg (requested extension preserved)", obj.Name)
	}
	if obj.MIMEType != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", obj.MIMEType)
	}
	if fr.gotFormat != "jpeg" {
		t.Errorf("renderer format = %q, want jpeg (jpg mapped)", fr.gotFormat)
	}
}

func TestHandle_PDF(t *testing.T) {
	fr := &fakeRenderer{out: []byte("%PDF-1.4")}
	withFakeRenderer(t, fr)

	res, _ := handle(context.Background(), Params{
		ConfigContent:  sampleJSON,
		ConfigFormat:   "json",
		OutputFilename: "chart",
		OutputFormat:   strptr("pdf"),
	}, &sdk.ToolContext{})
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success: %s", res.Message)
	}
	obj := res.DataObjects[0]
	if obj.Name != "chart.pdf" || obj.MIMEType != "application/pdf" {
		t.Errorf("pdf output wrong: name=%q mime=%q", obj.Name, obj.MIMEType)
	}
}

func TestHandle_EPS_Unsupported(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{out: []byte("x")})
	res, _ := handle(context.Background(), Params{
		ConfigContent:  sampleJSON,
		ConfigFormat:   "json",
		OutputFilename: "chart",
		OutputFormat:   strptr("eps"),
	}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error status for eps, got %v", res.Status)
	}
	if !strings.Contains(res.Message, "Unsupported output_format") {
		t.Errorf("expected unsupported-format message, got %q", res.Message)
	}
}

func TestHandle_EmptyConfig(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{out: []byte("x")})
	res, _ := handle(context.Background(), Params{ConfigContent: "  ", ConfigFormat: "json", OutputFilename: "c"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status for empty config, got %v", res.Status)
	}
}

func TestHandle_ChromiumNotFound(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{err: &errChromiumNotFound{inner: context.DeadlineExceeded}})
	res, _ := handle(context.Background(), Params{ConfigContent: sampleJSON, ConfigFormat: "json", OutputFilename: "c"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error status, got %v", res.Status)
	}
	if !strings.Contains(res.Message, "Chromium") {
		t.Errorf("expected actionable chromium message, got %q", res.Message)
	}
}

func TestHandle_EmptyPayload(t *testing.T) {
	withFakeRenderer(t, &fakeRenderer{out: nil})
	res, _ := handle(context.Background(), Params{ConfigContent: sampleJSON, ConfigFormat: "json", OutputFilename: "c"}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status for empty payload, got %v", res.Status)
	}
}

func TestDecodeDataURL(t *testing.T) {
	t.Run("base64 png", func(t *testing.T) {
		got, err := decodeDataURL("data:image/png;base64,iVBORw0KGgo=")
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) == 0 {
			t.Error("expected decoded bytes")
		}
	})
	t.Run("uri-encoded svg", func(t *testing.T) {
		got, err := decodeDataURL("data:image/svg+xml,%3Csvg%3E%3C%2Fsvg%3E")
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if string(got) != "<svg></svg>" {
			t.Errorf("svg decode = %q, want <svg></svg>", string(got))
		}
	})
	t.Run("malformed", func(t *testing.T) {
		if _, err := decodeDataURL("not-a-data-url"); err == nil {
			t.Error("expected error for malformed data URL")
		}
	})
}

func TestTools_Registration(t *testing.T) {
	defs := Tools()
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(defs))
	}
}
