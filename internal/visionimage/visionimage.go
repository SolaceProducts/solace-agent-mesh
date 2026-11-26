// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visionimage

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const MaxDim = 8000

const MaxDecodePixels = 40_000_000

const MaxImageBytes = 3_750_000

const (
	mimeJPEG = "image/jpeg"
	mimePNG  = "image/png"

	encodeDownscaledFormat = "encode downscaled image: %w"
	decodeImageFormat      = "decode image: %w"
)

var jpegQualities = []int{85, 70, 55}

const (
	shrinkStep = 0.75
	maxShrinks = 10
)

const (
	byteBudgetMargin = 0.9
	farOverLimit     = 1.5
)

const (
	DefaultMaxImages = 5
	DefaultMaxBytes  = 20 << 20
)

var ErrTooLarge = errors.New("image is too large to downscale safely")

var ErrUnreadable = errors.New("image header cannot be read")

func unreadable(err error) error {
	return fmt.Errorf("%w: %w", ErrUnreadable, err)
}

func Clamp(content []byte, mimeType string) ([]byte, string, bool) {
	data, fittedType, err := Fit(content, mimeType)
	if errors.Is(err, ErrUnreadable) {
		return content, mimeType, len(content) <= MaxImageBytes
	}
	return data, fittedType, err == nil
}

func Fit(content []byte, mimeType string) ([]byte, string, error) {
	cfg, _, err := visionFormats.decodeConfig(content)
	if err != nil {
		return nil, "", unreadable(err)
	}
	if cfg.Width <= MaxDim && cfg.Height <= MaxDim && len(content) <= MaxImageBytes {
		return content, mimeType, nil
	}
	img, format, err := decodeWithinPixelCap(visionFormats, content, cfg)
	if err != nil {
		return nil, "", err
	}
	scale := min(1, float64(MaxDim)/float64(max(cfg.Width, cfg.Height)))
	if scale < 1 && format != "jpeg" {
		var buf bytes.Buffer
		if err := png.Encode(&buf, resize(img, scale, nil)); err != nil {
			return nil, "", fmt.Errorf(encodeDownscaledFormat, err)
		}
		if buf.Len() <= MaxImageBytes {
			return buf.Bytes(), mimePNG, nil
		}
	}
	if format == "jpeg" {
		scale = min(scale, byteBudgetScale(len(content)))
	}
	return fitJPEG(img, scale)
}

func fitJPEG(img image.Image, scale float64) ([]byte, string, error) {
	for range maxShrinks {
		data, bestQualitySize, err := encodeJPEGWithinLimit(resize(img, scale, image.White))
		if err != nil {
			return nil, "", err
		}
		if data != nil {
			return data, mimeJPEG, nil
		}
		scale *= min(shrinkStep, byteBudgetScale(bestQualitySize))
	}
	return nil, "", fmt.Errorf("image does not fit in %d bytes", MaxImageBytes)
}

func encodeJPEGWithinLimit(frame image.Image) ([]byte, int, error) {
	bestQualitySize := 0
	for _, quality := range jpegQualities {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: quality}); err != nil {
			return nil, 0, fmt.Errorf(encodeDownscaledFormat, err)
		}
		if buf.Len() <= MaxImageBytes {
			return buf.Bytes(), 0, nil
		}
		if bestQualitySize == 0 {
			bestQualitySize = buf.Len()
		}
		if float64(buf.Len()) > farOverLimit*MaxImageBytes {
			break
		}
	}
	return nil, bestQualitySize, nil
}

func byteBudgetScale(size int) float64 {
	return byteBudgetMargin * math.Sqrt(float64(MaxImageBytes)/float64(size))
}

func resize(img image.Image, scale float64, background image.Image) *image.RGBA {
	bounds := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0,
		max(1, int(float64(bounds.Dx())*scale)),
		max(1, int(float64(bounds.Dy())*scale)),
	))
	if background != nil {
		draw.Draw(dst, dst.Bounds(), background, image.Point{}, draw.Src)
	}
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}

func Downscale(content []byte, mimeType string) ([]byte, string, error) {
	cfg, _, err := stillImageFormats.decodeConfig(content)
	if err != nil {
		return nil, "", unreadable(err)
	}
	if cfg.Width <= MaxDim && cfg.Height <= MaxDim {
		return content, mimeType, nil
	}
	img, format, err := decodeWithinPixelCap(stillImageFormats, content, cfg)
	if err != nil {
		return nil, "", err
	}
	return encodeLikeSource(resize(img, float64(MaxDim)/float64(max(cfg.Width, cfg.Height)), nil), format)
}

func ToStillPNG(content []byte) ([]byte, error) {
	cfg, _, err := stillImageFormats.decodeConfig(content)
	if err != nil {
		return nil, unreadable(err)
	}
	img, _, err := decodeWithinPixelCap(stillImageFormats, content, cfg)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode image as PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func decodeWithinPixelCap(formats formatSet, content []byte, cfg image.Config) (image.Image, string, error) {
	if int64(cfg.Width)*int64(cfg.Height) > MaxDecodePixels {
		return nil, "", ErrTooLarge
	}
	img, format, err := formats.decode(content)
	if err != nil {
		return nil, "", fmt.Errorf(decodeImageFormat, err)
	}
	return img, format, nil
}

func encodeLikeSource(dst image.Image, sourceFormat string) ([]byte, string, error) {
	var buf bytes.Buffer
	if sourceFormat == "jpeg" {
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", fmt.Errorf(encodeDownscaledFormat, err)
		}
		return buf.Bytes(), mimeJPEG, nil
	}
	if err := png.Encode(&buf, dst); err != nil {
		return nil, "", fmt.Errorf(encodeDownscaledFormat, err)
	}
	return buf.Bytes(), mimePNG, nil
}

type Bounds struct {
	MinSide   int
	MaxSide   int
	MinPixels int
	MaxBytes  int
}

func FitBounds(content []byte, mimeType string, b Bounds) ([]byte, string, error) {
	cfg, format, err := stillImageFormats.decodeConfig(content)
	if err != nil {
		return nil, "", unreadable(err)
	}
	scaledW, scaledH := scaledSize(cfg.Width, cfg.Height, b)
	canvasW, canvasH := paddedSize(scaledW, scaledH, b)
	sameSize := scaledW == cfg.Width && scaledH == cfg.Height && canvasW == scaledW && canvasH == scaledH
	withinBytes := b.MaxBytes == 0 || len(content) <= b.MaxBytes
	if sameSize && withinBytes && (format == "jpeg" || format == "png" && !isAnimatedPNG(content)) {
		return content, "image/" + format, nil
	}
	img, _, err := decodeWithinPixelCap(stillImageFormats, content, cfg)
	if err != nil {
		return nil, "", err
	}
	dst := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	op := draw.Src
	if canvasW != scaledW || canvasH != scaledH {
		draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
		op = draw.Over
	}
	offset := image.Pt((canvasW-scaledW)/2, (canvasH-scaledH)/2)
	draw.ApproxBiLinear.Scale(dst, image.Rectangle{Min: offset, Max: offset.Add(image.Pt(scaledW, scaledH))}, img, img.Bounds(), op, nil)
	return encodeLikeSource(dst, format)
}

func isAnimatedPNG(content []byte) bool {
	const signatureSize, chunkOverhead = 8, 12
	for pos := signatureSize; pos+8 <= len(content); {
		length := int64(binary.BigEndian.Uint32(content[pos:]))
		switch string(content[pos+4 : pos+8]) {
		case "acTL":
			return true
		case "IDAT":
			return false
		}
		next := int64(pos) + chunkOverhead + length
		if next > int64(len(content)) {
			return false
		}
		pos = int(next)
	}
	return false
}

func scaledSize(width, height int, b Bounds) (int, int) {
	longer, shorter := max(width, height), min(width, height)
	ceiling := float64(b.MaxSide) / float64(longer)
	scale := 1.0
	if longer > b.MaxSide {
		scale = ceiling
	} else if up := max(float64(b.MinSide)/float64(shorter), math.Sqrt(float64(b.MinPixels)/(float64(width)*float64(height)))); up > 1 {
		scale = min(up, ceiling)
	}
	if scale == 1 {
		return width, height
	}
	fit := func(side int) int { return min(b.MaxSide, max(1, int(math.Round(float64(side)*scale)))) }
	return fit(width), fit(height)
}

func paddedSize(width, height int, b Bounds) (int, int) {
	width, height = max(width, b.MinSide), max(height, b.MinSide)
	if width*height >= b.MinPixels {
		return width, height
	}
	if width <= height {
		return (b.MinPixels + height - 1) / height, height
	}
	return width, (b.MinPixels + width - 1) / width
}

func DataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func NormalizeMIME(mimeType string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
}

var renderableMIMEs = map[string]bool{
	mimePNG: true, mimeJPEG: true, "image/jpg": true,
	"image/webp": true, "image/gif": true,
}

func Renderable(mimeType string) bool {
	return renderableMIMEs[NormalizeMIME(mimeType)]
}
