// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"strings"
	"testing"
)

func driveConfig(extra map[string]any) map[string]any {
	raw := map[string]any{"delivery": "pubsub", "pubsub_subscription": testSubscription}
	for k, v := range extra {
		raw[k] = v
	}
	return raw
}

func TestParseConfig_DriveFilesStayInSAMAndAccessLastsAMonth(t *testing.T) {
	cfg, err := ParseConfig(driveConfig(nil))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ArtifactDriveFolderID != "" {
		t.Errorf("ArtifactDriveFolderID = %q, want none unless configured", cfg.ArtifactDriveFolderID)
	}
	if cfg.ArtifactDriveAccessDays != 30 || cfg.DriveAPIBaseURL != defaultDriveAPIBaseURL {
		t.Errorf("access days = %d, drive endpoint = %q", cfg.ArtifactDriveAccessDays, cfg.DriveAPIBaseURL)
	}
}

func TestParseConfig_DriveSettings(t *testing.T) {
	cfg, err := ParseConfig(driveConfig(map[string]any{
		"artifact_drive_folder_id":   "  0AExampleSharedDrive ",
		"artifact_drive_access_days": "14",
	}))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.ArtifactDriveFolderID != "0AExampleSharedDrive" || cfg.ArtifactDriveAccessDays != 14 {
		t.Errorf("folder = %q, access days = %d", cfg.ArtifactDriveFolderID, cfg.ArtifactDriveAccessDays)
	}
}

func TestParseConfig_DriveRejects(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"a folder id that is a link":    {"artifact_drive_folder_id": "https://drive.google.com/drive/folders/abc"},
		"a folder id that is too short": {"artifact_drive_folder_id": "abc"},
		"a folder id that is a number":  {"artifact_drive_folder_id": 12345678901234},
		"no days":                       {"artifact_drive_folder_id": "0AExampleSharedDrive", "artifact_drive_access_days": 0},
		"too many days":                 {"artifact_drive_folder_id": "0AExampleSharedDrive", "artifact_drive_access_days": 366},
		"days not a number":             {"artifact_drive_folder_id": "0AExampleSharedDrive", "artifact_drive_access_days": "a month"},
		"skip auth with a real drive": {
			"artifact_drive_folder_id": "0AExampleSharedDrive", "test_skip_google_auth": true,
			"api_base_url": "http://127.0.0.1:1", "pubsub_base_url": "http://127.0.0.1:1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(driveConfig(extra)); err == nil || !strings.HasPrefix(err.Error(), configErrorPrefix) {
				t.Errorf("ParseConfig(%v) err = %v, want a google_chat config error", extra, err)
			}
		})
	}
}

func TestParseConfig_DriveEdges(t *testing.T) {
	for _, days := range []any{1, 365} {
		if _, err := ParseConfig(driveConfig(map[string]any{"artifact_drive_folder_id": "0AExampleSharedDrive", "artifact_drive_access_days": days})); err != nil {
			t.Errorf("access days %v: %v", days, err)
		}
	}
	_, err := ParseConfig(driveConfig(map[string]any{
		"artifact_drive_folder_id": "0AExampleSharedDrive", "test_skip_google_auth": true,
		"api_base_url": "http://127.0.0.1:1", "pubsub_base_url": "http://127.0.0.1:1", "drive_base_url": "http://127.0.0.1:1/drive/v3",
	}))
	if err != nil {
		t.Errorf("a loopback drive with skipped authentication: %v", err)
	}
}

func TestDriveLabel(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":                      {"report.pdf", "report.pdf"},
		"link and format characters": {"a<b>|c*d_e~f`g.csv", "abcdefg.csv"},
		"control characters":         {"a\x00b\nc\x1b.txt", "abc.txt"},
		"nothing left":               {"<>|", "file"},
		"empty":                      {"", "file"},
		"padded":                     {"  report.pdf  ", "report.pdf"},
		"cut to the limit":           {strings.Repeat("é", 100) + ".pdf", strings.Repeat("é", maxDriveLabelRunes) + "…"},
		"at the limit":               {strings.Repeat("a", maxDriveLabelRunes), strings.Repeat("a", maxDriveLabelRunes)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := driveLabel(tc.in); got != tc.want {
				t.Errorf("driveLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDriveFileName(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":              {"report.pdf", "report.pdf"},
		"keeps marks":        {"a<b>|*.csv", "a<b>|*.csv"},
		"control characters": {"a\x00b\n.csv", "ab.csv"},
		"empty":              {"  ", "file"},
		"cut to drive limit": {strings.Repeat("é", 300), strings.Repeat("é", maxDriveFileNameRunes)},
	} {
		t.Run(name, func(t *testing.T) {
			if got := driveFileName(tc.in); got != tc.want {
				t.Errorf("driveFileName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsEmailAddress(t *testing.T) {
	for in, want := range map[string]bool{
		"person@example.com":          true,
		"first.last+tag@example.com":  true,
		"system:eval":                 false,
		"":                            false,
		"person":                      false,
		"Person <person@example.com>": false,
		"a@b@example.com":             false,
		" person@example.com":         false,
	} {
		if got := isEmailAddress(in); got != want {
			t.Errorf("isEmailAddress(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDriveNoticeIsOneLinkLabelledWithTheName(t *testing.T) {
	got := driveNotice("my <report>.pdf", "https://drive.google.com/file/d/x/view")

	const want = "📎 <https://drive.google.com/file/d/x/view|my report.pdf> is shared with you in Google Drive."
	if got != want {
		t.Errorf("driveNotice = %q, want %q", got, want)
	}
}
