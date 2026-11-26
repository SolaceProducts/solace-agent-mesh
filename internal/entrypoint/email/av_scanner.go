// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "context"

type ScannerVerdict string

const (
	ScanClean        ScannerVerdict = "clean"
	ScanInfected     ScannerVerdict = "infected"
	ScanInconclusive ScannerVerdict = "inconclusive"
)

type AttachmentScanner interface {
	Scan(ctx context.Context, filename string, body []byte) (ScannerVerdict, error)
}
