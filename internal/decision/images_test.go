// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func encodedImage(t *testing.T, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSniffImage(t *testing.T) {
	pngData := encodedImage(t, func(b *bytes.Buffer, img image.Image) error { return png.Encode(b, img) })
	jpegData := encodedImage(t, func(b *bytes.Buffer, img image.Image) error { return jpeg.Encode(b, img, nil) })
	webpHeader := []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00")
	cases := map[string]struct {
		data     []byte
		wantMIME string
		wantOK   bool
	}{
		"png":            {data: pngData, wantMIME: "image/png", wantOK: true},
		"jpeg":           {data: jpegData, wantMIME: "image/jpeg", wantOK: true},
		"webp":           {data: webpHeader, wantMIME: "image/webp", wantOK: true},
		"gif":            {data: []byte("GIF89a\x01\x00\x01\x00"), wantMIME: "image/gif"},
		"svg is text":    {data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)},
		"json":           {data: []byte(`{"ticket":"x"}`)},
		"pdf":            {data: []byte("%PDF-1.7\n"), wantMIME: "application/pdf"},
		"empty":          {data: nil},
		"png named jpeg": {data: pngData, wantMIME: "image/png", wantOK: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mimeType, ok := SniffImage(tc.data)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (sniffed %q)", ok, tc.wantOK, mimeType)
			}
			if tc.wantMIME != "" && mimeType != tc.wantMIME {
				t.Errorf("mime = %q, want %q", mimeType, tc.wantMIME)
			}
		})
	}
}

func TestImageMIMETypesIsACopy(t *testing.T) {
	types := ImageMIMETypes()
	types[0] = "image/gif"
	if ImageMIMETypes()[0] == "image/gif" {
		t.Error("ImageMIMETypes returned the package's own slice")
	}
}

func TestImagesTooLargeErrorNamesTheCapItBroke(t *testing.T) {
	cases := map[string]struct {
		err  *ImagesTooLargeError
		want string
	}{
		"count":     {err: TooManyImages(6, 5), want: "6 images; the limit is 5 (set max_images"},
		"bytes":     {err: ImagesOverBudget(30, 20), want: "30 bytes after downscaling; the limit is 20 (set max_image_bytes"},
		"file":      {err: ImageFileTooLarge("raw.png", 30, 20), want: `"raw.png" is 30 bytes; the limit is 20`},
		"oversized": {err: ImageTooLargeToDownscale("scan.png"), want: `"scan.png" is too large to downscale`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if msg := tc.err.Error(); !strings.Contains(msg, tc.want) {
				t.Errorf("Error() = %q, want it to mention %q", msg, tc.want)
			}
			if kind := EncodeFailureKind(fmt.Errorf("wrapped: %w", tc.err)); kind != FailureInputTooLarge {
				t.Errorf("EncodeFailureKind = %s, want %s", kind, FailureInputTooLarge)
			}
		})
	}
}

func TestEncodeFailureKindTreatsUnsupportedMediaAsConfiguration(t *testing.T) {
	if kind := EncodeFailureKind(fmt.Errorf("typesafe: %w", ErrMediaUnsupported)); kind != FailureConfig {
		t.Errorf("EncodeFailureKind = %s, want %s", kind, FailureConfig)
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrMediaUnsupported), ErrMediaUnsupported) {
		t.Error("ErrMediaUnsupported does not survive wrapping")
	}
}
