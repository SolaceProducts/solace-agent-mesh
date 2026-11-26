// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
)

type ExpectedGoogleChatCall struct {
	Method          string       `yaml:"method"`
	TextContains    StringOrList `yaml:"text_contains"`
	TextNotContains StringOrList `yaml:"text_not_contains"`
	Status          int          `yaml:"status"`
}

type ExpectedGoogleChatMessage struct {
	TextContains    StringOrList `yaml:"text_contains"`
	TextNotContains StringOrList `yaml:"text_not_contains"`
	MinEdits        int          `yaml:"min_edits"`
}

type ExpectedGoogleChatDriveFile struct {
	Name            string       `yaml:"name"`
	MIMEType        string       `yaml:"mime_type"`
	ContentContains StringOrList `yaml:"content_contains"`
	SharedWith      string       `yaml:"shared_with"`
	ExpiresInDays   int          `yaml:"expires_in_days"`
	Deleted         bool         `yaml:"deleted"`
	Trashed         bool         `yaml:"trashed"`
	HasTaskID       bool         `yaml:"has_task_id"`
}

func (sc *Scenario) validateGoogleChatDriveExpectations() error {
	expectsFiles := len(sc.ExpectedGoogleChatDriveFiles) > 0
	expectsDrive := expectsFiles || sc.ExpectNoGoogleChatDriveFile || sc.ExpectedGoogleChatDriveFileCount != nil
	if expectsFiles && sc.ExpectNoGoogleChatDriveFile {
		return fmt.Errorf("expected_googlechat_drive_files and expect_no_googlechat_drive_file contradict each other")
	}
	if sc.ExpectedGoogleChatDriveFileCount != nil && sc.ExpectNoGoogleChatDriveFile && *sc.ExpectedGoogleChatDriveFileCount != 0 {
		return fmt.Errorf("expected_googlechat_drive_file_count %d contradicts expect_no_googlechat_drive_file", *sc.ExpectedGoogleChatDriveFileCount)
	}
	if expectsDrive && (sc.GoogleChatConfig == nil || !sc.GoogleChatConfig.ArtifactDrive) {
		return fmt.Errorf("a Drive expectation needs googlechat_config.artifact_drive: true")
	}
	if want := sc.ExpectedGoogleChatDriveFileCount; want != nil {
		if *want < 0 {
			return fmt.Errorf("expected_googlechat_drive_file_count %d is negative", *want)
		}
		if *want < len(sc.ExpectedGoogleChatDriveFiles) {
			return fmt.Errorf("expected_googlechat_drive_file_count %d is below the %d file(s) in expected_googlechat_drive_files", *want, len(sc.ExpectedGoogleChatDriveFiles))
		}
	}
	for i, f := range sc.ExpectedGoogleChatDriveFiles {
		if f.Trashed && !f.Deleted {
			return fmt.Errorf("expected_googlechat_drive_files[%d] is trashed but not deleted, which can never match", i)
		}
		if f.ExpiresInDays != 0 && f.SharedWith == "" {
			return fmt.Errorf("expected_googlechat_drive_files[%d] sets expires_in_days without shared_with, so it would be ignored", i)
		}
	}
	return nil
}

const googleChatAssertWait = 5 * time.Second

const googleChatQuietWindow = 400 * time.Millisecond

func (h *TestHarness) GoogleChatGateway() *GoogleChatTestGateway {
	gw, _ := h.gateway.(*GoogleChatTestGateway)
	return gw
}

func (h *TestHarness) AssertGoogleChat(t *testing.T) {
	t.Helper()
	gw := h.GoogleChatGateway()
	driveExpected := len(h.scenario.ExpectedGoogleChatDriveFiles) > 0 || h.scenario.ExpectNoGoogleChatDriveFile || h.scenario.ExpectedGoogleChatDriveFileCount != nil
	if gw == nil || (len(h.scenario.ExpectedGoogleChatCalls) == 0 && len(h.scenario.ExpectedGoogleChatMessages) == 0 && !driveExpected && len(h.scenario.NoGoogleChatMessageContains) == 0) {
		return
	}
	deadline := time.Now().Add(googleChatAssertWait)
	for {
		callsOK := matchGoogleChatCalls(gw.MockServer().Calls(), h.scenario.ExpectedGoogleChatCalls) == len(h.scenario.ExpectedGoogleChatCalls)
		msgsOK := matchGoogleChatMessages(gw.MockServer().Messages(), h.scenario.ExpectedGoogleChatMessages) == len(h.scenario.ExpectedGoogleChatMessages)
		driveOK := matchGoogleChatDriveFiles(gw.MockServer().DriveFiles(), h.scenario.ExpectedGoogleChatDriveFiles) == len(h.scenario.ExpectedGoogleChatDriveFiles)
		if (callsOK && msgsOK && driveOK) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	gw.MockServer().WaitQuiet(googleChatQuietWindow, googleChatAssertWait)
	if unknown := gw.MockServer().UnknownDriveFileRequests(); len(unknown) > 0 {
		t.Errorf("the adapter sent Drive requests for a file id Drive never gave it: %q", unknown)
	}
	AssertGoogleChatCalls(t, gw.MockServer().Calls(), h.scenario.ExpectedGoogleChatCalls)
	AssertGoogleChatMessages(t, gw.MockServer().Messages(), h.scenario.ExpectedGoogleChatMessages)
	AssertGoogleChatDriveFiles(t, gw.MockServer().DriveFiles(), h.scenario.ExpectedGoogleChatDriveFiles, h.scenario.ExpectNoGoogleChatDriveFile)
	if want := h.scenario.ExpectedGoogleChatDriveFileCount; want != nil {
		if got := len(gw.MockServer().DriveFiles()); got != *want {
			t.Errorf("expected_googlechat_drive_file_count: %d file(s) were uploaded, want %d", got, *want)
		}
	}
	if left := gw.MockServer().UnconsumedDriveFailures(); len(left) > 0 {
		t.Errorf("drive_failures %v were scripted but no Drive call consumed them, so the scenario did not exercise them", left)
	}
	for _, m := range gw.MockServer().Messages() {
		for _, banned := range h.scenario.NoGoogleChatMessageContains {
			if strings.Contains(m.Text, banned) {
				t.Errorf("no_googlechat_message_contains %q: message %s holds it: %q", banned, m.Name, truncateStr(m.Text, 200))
			}
		}
	}
}

func AssertGoogleChatDriveFiles(t *testing.T, actual []MockDriveFile, expected []ExpectedGoogleChatDriveFile, none bool) {
	t.Helper()
	if none && len(actual) > 0 {
		t.Errorf("expect_no_googlechat_drive_file: %d file(s) were uploaded, the first named %q", len(actual), actual[0].Name)
	}
	if matched := matchGoogleChatDriveFiles(actual, expected); matched < len(expected) {
		for i, f := range actual {
			t.Logf("  drive file [%d] %s name=%q mime=%q parents=%v shared=%v deleted=%v", i, f.ID, f.Name, f.MIMEType, f.Parents, f.Permissions, f.Deleted)
		}
		t.Errorf("expected_googlechat_drive_files[%d] (name=%q) matched no file", matched, expected[matched].Name)
	}
}

func matchGoogleChatDriveFiles(actual []MockDriveFile, expected []ExpectedGoogleChatDriveFile) int {
	pos := 0
	for i, exp := range expected {
		found := false
		for ; pos < len(actual); pos++ {
			if driveFileMatches(actual[pos], exp) {
				found = true
				pos++
				break
			}
		}
		if !found {
			return i
		}
	}
	return len(expected)
}

func driveFileMatches(f MockDriveFile, exp ExpectedGoogleChatDriveFile) bool {
	if (exp.Name != "" && f.Name != exp.Name) || (exp.MIMEType != "" && f.MIMEType != exp.MIMEType) || f.Deleted != exp.Deleted || f.Trashed != exp.Trashed {
		return false
	}
	if exp.HasTaskID && f.Properties[googlechat.AppPropertyTaskID] == "" {
		return false
	}
	if !textMatches(string(f.Content), exp.ContentContains, nil) || !slices.Contains(f.Parents, MockDriveFolderID) {
		return false
	}
	if exp.SharedWith == "" {
		return true
	}
	return slices.ContainsFunc(f.Permissions, func(p MockDrivePermission) bool {
		return p.Email == exp.SharedWith && p.Role == "reader" && p.Type == "user" && !p.Notified && grantExpiresIn(p.ExpirationTime, exp.ExpiresInDays)
	})
}

func grantExpiresIn(expiration string, days int) bool {
	at, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		return false
	}
	if days == 0 {
		return true
	}
	left := time.Until(at)
	return left > time.Duration(days-1)*24*time.Hour && left < time.Duration(days+1)*24*time.Hour
}

func AssertGoogleChatCalls(t *testing.T, actual []CapturedGoogleChatCall, expected []ExpectedGoogleChatCall) {
	t.Helper()
	if matched := matchGoogleChatCalls(actual, expected); matched < len(expected) {
		for i, c := range actual {
			t.Logf("  google chat call [%d] %s %s status=%d text=%q", i, c.Method, c.Message, c.Status, truncateStr(c.Text, 100))
		}
		t.Errorf("expected_googlechat_calls[%d] (method=%q text_contains=%v) matched no call", matched, expected[matched].Method, []string(expected[matched].TextContains))
	}
}

func AssertGoogleChatMessages(t *testing.T, actual []GoogleChatMessage, expected []ExpectedGoogleChatMessage) {
	t.Helper()
	if matched := matchGoogleChatMessages(actual, expected); matched < len(expected) {
		for i, m := range actual {
			t.Logf("  google chat message [%d] %s edits=%d text=%q", i, m.Name, m.Edits, truncateStr(m.Text, 100))
		}
		t.Errorf("expected_googlechat_messages[%d] (text_contains=%v) matched no message", matched, []string(expected[matched].TextContains))
	}
}

func matchGoogleChatCalls(actual []CapturedGoogleChatCall, expected []ExpectedGoogleChatCall) int {
	pos := 0
	for i, exp := range expected {
		found := false
		for ; pos < len(actual); pos++ {
			c := actual[pos]
			if exp.Method != "" && c.Method != exp.Method {
				continue
			}
			if (exp.Status == 0 && c.Status != 200) || (exp.Status != 0 && c.Status != exp.Status) {
				continue
			}
			if textMatches(c.Text, exp.TextContains, exp.TextNotContains) {
				found = true
				pos++
				break
			}
		}
		if !found {
			return i
		}
	}
	return len(expected)
}

func matchGoogleChatMessages(actual []GoogleChatMessage, expected []ExpectedGoogleChatMessage) int {
	pos := 0
	for i, exp := range expected {
		found := false
		for ; pos < len(actual); pos++ {
			m := actual[pos]
			if m.Edits >= exp.MinEdits && textMatches(m.Text, exp.TextContains, exp.TextNotContains) {
				found = true
				pos++
				break
			}
		}
		if !found {
			return i
		}
	}
	return len(expected)
}

func textMatches(text string, contains, notContains StringOrList) bool {
	for _, s := range contains {
		if !strings.Contains(text, s) {
			return false
		}
	}
	for _, s := range notContains {
		if strings.Contains(text, s) {
			return false
		}
	}
	return true
}

func (h *TestHarness) LoadArtifactURI(ctx context.Context, uri string) ([]byte, error) {
	loc, version, ok := artifact.ParseURI(uri)
	if !ok {
		return nil, fmt.Errorf("not an artifact URI: %q", uri)
	}
	data, _, err := h.artSvc.Inner.Load(ctx, loc, version)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", uri, err)
	}
	return data, nil
}

func (h *TestHarness) LLMRequestsContain(substr string) bool {
	for _, req := range h.llmClient.Recorder().Requests() {
		body, err := json.Marshal(req)
		if err == nil && strings.Contains(string(body), substr) {
			return true
		}
	}
	return false
}
