// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !sam_lean

package embedded

import "testing"

func TestInProcessToolDefs_CoversAllConnectors(t *testing.T) {
	want := []string{
		"execute_sql_query",
		"query_bedrock_kb",
		"document_db_query",
		"graph_db_query",
		"search_query",
		"slack",
		"slack_update",
		"send_email",
		"mark_changes_applied",
		"concatenate_audio",
		"convert_file_to_markdown",
		"ffmpeg",
		"ffprobe",
		"image_magick",
		"libreoffice_convert",
		"mermaid_diagram_generator",
		"multi_speaker_text_to_speech",
		"pdf_extract_text",
		"pdf_ops",
		"pptx_create_presentation",
		"render_document_to_images",
		"text_to_speech",
	}

	got := map[string]bool{}
	for _, td := range inProcessToolDefs() {
		got[td.Name()] = true
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("in-process tool set missing %q (connector tool would hang in embedded mode)", name)
		}
	}
	if len(got) < len(want) {
		t.Errorf("registered %d in-process tools, want at least %d", len(got), len(want))
	}
}
