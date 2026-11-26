// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visionimage

import (
	"bytes"
	"fmt"
	"image"
	"slices"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
)

type formatSet []string

var (
	visionFormats     = formatSet{"png", "jpeg", "gif", "webp"}
	stillImageFormats = formatSet{"png", "jpeg", "gif", "webp", "bmp", "tiff"}
)

func (s formatSet) decodeConfig(content []byte) (image.Config, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return image.Config{}, format, err
	}
	if !slices.Contains(s, format) {
		return image.Config{}, format, fmt.Errorf("%w: %s is not read here", image.ErrFormat, format)
	}
	return cfg, format, nil
}

func (s formatSet) decode(content []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, format, err
	}
	if !slices.Contains(s, format) {
		return nil, format, fmt.Errorf("%w: %s is not read here", image.ErrFormat, format)
	}
	return img, format, nil
}
