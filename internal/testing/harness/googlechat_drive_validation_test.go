// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"strings"
	"testing"
)

func TestDriveExpectationsAreValidated(t *testing.T) {
	driveOn := &GoogleChatTestConfig{ArtifactDrive: true}
	for name, tc := range map[string]struct {
		sc      Scenario
		wantErr string
	}{
		"nothing expected":                       {sc: Scenario{}},
		"a file with the drive on":               {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a"}}}},
		"a file without the drive on":            {sc: Scenario{GoogleChatConfig: &GoogleChatTestConfig{}, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a"}}}, wantErr: "needs googlechat_config.artifact_drive"},
		"files and none":                         {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a"}}, ExpectNoGoogleChatDriveFile: true}, wantErr: "contradict each other"},
		"a count and none, with a count of zero": {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFileCount: intPtr(0), ExpectNoGoogleChatDriveFile: true}},
		"a count and none, with a count of two":  {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFileCount: intPtr(2), ExpectNoGoogleChatDriveFile: true}, wantErr: "contradicts expect_no_googlechat_drive_file"},
		"a negative count":                       {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFileCount: intPtr(-1)}, wantErr: "is negative"},
		"a count below the files expected":       {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFileCount: intPtr(1), ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a"}, {Name: "b"}}}, wantErr: "is below the 2 file(s)"},
		"trashed but not deleted":                {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a", Trashed: true}}}, wantErr: "trashed but not deleted"},
		"an expiry without a person":             {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a", ExpiresInDays: 3}}}, wantErr: "without shared_with"},
		"an expiry with a person":                {sc: Scenario{GoogleChatConfig: driveOn, ExpectedGoogleChatDriveFiles: []ExpectedGoogleChatDriveFile{{Name: "a", ExpiresInDays: 3, SharedWith: "x@example.com"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.sc.validateGoogleChatDriveExpectations()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("validateGoogleChatDriveExpectations() = %v, want none", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("validateGoogleChatDriveExpectations() = nil, want an error saying %q", tc.wantErr)
			case err != nil && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error = %q, want it to say %q", err, tc.wantErr)
			}
		})
	}
}

func TestDriveConfigIsValidated(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     GoogleChatTestConfig
		wantErr string
	}{
		"no drive options":                  {cfg: GoogleChatTestConfig{}},
		"a known failure with the drive on": {cfg: GoogleChatTestConfig{ArtifactDrive: true, DriveFailures: []string{GoogleChatDriveFailDeleteError}}},
		"an unknown failure":                {cfg: GoogleChatTestConfig{ArtifactDrive: true, DriveFailures: []string{"nope"}}, wantErr: "is not one of"},
		"a failure with the drive off":      {cfg: GoogleChatTestConfig{DriveFailures: []string{GoogleChatDriveFailShare}}, wantErr: "would be ignored"},
		"a size limit with the drive off":   {cfg: GoogleChatTestConfig{DriveMaxArtifactBytes: 8}, wantErr: "would be ignored"},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("validate() = %v, want none", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("validate() = nil, want an error saying %q", tc.wantErr)
			case err != nil && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("error = %q, want it to say %q", err, tc.wantErr)
			}
		})
	}
}
