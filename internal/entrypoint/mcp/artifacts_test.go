// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import "testing"

func TestClassifyArtifact(t *testing.T) {
	cfg := mustParseConfig(t, nil)

	cases := []struct {
		name     string
		mimeType string
		size     int
		want     artifactClass
	}{
		{"small image inline", "image/png", 1024, classImage},
		{"large image link", "image/png", cfg.InlineImageMaxBytes + 1, classResourceLink},
		{"small audio inline", "audio/mpeg", 1024, classAudio},
		{"large audio link", "audio/mpeg", cfg.InlineAudioMaxBytes + 1, classResourceLink},
		{"small text embedded", "text/plain", 1024, classTextEmbedded},
		{"json text embedded", "application/json", 1024, classTextEmbedded},
		{"large text link", "text/plain", cfg.InlineTextMaxBytes + 1, classResourceLink},
		{"small binary blob", "application/pdf", 1024, classBlobEmbedded},
		{"large binary link", "application/pdf", cfg.InlineBinaryMaxBytes + 1, classResourceLink},
		{"empty mime treated as binary", "", 1024, classBlobEmbedded},
		{"pptx small blob", "application/vnd.openxmlformats-officedocument.presentationml.presentation", 2048, classBlobEmbedded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyArtifact(tc.mimeType, tc.size, cfg)
			if got != tc.want {
				t.Errorf("classifyArtifact(%q, %d) = %q, want %q", tc.mimeType, tc.size, got, tc.want)
			}
		})
	}
}
