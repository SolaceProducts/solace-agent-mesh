// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func savedFile(name string, version int, tags ...string) entrypoint.SavedArtifact {
	return entrypoint.SavedArtifact{Filename: name, MIMEType: "text/csv", Version: &version, Tags: tags}
}

func candidateVersions(files []entrypoint.SavedArtifact) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = fmt.Sprintf("%s@%d", f.Filename, *f.Version)
	}
	return out
}

func TestDriveCandidates(t *testing.T) {
	six := make([]entrypoint.SavedArtifact, 0, 6)
	sixNames := make([]string, 0, 6)
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("f%d.csv", i)
		six = append(six, savedFile(name, 0))
		sixNames = append(sixNames, name)
	}
	for name, tc := range map[string]struct {
		saved []entrypoint.SavedArtifact
		names []string
		want  []string
	}{
		"a file saved again is one file in its last version": {
			saved: []entrypoint.SavedArtifact{savedFile("a.csv", 0), savedFile("b.csv", 0), savedFile("a.csv", 1)},
			names: []string{"a.csv", "b.csv"},
			want:  []string{"a.csv@1", "b.csv@0"},
		},
		"a file whose last save is a working draft is skipped": {
			saved: []entrypoint.SavedArtifact{savedFile("a.csv", 0), savedFile("a.csv", 1, a2a.ArtifactTagWorking)},
			names: []string{"a.csv"},
			want:  nil,
		},
		"a file that became final after a working draft is kept": {
			saved: []entrypoint.SavedArtifact{savedFile("a.csv", 0, a2a.ArtifactTagWorking), savedFile("a.csv", 1)},
			names: []string{"a.csv"},
			want:  []string{"a.csv@1"},
		},
		"a file the caller did not name is skipped": {
			saved: []entrypoint.SavedArtifact{savedFile("a.csv", 0), savedFile("b.csv", 0)},
			names: []string{"b.csv"},
			want:  []string{"b.csv@0"},
		},
		"no more than the per-task limit": {
			saved: six,
			names: sixNames,
			want:  []string{"f1.csv@0", "f2.csv@0", "f3.csv@0", "f4.csv@0", "f5.csv@0"},
		},
		"nothing saved": {},
	} {
		t.Run(name, func(t *testing.T) {
			got := candidateVersions(driveCandidates(tc.saved, tc.names))
			if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Errorf("driveCandidates = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDriveErrorFieldsNeverCarryTheErrorText(t *testing.T) {
	const secret = "tester@example.com secret-file.csv"
	apiErr := &googleapi.Error{Code: 403, Message: secret, Errors: []googleapi.ErrorItem{{Reason: "insufficientFilePermissions", Message: secret}}}
	for name, tc := range map[string]struct {
		err  error
		want []any
	}{
		"api error":     {&driveStageError{stage: "share", err: apiErr}, []any{"stage", "share", "httpStatus", 403, "reason", "insufficientFilePermissions"}},
		"timeout":       {&driveStageError{stage: "upload", err: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded)}, []any{"stage", "upload", "cause", "timed out"}},
		"canceled":      {&driveStageError{stage: "upload", err: context.Canceled}, []any{"stage", "upload", "cause", "canceled"}},
		"transport":     {&driveStageError{stage: "upload", err: &url.Error{Op: "Post", URL: "https://x/?email=" + secret, Err: errors.New(secret)}}, []any{"stage", "upload", "cause", "request failed", "op", "Post", "errorType", "*errors.errorString"}},
		"anything else": {&driveStageError{stage: "read", err: errors.New(secret)}, []any{"stage", "read", "cause", "failed", "errorType", "*errors.errorString"}},
		"wrapped other": {&driveStageError{stage: "read", err: fmt.Errorf("outer %s: %w", secret, errors.New(secret))}, []any{"stage", "read", "cause", "failed", "errorType", "*errors.errorString"}},
		"trash failed": {&driveStageError{stage: "remove", err: &trashFailedError{deleteErr: errors.New(secret), trashErr: errors.New(secret)}},
			[]any{"stage", "remove", "cause", "failed", "errorType", "*errors.errorString", "trashCause", "failed", "trashErrorType", "*errors.errorString"}},
		"not a stage":    {errors.New(secret), []any{"stage", "unknown", "cause", "failed", "errorType", "*errors.errorString"}},
		"wrapped staged": {fmt.Errorf("outer %s: %w", secret, &driveStageError{stage: "share", err: apiErr}), []any{"stage", "share", "httpStatus", 403, "reason", "insufficientFilePermissions"}},
	} {
		t.Run(name, func(t *testing.T) {
			got := driveErrorFields(tc.err)
			if !slices.Equal(got, tc.want) {
				t.Errorf("driveErrorFields = %v, want %v", got, tc.want)
			}
			if strings.Contains(fmt.Sprint(got...), "tester@") || strings.Contains(fmt.Sprint(got...), "secret-file") {
				t.Errorf("driveErrorFields leaked the error text: %v", got)
			}
		})
	}
}

func TestDriveMIMEType(t *testing.T) {
	for in, want := range map[string]string{
		"text/csv":                             "text/csv",
		" text/csv ":                           "text/csv",
		"":                                     defaultDriveMIMEType,
		"   ":                                  defaultDriveMIMEType,
		"application/vnd.google-apps.document": defaultDriveMIMEType,
		"Application/VND.Google-Apps.folder":   defaultDriveMIMEType,
		"application/vnd.google-apps.spreadsheet":   defaultDriveMIMEType,
		"application/vnd.openxmlformats-officedoc.": "application/vnd.openxmlformats-officedoc.",
	} {
		if got := driveMIMEType(in); got != want {
			t.Errorf("driveMIMEType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHiddenRune(t *testing.T) {
	for r, want := range map[rune]bool{
		'a': false, '.': false, ' ': false, 'é': false, '日': false,
		0x00: true, '\n': true, 0x7f: true,
		0x200b: true, 0x200e: true, 0x202e: true, 0x2066: true, 0xfeff: true,
	} {
		if got := hiddenRune(r); got != want {
			t.Errorf("hiddenRune(%U) = %v, want %v", r, got, want)
		}
	}
	override, zeroWidthSpace := string(rune(0x202e)), string(rune(0x200b))
	if got := driveFileName("report" + override + "gnp.exe"); got != "reportgnp.exe" {
		t.Errorf("driveFileName kept a right-to-left override: %q", got)
	}
	if got := driveLabel("re" + zeroWidthSpace + "port.pdf"); got != "report.pdf" {
		t.Errorf("driveLabel kept a zero-width space: %q", got)
	}
}

func TestParseConfig_DriveMaxArtifactBytes(t *testing.T) {
	loopback := map[string]any{
		"test_skip_google_auth": true, "api_base_url": "http://127.0.0.1:1", "pubsub_base_url": "http://127.0.0.1:1",
	}
	cfg, err := ParseConfig(driveConfig(nil))
	if err != nil || cfg.DriveMaxArtifactBytes != defaultMaxDriveArtifactBytes {
		t.Fatalf("default: limit = %d, err = %v", cfg.DriveMaxArtifactBytes, err)
	}

	for _, limit := range []any{8, defaultMaxDriveArtifactBytes} {
		raw := driveConfig(map[string]any{"test_drive_max_artifact_bytes": limit})
		if _, refused := ParseConfig(raw); refused == nil || !strings.HasPrefix(refused.Error(), configErrorPrefix) {
			t.Errorf("limit %v without skipped authentication: err = %v, want a google_chat config error: a test-only key is refused whatever its value", limit, refused)
		}
	}

	withSkip := driveConfig(map[string]any{"test_drive_max_artifact_bytes": 8})
	for k, v := range loopback {
		withSkip[k] = v
	}
	cfg, err = ParseConfig(withSkip)
	if err != nil || cfg.DriveMaxArtifactBytes != 8 {
		t.Errorf("a lowered limit with skipped authentication: limit = %d, err = %v", cfg.DriveMaxArtifactBytes, err)
	}

	for _, bad := range []any{0, -1, defaultMaxDriveArtifactBytes + 1, "big"} {
		raw := driveConfig(map[string]any{"test_drive_max_artifact_bytes": bad})
		for k, v := range loopback {
			raw[k] = v
		}
		if _, err := ParseConfig(raw); err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
			t.Errorf("limit %v: err = %v, want a google_chat config error", bad, err)
		}
	}
}

func TestDriveAccessDaysDefaultIsWithinTheAdapterRange(t *testing.T) {
	if DefaultArtifactDriveAccessDays < 1 || DefaultArtifactDriveAccessDays > MaxArtifactDriveAccessDays {
		t.Errorf("default %d is outside 1..%d", DefaultArtifactDriveAccessDays, MaxArtifactDriveAccessDays)
	}
}

func handOffInBackground(a *Adapter, work *driveHandOff) {
	a.goLinger(func() { a.handOffToDrive(work) })
}
