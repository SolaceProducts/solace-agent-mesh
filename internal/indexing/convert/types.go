// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

type CitationRange struct {
	Location  string `json:"location"`
	CharStart int    `json:"char_start"`
	CharEnd   int    `json:"char_end"`
}

type Result struct {
	Text         string
	CitationType string
	Citations    []CitationRange
}
