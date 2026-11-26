// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func loadImageBlock(t *testing.T, filename string, content []byte) tool.ImageBlock {
	t.Helper()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, filename, string(content), "image/png")

	result, err := (&LoadArtifactTool{}).Execute(t.Context(), map[string]any{
		"filename": filename,
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImageBlocks) != 1 {
		t.Fatalf("got %d image blocks, want 1", len(result.ImageBlocks))
	}
	return result.ImageBlocks[0]
}

func TestLoadArtifactFitsAnImageOverTheProviderByteLimit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1500, 1500))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	content := encodePNG(t, img)
	if len(content) < 6<<20 {
		t.Fatalf("fixture is %d bytes, want at least 6 MiB", len(content))
	}

	block := loadImageBlock(t, "scan.png", content)
	if len(block.Data) > visionimage.MaxImageBytes {
		t.Errorf("emitted image is %d bytes, want at most %d", len(block.Data), visionimage.MaxImageBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("emitted image is not readable: %v", err)
	}
	if block.MIMEType != "image/"+format {
		t.Errorf("block MIME = %q, but the bytes are %s", block.MIMEType, format)
	}
	if cfg.Width != cfg.Height {
		t.Errorf("emitted %dx%d, want the square aspect ratio kept", cfg.Width, cfg.Height)
	}
}

func TestLoadArtifactScalesAnOverDimensionImageWithinMaxDim(t *testing.T) {
	img := image.NewPaletted(image.Rect(0, 0, 12000, 3000), color.Palette{color.White, color.Black})
	for x := range 12000 {
		img.SetColorIndex(x, x%3000, 1)
	}

	block := loadImageBlock(t, "panorama.png", encodePNG(t, img))
	cfg, _, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("emitted image is not readable: %v", err)
	}
	if cfg.Width != visionimage.MaxDim || cfg.Height != visionimage.MaxDim/4 {
		t.Errorf("emitted %dx%d, want %dx%d", cfg.Width, cfg.Height, visionimage.MaxDim, visionimage.MaxDim/4)
	}
	if len(block.Data) > visionimage.MaxImageBytes {
		t.Errorf("emitted image is %d bytes, want at most %d", len(block.Data), visionimage.MaxImageBytes)
	}
}

func pngDeclaring(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8
	ihdr[9] = 6
	var length, crc [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(ihdr)))
	sum := crc32.NewIEEE()
	sum.Write([]byte("IHDR"))
	sum.Write(ihdr)
	binary.BigEndian.PutUint32(crc[:], sum.Sum32())
	b.Write(length[:])
	b.WriteString("IHDR")
	b.Write(ihdr)
	b.Write(crc[:])
	return b.Bytes()
}

func TestLoadArtifactReportsAnImageThatCannotBeFitted(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, "huge.png", string(pngDeclaring(10000, 10000)), "image/png")

	result, err := (&LoadArtifactTool{}).Execute(t.Context(), map[string]any{
		"filename": "huge.png",
		"version":  float64(0),
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImageBlocks) != 0 {
		t.Fatalf("got %d image blocks, want 0 for an image the provider would reject", len(result.ImageBlocks))
	}
	if content, _ := result.Data["content"].(string); !strings.Contains(content, "too large to view inline") {
		t.Errorf("content = %q, want it to say the image is too large to view inline", content)
	}
	if advice, ok := result.Data["message_to_llm"]; ok {
		t.Errorf("message_to_llm = %q; any tool that reads an image hits the same limit, so the "+
			"extraction advice only spends a turn", advice)
	}
}
