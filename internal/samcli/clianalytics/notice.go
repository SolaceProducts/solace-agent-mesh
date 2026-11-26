// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const noticeMarkerFile = ".product-analytics-notice"

const noticeText = `Agent Mesh collects usage data to help improve the product. It is
anonymous until the SAM Desktop license is accepted, and linked to the
Solace ID account that accepted it after that. It is on unless you turn it
off. This setting applies to this installation.

  Turn off: sam analytics disable
  Turn on:  sam analytics enable
`

func (r *Reporter) ShowNoticeOnce(ctx context.Context, w io.Writer) {
	if !r.available || r.Consent(ctx) != productanalytics.ConsentUnset {
		return
	}
	if r.dataDir == "" {
		return
	}
	marker := filepath.Join(r.dataDir, noticeMarkerFile)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	if err := writeNoticeMarker(marker); err != nil {
		r.logger.Warn("product analytics notice marker not written", "error", productanalytics.SanitizedError(err))
		return
	}
	if _, err := fmt.Fprint(w, "\n"+noticeText+"\n"); err != nil {
		r.logger.Debug("product analytics notice not written", "error", productanalytics.SanitizedError(err))
		if rmErr := os.Remove(marker); rmErr != nil {
			r.logger.Debug("product analytics notice marker not released", "error", productanalytics.SanitizedError(rmErr))
		}
	}
}

func writeNoticeMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create notice marker: %w", err)
	}
	return f.Close()
}
