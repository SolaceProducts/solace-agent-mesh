// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build browser

package plotlytool

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
)

const browserSampleJSON = `{"data":[{"x":["A","B","C"],"y":[10,25,15],"type":"bar"}],"layout":{"title":"Revenue","width":700,"height":450}}`

func renderOrSkip(t *testing.T, format string) []byte {
	t.Helper()
	if !chromiumAvailable(t) {
		t.Skip("no Chromium available; set SAM_CHROMIUM_PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := (&chromedpRenderer{}).Render(ctx, browserSampleJSON, renderFormat(format), 1)
	if err != nil {
		t.Fatalf("render %s: %v", format, err)
	}
	if len(out) == 0 {
		t.Fatalf("render %s: empty output", format)
	}
	return out
}

func TestBrowserRender_PNG(t *testing.T) {
	out := renderOrSkip(t, "png")
	if !bytes.HasPrefix(out, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("png output is not a PNG (first bytes %x)", out[:min(8, len(out))])
	}
}

func TestBrowserRender_SVG(t *testing.T) {
	out := renderOrSkip(t, "svg")
	if !bytes.Contains(out, []byte("<svg")) {
		t.Errorf("svg output does not contain <svg (got %q...)", string(out[:min(64, len(out))]))
	}
}

func TestBrowserRender_JPEG(t *testing.T) {
	out := renderOrSkip(t, "jpeg")
	if !bytes.HasPrefix(out, []byte{0xFF, 0xD8, 0xFF}) {
		t.Errorf("jpeg output is not a JPEG (first bytes %x)", out[:min(8, len(out))])
	}
}

func TestBrowserRender_WebP(t *testing.T) {
	out := renderOrSkip(t, "webp")
	if !bytes.HasPrefix(out, []byte("RIFF")) || !bytes.Contains(out[:min(16, len(out))], []byte("WEBP")) {
		t.Errorf("webp output is not a WebP (first bytes %x)", out[:min(16, len(out))])
	}
}

func TestBrowserRender_PDF(t *testing.T) {
	out := renderOrSkip(t, "pdf")
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Errorf("pdf output is not a PDF (first bytes %x)", out[:min(8, len(out))])
	}
}

func chromiumAvailable(t *testing.T) bool {
	t.Helper()
	_, err := chromium.Find()
	return err == nil
}
