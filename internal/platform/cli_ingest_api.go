// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "context"

type CLIIngester interface {
	Ingest(ctx context.Context, batch CLIIngestBatch)
}

type CLIIngestBatch struct {
	OS         string
	Arch       string
	AppVersion string
	Events     []CLIIngestBatchEvent
}

type CLIIngestBatchEvent struct {
	EventID    string
	SessionID  string
	OccurredAt string
	Event      string
	Properties map[string]any
}

func (p *PlatformCore) CLIIngest() CLIIngester {
	return p.cliIngester
}
