// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZIPRejection_V1KeepsTheReaderText(t *testing.T) {
	t.Parallel()
	notAZIP := []byte("not a zip")
	for _, tc := range []struct {
		name         string
		validate     func() error
		wantV1       string
		wantSentence string
	}{
		{
			name:         "toolset not a ZIP",
			validate:     func() error { _, err := validateAndDetectRuntime(notAZIP); return err },
			wantV1:       "invalid ZIP file: zip: not a valid zip file",
			wantSentence: "invalid ZIP file",
		},
		{
			name:         "skill not a ZIP",
			validate:     func() error { _, err := validateSkillZIP(notAZIP, ""); return err },
			wantV1:       "invalid ZIP file: zip: not a valid zip file",
			wantSentence: "invalid ZIP file",
		},
		{
			name: "skill frontmatter not YAML",
			validate: func() error {
				_, err := validateSkillZIP(buildSkillZIPWithContent(t, "---\nname: [unclosed\n---\n"), "")
				return err
			},
			wantV1:       "parsing SKILL.md frontmatter: malformed frontmatter YAML: ",
			wantSentence: "SKILL.md frontmatter is not valid YAML",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rejection := zipRejection(tc.validate())

			valErr, ok := errors.AsType[*ValidationError](rejection)
			if !ok || valErr.Field != fieldPackageFile || valErr.Message != tc.wantSentence {
				t.Fatalf("rejection = %#v, want the sentence %q under %q", valErr, tc.wantSentence, fieldPackageFile)
			}

			rec := httptest.NewRecorder()
			handleServiceError(rec, rejection)
			var body errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode v1 body: %v", err)
			}
			if rec.Code != http.StatusUnprocessableEntity || !strings.HasPrefix(body.Message, tc.wantV1) {
				t.Errorf("v1 = %d %q, want 422 starting %q", rec.Code, body.Message, tc.wantV1)
			}
		})
	}
}
