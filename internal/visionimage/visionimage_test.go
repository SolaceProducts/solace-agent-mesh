// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visionimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode png %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatalf("encode jpeg %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8
	ihdr[9] = 6
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(ihdr)))
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(ihdr)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc.Sum32())
	b.Write(length[:])
	b.WriteString("IHDR")
	b.Write(ihdr)
	b.Write(c[:])
	return b.Bytes()
}

func TestClamp(t *testing.T) {
	out, mime, ok := Clamp(pngBytes(t, 10000, 100), "image/png")
	if !ok {
		t.Fatal("oversized decodable PNG should downscale (ok)")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode resized png: %v", err)
	}
	if cfg.Width > MaxDim || cfg.Height > MaxDim {
		t.Errorf("resized %dx%d exceeds cap %d", cfg.Width, cfg.Height, MaxDim)
	}
	if cfg.Width != MaxDim {
		t.Errorf("longest axis = %d, want %d", cfg.Width, MaxDim)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}

	jout, jmime, jok := Clamp(jpegBytes(t, 12000, 200), "image/jpeg")
	if !jok {
		t.Fatal("oversized JPEG should downscale (ok)")
	}
	if jmime != "image/jpeg" {
		t.Errorf("jpeg mime = %q, want image/jpeg", jmime)
	}
	if jcfg, _, err := image.DecodeConfig(bytes.NewReader(jout)); err != nil || jcfg.Width > MaxDim {
		t.Errorf("resized jpeg invalid or over cap: err=%v dims=%dx%d", err, jcfg.Width, jcfg.Height)
	}

	small := pngBytes(t, 100, 100)
	if out2, mime2, ok2 := Clamp(small, "image/png"); !ok2 || !bytes.Equal(out2, small) || mime2 != "image/png" {
		t.Errorf("within-bounds image should pass through unchanged & ok")
	}

	junk := []byte("definitely not an image")
	if out3, mime3, ok3 := Clamp(junk, "image/webp"); !ok3 || !bytes.Equal(out3, junk) || mime3 != "image/webp" {
		t.Errorf("undecodable input should pass through unchanged & ok")
	}

	bomb := pngHeaderOnly(30000, 30000)
	if _, _, okBomb := Clamp(bomb, "image/png"); okBomb {
		t.Error("oversized image past the pixel ceiling must be skipped (ok=false), not decoded")
	}
}

func TestDownscaleReportsWhyAnImageCannotBeSent(t *testing.T) {
	if _, _, err := Downscale(pngHeaderOnly(30000, 30000), "image/png"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("past the pixel ceiling: err = %v, want ErrTooLarge", err)
	}
	_, _, err := Downscale(pngHeaderOnly(9000, 4000), "image/png")
	if err == nil || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrUnreadable) {
		t.Errorf("oversized but undecodable: err = %v, want a decode error that is neither ErrTooLarge nor ErrUnreadable", err)
	}
	truncated := pngHeaderOnly(100, 100)[:20]
	if _, _, err := Downscale(truncated, "image/png"); !errors.Is(err, ErrUnreadable) {
		t.Errorf("a PNG signature with a truncated header: err = %v, want ErrUnreadable", err)
	}
	if out, mime, ok := Clamp(truncated, "image/png"); !ok || !bytes.Equal(out, truncated) || mime != "image/png" {
		t.Error("Clamp must still pass an unreadable image through unchanged for the agent path")
	}
}

func solid(w, h int, c color.Color) *image.Paletted {
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White, c})
	for i := range img.Pix {
		img.Pix[i] = 2
	}
	return img
}

func TestToStillPNGReencodesTheFirstFrame(t *testing.T) {
	red, blue := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{solid(8, 4, red), solid(8, 4, blue)}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	var bmpData, tiffData bytes.Buffer
	if err := bmp.Encode(&bmpData, solid(8, 4, red)); err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(&tiffData, solid(8, 4, red), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"animated gif": animated.Bytes(), "bmp": bmpData.Bytes(), "tiff": tiffData.Bytes()} {
		t.Run(name, func(t *testing.T) {
			out, err := ToStillPNG(data)
			if err != nil {
				t.Fatalf("ToStillPNG: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("output is not a PNG: %v", err)
			}
			if got := img.Bounds().Size(); got != image.Pt(8, 4) {
				t.Errorf("size = %v, want 8x4", got)
			}
			if r, g, b, _ := img.At(0, 0).RGBA(); r>>8 != 255 || g != 0 || b != 0 {
				t.Errorf("pixel = %v, want the first frame's red", img.At(0, 0))
			}
		})
	}
}

func TestToStillPNGReportsWhyAnImageCannotBeConverted(t *testing.T) {
	if _, err := ToStillPNG(pngHeaderOnly(30000, 30000)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("past the pixel ceiling: err = %v, want ErrTooLarge", err)
	}
	if _, err := ToStillPNG([]byte("not an image")); !errors.Is(err, ErrUnreadable) {
		t.Errorf("not an image: err = %v, want ErrUnreadable", err)
	}
}

func TestDownscaleShrinksAnOversizedBMPToPNG(t *testing.T) {
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, image.NewRGBA(image.Rect(0, 0, MaxDim+1000, 10))); err != nil {
		t.Fatal(err)
	}
	out, mime, err := Downscale(buf.Bytes(), "image/bmp")
	if err != nil {
		t.Fatalf("Downscale: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil || mime != "image/png" {
		t.Fatalf("Downscale returned %s that does not decode as PNG: %v", mime, err)
	}
	if cfg.Width != MaxDim {
		t.Errorf("width = %d, want %d", cfg.Width, MaxDim)
	}
}

func TestFitAndClampLeaveBMPAndTIFFUndecoded(t *testing.T) {
	oversized := image.NewRGBA(image.Rect(0, 0, MaxDim+1000, 10))
	var bmpData, tiffData bytes.Buffer
	if err := bmp.Encode(&bmpData, oversized); err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(&tiffData, oversized, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"bmp": bmpData.Bytes(), "tiff": tiffData.Bytes()} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Fit(data, "image/png"); !errors.Is(err, ErrUnreadable) {
				t.Errorf("Fit of %s labelled image/png: err = %v, want ErrUnreadable without decoding", name, err)
			}
			out, mime, _ := Clamp(data, "image/png")
			if !bytes.Equal(out, data) || mime != "image/png" {
				t.Errorf("Clamp of %s labelled image/png returned %d bytes as %s, want the input unchanged", name, len(out), mime)
			}
		})
	}
}

func TestDataURL(t *testing.T) {
	if got := DataURL("image/png", []byte("hi")); got != "data:image/png;base64,aGk=" {
		t.Errorf("DataURL = %q", got)
	}
}

func TestRenderable(t *testing.T) {
	for mime, want := range map[string]bool{
		"image/png": true, "IMAGE/JPEG; charset=binary": true, "image/jpg": true, "image/webp": true, "image/gif": true,
		"image/svg+xml": false, "image/heic": false, "application/pdf": false, "": false,
	} {
		if got := Renderable(mime); got != want {
			t.Errorf("Renderable(%q) = %v, want %v", mime, got, want)
		}
	}
}

func noisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode noise png %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

func TestFitBringsAnImageOverTheByteLimitWithinIt(t *testing.T) {
	big := noisePNG(t, 1100, 1100)
	if len(big) <= MaxImageBytes {
		t.Fatalf("fixture is %d bytes, want it over MaxImageBytes (%d)", len(big), MaxImageBytes)
	}
	out, mime, err := Fit(big, "image/png")
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	if len(out) > MaxImageBytes {
		t.Errorf("fitted image is %d bytes, want at most %d", len(out), MaxImageBytes)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(out)); err != nil || format != "jpeg" {
		t.Errorf("fitted bytes are not a readable JPEG: format=%q err=%v", format, err)
	}
}

func noiseJPEG(t *testing.T, w, h, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(3, 4))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode noise jpeg %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

func TestFitBringsALargeJPEGWithinTheByteLimitWithoutOvershrinking(t *testing.T) {
	const side = 2400
	big := noiseJPEG(t, side, side, 100)
	if len(big) <= MaxImageBytes {
		t.Fatalf("fixture is %d bytes, want it over MaxImageBytes (%d)", len(big), MaxImageBytes)
	}
	out, mime, err := Fit(big, "image/jpeg")
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	if len(out) > MaxImageBytes {
		t.Errorf("fitted image is %d bytes, want at most %d", len(out), MaxImageBytes)
	}
	if mime != "image/jpeg" {
		t.Errorf("mime = %q, want image/jpeg", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode fitted image: %v", err)
	}
	if cfg.Width != cfg.Height {
		t.Errorf("fitted to %dx%d, want the square aspect ratio kept", cfg.Width, cfg.Height)
	}
	if cfg.Width < side/4 {
		t.Errorf("fitted to %d px wide from %d, shrunk well past what the byte limit needs", cfg.Width, side)
	}
}

func TestFitScalesAnOverDimensionImageWithinMaxDim(t *testing.T) {
	out, mime, err := Fit(pngBytes(t, 12000, 300), "image/png")
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode fitted image: %v", err)
	}
	if cfg.Width != MaxDim || cfg.Height != 200 {
		t.Errorf("fitted to %dx%d, want %dx200 with the aspect ratio kept", cfg.Width, cfg.Height, MaxDim)
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
}

func TestFitReturnsAnImageWithinLimitsUnchanged(t *testing.T) {
	small := pngBytes(t, 100, 100)
	out, mime, err := Fit(small, "image/png")
	if err != nil || !bytes.Equal(out, small) || mime != "image/png" {
		t.Errorf("Fit = (%d bytes, %q, %v), want the input unchanged", len(out), mime, err)
	}
}

func TestFitRefusesContentThatIsNotAnImage(t *testing.T) {
	if _, _, err := Fit([]byte("definitely not an image"), "image/png"); !errors.Is(err, ErrUnreadable) {
		t.Errorf("err = %v, want ErrUnreadable", err)
	}
}

func TestClampRefusesUnreadableContentOverTheByteLimit(t *testing.T) {
	junk := bytes.Repeat([]byte("x"), MaxImageBytes+1)
	if _, _, ok := Clamp(junk, "image/webp"); ok {
		t.Error("unreadable content over MaxImageBytes must not be sent (ok=false)")
	}
}

var jevLikeBounds = Bounds{MinSide: 256, MaxSide: 1568, MinPixels: 78_400}

func TestFitBoundsReturnsAnImageWithinBoundsUnchanged(t *testing.T) {
	src := pngBytes(t, 400, 300)
	data, mimeType, err := FitBounds(src, "image/png", jevLikeBounds)
	if err != nil {
		t.Fatalf("FitBounds: %v", err)
	}
	if mimeType != "image/png" || !bytes.Equal(data, src) {
		t.Errorf("got %s, %d bytes; want the PNG unchanged", mimeType, len(data))
	}
}

func TestFitBoundsBringsAnImageWithinBounds(t *testing.T) {
	cases := []struct {
		name         string
		src          []byte
		mime         string
		wantMIME     string
		wantW, wantH int
	}{
		{"scales a small image up to the minimum pixels", pngBytes(t, 100, 100), "image/png", "image/png", 280, 280},
		{"scales a large JPEG down and keeps it JPEG", jpegBytes(t, 4000, 1000), "image/jpeg", "image/jpeg", 1568, 392},
		{"pads a long strip to the minimum side", pngBytes(t, 3136, 100), "image/png", "image/png", 1568, 256},
		{"scales up until the short side reaches the minimum", pngBytes(t, 1000, 200), "image/png", "image/png", 1280, 256},
		{"scales a large PNG down", pngBytes(t, 3000, 2000), "image/png", "image/png", 1568, 1045},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, mimeType, err := FitBounds(tc.src, tc.mime, jevLikeBounds)
			if err != nil {
				t.Fatalf("FitBounds: %v", err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if mimeType != tc.wantMIME || cfg.Width != tc.wantW || cfg.Height != tc.wantH {
				t.Errorf("got %s %dx%d, want %s %dx%d", mimeType, cfg.Width, cfg.Height, tc.wantMIME, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestFitBoundsPadsATransparentImageWithWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3136, 100))
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	data, _, err := FitBounds(buf.Bytes(), "image/png", jevLikeBounds)
	if err != nil {
		t.Fatalf("FitBounds: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if r, g, b, a := img.At(0, 0).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
		t.Errorf("padding pixel = %v, want opaque white", img.At(0, 0))
	}
}

func pngChunk(kind string, data []byte) []byte {
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(data)))
	chunk.WriteString(kind)
	chunk.Write(data)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	return chunk.Bytes()
}

func withChunkAfterIHDR(pngData, chunk []byte) []byte {
	const signatureAndIHDR = 8 + 25
	out := append([]byte{}, pngData[:signatureAndIHDR]...)
	out = append(out, chunk...)
	return append(out, pngData[signatureAndIHDR:]...)
}

func TestFitBoundsReencodesAnAnimatedPNGWithinBounds(t *testing.T) {
	acTL := pngChunk("acTL", make([]byte, 8))
	for name, animated := range map[string][]byte{
		"acTL after IHDR": withChunkAfterIHDR(pngBytes(t, 400, 300), acTL),
		"acTL after a text chunk that spells IDAT": withChunkAfterIHDR(
			withChunkAfterIHDR(pngBytes(t, 400, 300), acTL), pngChunk("tEXt", []byte("note\x00IDAT here"))),
	} {
		data, mimeType, err := FitBounds(animated, "image/png", jevLikeBounds)
		if err != nil {
			t.Fatalf("%s: FitBounds: %v", name, err)
		}
		if mimeType != "image/png" || bytes.Equal(data, animated) || bytes.Contains(data, []byte("acTL")) {
			t.Errorf("%s: got %s, unchanged=%v; want a re-encoded still PNG without acTL", name, mimeType, bytes.Equal(data, animated))
		}
	}
}

func TestFitBoundsKeepsAPNGWhoseAcTLComesAfterItsImageData(t *testing.T) {
	src := pngBytes(t, 400, 300)
	const iendSize = 12
	late := append(append(append([]byte{}, src[:len(src)-iendSize]...), pngChunk("acTL", make([]byte, 8))...), src[len(src)-iendSize:]...)
	data, _, err := FitBounds(late, "image/png", jevLikeBounds)
	if err != nil {
		t.Fatalf("FitBounds: %v", err)
	}
	if !bytes.Equal(data, late) {
		t.Errorf("a PNG with acTL after IDAT is not animated, want it unchanged")
	}
}

func TestFitBoundsReencodesAnImageOverMaxBytes(t *testing.T) {
	src := jpegBytes(t, 400, 300)
	bounds := jevLikeBounds
	bounds.MaxBytes = len(src) - 1
	data, mimeType, err := FitBounds(src, "image/jpeg", bounds)
	if err != nil {
		t.Fatalf("FitBounds: %v", err)
	}
	if mimeType != "image/jpeg" || bytes.Equal(data, src) {
		t.Errorf("got %s, unchanged=%v; want the JPEG re-encoded", mimeType, bytes.Equal(data, src))
	}
}

func TestFitBoundsReportsWhyAnImageCannotBeFitted(t *testing.T) {
	if _, _, err := FitBounds([]byte("not an image"), "image/png", jevLikeBounds); !errors.Is(err, ErrUnreadable) {
		t.Errorf("unreadable content: err = %v, want ErrUnreadable", err)
	}
	if _, _, err := FitBounds(pngHeaderOnly(10_000, 10_000), "image/png", jevLikeBounds); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a 100-megapixel image: err = %v, want ErrTooLarge", err)
	}
}
