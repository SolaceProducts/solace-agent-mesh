// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"bytes"
	"errors"
	"image/png"
	"strings"
	"testing"
)

func imageFile(name, mimeType string, data []byte) Media {
	return Media{Name: name, Kind: MediaImage, MIMEType: mimeType, Data: data}
}

func TestCheckStillImagesOnly(t *testing.T) {
	cases := map[string]struct {
		req      Request
		wantErr  string
		wantKind bool
	}{
		"png, gif, bmp and tiff": {req: Request{Media: []Media{
			imageFile("a.png", "image/png", nil), imageFile("b.gif", "image/gif", nil),
			imageFile("c.bmp", "image/bmp", nil), imageFile("d.tiff", "image/tiff", nil),
		}}},
		"no media":           {req: Request{}},
		"heic":               {req: Request{Media: []Media{imageFile("photo.heic", "image/heic", nil)}}, wantErr: `image "photo.heic"`, wantKind: true},
		"audio":              {req: Request{Media: []Media{{Name: "call.wav", Kind: MediaAudio, MIMEType: "audio/wav"}}}, wantErr: "does not read audio", wantKind: true},
		"a document":         {req: Request{Media: []Media{{Name: "a.pdf", Kind: MediaDocument, MIMEType: "application/pdf"}}}, wantErr: "does not read documents", wantKind: true},
		"a resolution":       {req: Request{ImageResolution: "high"}, wantErr: "image_resolution"},
		"a video resolution": {req: Request{VideoResolution: "low"}, wantErr: "video_resolution"},
		"heif":               {req: Request{Media: []Media{imageFile("x.heif", "image/heif", nil)}}, wantErr: "image/heif", wantKind: true},
		"named in error":     {req: Request{Media: []Media{imageFile("y.heic", "image/heic", nil)}}, wantErr: `"gpt-6-luna"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckStillImagesOnly(tc.req, "gpt-6-luna")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if tc.wantKind && !errors.Is(err, ErrMediaUnsupported) {
				t.Errorf("err = %v, want it to wrap ErrMediaUnsupported", err)
			}
			if kind := EncodeFailureKind(err); kind != FailureConfig {
				t.Errorf("EncodeFailureKind = %q, want %q", kind, FailureConfig)
			}
		})
	}
}

func TestToStillConvertsGIFBMPAndTIFFToPNG(t *testing.T) {
	jpegData := []byte("jpeg-bytes")
	in := []Media{
		imageFile("a.gif", "image/gif", fixture(t, "image.gif")),
		imageFile("b.bmp", "image/bmp", fixture(t, "image.bmp")),
		imageFile("c.tiff", "image/tiff", fixture(t, "image.tiff")),
		imageFile("d.jpg", "image/jpeg", jpegData),
	}
	out, err := ToStill(in, 0)
	if err != nil {
		t.Fatalf("ToStill: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d images, want %d", len(out), len(in))
	}
	for _, img := range out[:3] {
		if img.MIMEType != "image/png" || img.Kind != MediaImage {
			t.Errorf("%s: type %s, kind %s; want image/png, image", img.Name, img.MIMEType, img.Kind)
		}
		if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
			t.Errorf("%s: not a PNG: %v", img.Name, err)
		}
	}
	if out[3].MIMEType != "image/jpeg" || !bytes.Equal(out[3].Data, jpegData) {
		t.Errorf("the JPEG changed: %s, %d bytes", out[3].MIMEType, len(out[3].Data))
	}
	if &out[0] == &in[0] || in[0].MIMEType != "image/gif" {
		t.Error("ToStill changed its input slice")
	}
}

func TestToStillRefusesWhatCannotBeSent(t *testing.T) {
	gif := imageFile("a.gif", "image/gif", fixture(t, "image.gif"))
	_, err := ToStill([]Media{gif}, 10)
	if tooLarge, ok := errors.AsType[*ImagesTooLargeError](err); !ok || !strings.Contains(tooLarge.Error(), "max_image_bytes") {
		t.Errorf("over the byte limit after conversion: err = %v, want ImagesOverBudget", err)
	}
	bomb := imageFile("bomb.gif", "image/gif", []byte("GIF89a\xff\xff\xff\xff\x00\x00\x00;"))
	_, err = ToStill([]Media{bomb}, 0)
	if kind := EncodeFailureKind(err); kind != FailureInputTooLarge {
		t.Errorf("declared dimensions past the pixel cap: kind %q (%v), want %q", kind, err, FailureInputTooLarge)
	}
	for _, unreadable := range []Media{
		imageFile("x.bmp", "image/bmp", []byte("BM not a bitmap")),
		imageFile("scan.tiff", "image/tiff", fixture(t, "image_4bit.tiff")),
	} {
		_, err = ToStill([]Media{unreadable}, 0)
		if err == nil || !strings.Contains(err.Error(), `"`+unreadable.Name+`"`) || EncodeFailureKind(err) != FailureConfig {
			t.Errorf("%s the decoder cannot read: err = %v, want a configuration error naming it", unreadable.Name, err)
		}
	}
}
