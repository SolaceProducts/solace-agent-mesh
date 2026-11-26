// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gotools_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	readOnly      = "read_only"
	sideEffecting = "side_effecting"
)

var reviewedEffects = map[string]string{
	"bamboohr_profile_provider/enrich_authenticated_user":       readOnly,
	"bamboohr_profile_provider/get_time_off":                    readOnly,
	"bamboohr_profile_provider/get_user_profile":                readOnly,
	"bamboohr_profile_provider/get_user_profile_picture":        sideEffecting,
	"bamboohr_profile_provider/search_users":                    readOnly,
	"bedrock-kb/query_bedrock_kb":                               readOnly,
	"build_apply/mark_changes_applied":                          sideEffecting,
	"concatenate_audio/concatenate_audio":                       sideEffecting,
	"configtest/add_numbers":                                    readOnly,
	"configtest/echo":                                           readOnly,
	"convert_to_markdown/convert_file_to_markdown":              sideEffecting,
	"dev_profile_provider/enrich_authenticated_user":            readOnly,
	"dev_profile_provider/get_user_profile":                     readOnly,
	"dev_profile_provider/search_users":                         readOnly,
	"document-db/document_db_query":                             sideEffecting,
	"docx/docx_create_document":                                 sideEffecting,
	"event-mesh/event_mesh_request":                             sideEffecting,
	"event_mesh_profile_provider/enrich_authenticated_user":     sideEffecting,
	"ffmpeg/ffmpeg":                                             sideEffecting,
	"ffmpeg/ffprobe":                                            readOnly,
	"graph-db/graph_db_query":                                   sideEffecting,
	"html_to_pdf/html_to_pdf":                                   sideEffecting,
	"imagemagick/image_magick":                                  sideEffecting,
	"libreoffice/libreoffice_convert":                           sideEffecting,
	"mermaid/mermaid_diagram_generator":                         sideEffecting,
	"multi_speaker_text_to_speech/multi_speaker_text_to_speech": sideEffecting,
	"pdf_extract_text/pdf_extract_text":                         readOnly,
	"pdf_ops/pdf_ops":                                           sideEffecting,
	"plotly/create_chart_from_plotly_config":                    sideEffecting,
	"pptx/pptx_create_presentation":                             sideEffecting,
	"render_to_images/render_document_to_images":                sideEffecting,
	"sam_coder/sam_coder":                                       readOnly,
	"search/search_query":                                       sideEffecting,
	"send_email/send_email":                                     sideEffecting,
	"skill/grep_skill_resources":                                readOnly,
	"skill/list_skill_resources":                                readOnly,
	"skill/load_skill":                                          readOnly,
	"skill/read_skill_resource":                                 readOnly,
	"slack/slack":                                               sideEffecting,
	"slack/slack_update":                                        sideEffecting,
	"sql/execute_sql_query":                                     sideEffecting,
	"test_harness/generate_recommendations":                     sideEffecting,
	"test_harness/generate_test_plan":                           sideEffecting,
	"text_to_speech/text_to_speech":                             sideEffecting,
	"volume_fs/volume_append":                                   sideEffecting,
	"volume_fs/volume_delete":                                   sideEffecting,
	"volume_fs/volume_edit":                                     sideEffecting,
	"volume_fs/volume_glob":                                     readOnly,
	"volume_fs/volume_grep":                                     readOnly,
	"volume_fs/volume_list":                                     readOnly,
	"volume_fs/volume_mkdir":                                    sideEffecting,
	"volume_fs/volume_move":                                     sideEffecting,
	"volume_fs/volume_read":                                     readOnly,
	"volume_fs/volume_stat":                                     readOnly,
	"volume_fs/volume_write":                                    sideEffecting,
	"web_search/web_search":                                     readOnly,
	"xlsx/xlsx_read":                                            readOnly,
	"xlsx/xlsx_write":                                           sideEffecting,
}

var mainsThatAreNotTools = map[string]bool{"pptx/test_direct": true}

func TestEveryGoToolDeclaresItsReviewedEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every tool binary")
	}
	binDir := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", binDir, "./...").CombinedOutput(); err != nil {
		t.Fatalf("go build ./tools/go/...: %v\n%s", err, out)
	}

	declared := map[string]string{}
	for _, dir := range toolMainDirs(t) {
		if mainsThatAreNotTools[dir] {
			continue
		}
		for name, effect := range schemaEffects(t, filepath.Join(binDir, filepath.Base(dir))) {
			declared[dir+"/"+name] = effect
		}
	}

	for key, got := range declared {
		want, listed := reviewedEffects[key]
		if !listed {
			t.Errorf("%s declares %q but is missing from reviewedEffects; add it so the classification is reviewed", key, got)
			continue
		}
		if got != want {
			t.Errorf("%s: effect %q, want %q", key, got, want)
		}
	}
	for key := range reviewedEffects {
		if _, ok := declared[key]; !ok {
			t.Errorf("reviewedEffects lists %q, which no tool under tools/go declares", key)
		}
	}
}

func toolMainDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "main.go" {
			return err
		}
		dirs = append(dirs, filepath.ToSlash(filepath.Dir(path)))
		return nil
	})
	if err != nil {
		t.Fatalf("walking tools/go: %v", err)
	}
	return dirs
}

func schemaEffects(t *testing.T, binary string) map[string]string {
	t.Helper()
	cmd := exec.Command(binary, "--schema")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s --schema: %v", binary, err)
	}
	var doc struct {
		Tools map[string]struct {
			Effect string `json:"effect"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("%s --schema output: %v", binary, err)
	}
	effects := make(map[string]string, len(doc.Tools))
	for name, tool := range doc.Tools {
		effects[name] = tool.Effect
	}
	return effects
}
