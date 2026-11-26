// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"errors"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

var convertedImageMIMETypes = []string{"image/gif", "image/bmp", "image/tiff"}

func StillImageMIMETypes() []string {
	return append(ImageMIMETypes(), convertedImageMIMETypes...)
}

func CheckStillImagesOnly(req Request, model string) error {
	for _, m := range req.Media {
		if m.Kind != MediaImage {
			return MediaUnsupportedError(m.Kind, model)
		}
		if !slices.Contains(StillImageMIMETypes(), m.MIMEType) {
			return fmt.Errorf("decision model %q does not read %s images, so image %q cannot be sent; use a Gemini decision model, or convert it to PNG or JPEG: %w",
				model, m.MIMEType, m.Name, ErrMediaUnsupported)
		}
	}
	if req.ImageResolution != "" {
		return fmt.Errorf("decision model %q does not take an image resolution; image_resolution is for Gemini decision models only", model)
	}
	if req.VideoResolution != "" {
		return fmt.Errorf("decision model %q does not take a video resolution; video_resolution is for Gemini decision models only", model)
	}
	return nil
}

func ToStill(images []Media, limit int64) ([]Media, error) {
	out := make([]Media, 0, len(images))
	var total int64
	for _, img := range images {
		if slices.Contains(convertedImageMIMETypes, img.MIMEType) {
			data, err := visionimage.ToStillPNG(img.Data)
			if errors.Is(err, visionimage.ErrTooLarge) {
				return nil, ImageTooLargeToDownscale(img.Name)
			}
			if err != nil {
				return nil, fmt.Errorf("convert image %q to PNG: %w", img.Name, err)
			}
			img = Media{Name: img.Name, Kind: MediaImage, MIMEType: "image/png", Data: data}
		}
		total += int64(len(img.Data))
		out = append(out, img)
	}
	if limit > 0 && total > limit {
		return nil, ImagesOverBudget(total, limit)
	}
	return out, nil
}
