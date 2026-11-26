// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

func decodeImage(data []byte) (format string, ratio float64, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, fmt.Errorf("image is not a decodable PNG, JPEG, or GIF: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, fmt.Errorf("image reports zero dimensions (%dx%d)", cfg.Width, cfg.Height)
	}
	return format, float64(cfg.Height) / float64(cfg.Width), nil
}
