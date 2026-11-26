// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func (h *TestHarness) setupArtifacts(ctx context.Context) error {
	for _, sa := range h.scenario.SetupArtifacts {
		agentName := "test_agent"
		if sa.AppName != "" {
			agentName = sa.AppName
		}
		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" && sa.AppName == "" {
			agentName = h.scenario.GatewayInput.TargetAgentName
		}

		userID := h.effectiveUserID()
		sessionID := "test-session"
		if sa.SessionID != "" {
			sessionID = sa.SessionID
		} else if h.scenario.GatewayInput != nil {
			if h.scenario.GatewayInput.ExternalContext != nil && h.scenario.GatewayInput.ExternalContext.A2ASessionID != "" {
				sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
			}
		}

		loc := h.artSvc.Resolve(agentName, userID, sessionID, sa.Filename)
		mimeType := sa.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		var contentBytes []byte
		if sa.ContentBase64 != "" {
			var decErr error
			contentBytes, decErr = base64.StdEncoding.DecodeString(sa.ContentBase64)
			if decErr != nil {
				cleaned := cleanBase64(sa.ContentBase64)
				contentBytes, decErr = base64.StdEncoding.DecodeString(cleaned)
				if decErr != nil {
					contentBytes, decErr = base64.RawStdEncoding.DecodeString(cleaned)
					if decErr != nil {
						return fmt.Errorf("decode content_base64 for %q: %w", sa.Filename, decErr)
					}
				}
			}
		} else {
			contentBytes = []byte(sa.Content)
		}

		version, err := h.artSvc.Inner.Save(ctx, loc, contentBytes, mimeType)
		if err != nil {
			return fmt.Errorf("save artifact %q: %w", sa.Filename, err)
		}

		meta := &artifact.VersionMetadata{
			MIMEType:  mimeType,
			SizeBytes: int64(len(contentBytes)),
		}
		if sa.Metadata != nil {
			meta.Custom = sa.Metadata
			if desc, ok := sa.Metadata["description"].(string); ok {
				meta.Description = desc
			}
		}
		if err := h.artSvc.Inner.SaveMetadata(ctx, loc, version, meta); err != nil {
			return fmt.Errorf("save metadata for %q: %w", sa.Filename, err)
		}

		fsUserID := ""
		switch h.GatewayMode {
		case "python-http", "python-enterprise-http":
			fsUserID = userID
		case "go-http", "go-enterprise-http":
			fsUserID = userID
		}
		if fsUserID != "" {
			fsBasePath := filepath.Join(os.TempDir(), "sam-test", "artifacts")
			appName := h.artSvc.Namespace
			artDir := filepath.Join(fsBasePath, appName, fsUserID, sessionID, sa.Filename)
			if err := os.MkdirAll(artDir, 0o755); err != nil {
				return fmt.Errorf("create artifact dir %q: %w", artDir, err)
			}
			versionPath := filepath.Join(artDir, fmt.Sprintf("%d", version))
			if err := os.WriteFile(versionPath, contentBytes, 0o644); err != nil {
				return fmt.Errorf("write artifact data %q: %w", versionPath, err)
			}
			metaPath := filepath.Join(artDir, fmt.Sprintf("%d.meta", version))
			metaJSON, marshalErr := json.Marshal(map[string]any{
				"mime_type":   mimeType,
				"size_bytes":  len(contentBytes),
				"description": meta.Description,
			})
			if marshalErr != nil {
				return fmt.Errorf("marshal artifact metadata for %q: %w", sa.Filename, marshalErr)
			}
			if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
				return fmt.Errorf("write artifact metadata %q: %w", metaPath, err)
			}
		}
	}
	return nil
}

func cleanBase64(s string) string {
	if idx := strings.LastIndex(s, "=="); idx >= 0 {
		return s[:idx+2]
	}
	if idx := strings.LastIndex(s, "="); idx >= 0 {
		return s[:idx+1]
	}
	const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	i := 0
	for i < len(s) && strings.ContainsRune(b64chars, rune(s[i])) {
		i++
	}
	s = s[:i]
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return s
}

func (h *TestHarness) AssertArtifactState(t *testing.T, expected []ExpectedEvent) {
	t.Helper()
	if h.artSvc == nil {
		return
	}

	agentName := "test_agent"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
		agentName = h.scenario.GatewayInput.TargetAgentName
	}
	userID := h.effectiveUserID()
	sessionID := "session_" + h.scenario.TestCaseID
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ExternalContext != nil &&
		h.scenario.GatewayInput.ExternalContext.A2ASessionID != "" {
		sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
	}

	for _, exp := range expected {
		assertArtifactList(t, h.artSvc, agentName, userID, sessionID, "assert_artifact_state", exp.AssertArtifactState)
	}
	assertArtifactList(t, h.artSvc, agentName, userID, sessionID, "expected_artifacts", h.scenario.ExpectedArtifacts)
}

func assertArtifactList(t *testing.T, artSvc *artifact.ScopedService, agentName, userID, sessionID, label string, items []ArtifactAssertion) {
	t.Helper()
	if artSvc == nil || len(items) == 0 {
		return
	}

	ctx := context.Background()
	scope := artifact.KeyScope{AppName: artSvc.Namespace, UserID: userID, SessionID: sessionID}
	if artSvc.Scope != "namespace" {
		scope.AppName = agentName
	}

	for j, aa := range items {
		prefix := fmt.Sprintf("%s[%d]", label, j)

		var matchedFilename string
		if aa.FilenameMatchesRegex != "" {
			re, err := regexp.Compile(aa.FilenameMatchesRegex)
			if err != nil {
				t.Errorf("%s: invalid filename regex %q: %v", prefix, aa.FilenameMatchesRegex, err)
				continue
			}
			keys, err := artSvc.Inner.ListKeys(ctx, scope)
			if err != nil {
				t.Errorf("%s: list keys: %v", prefix, err)
				continue
			}
			for _, k := range keys {
				if re.MatchString(k) {
					matchedFilename = k
					break
				}
			}
			if matchedFilename == "" {
				t.Errorf("%s: no artifact matching regex %q (found: %v)", prefix, aa.FilenameMatchesRegex, keys)
				continue
			}
		} else if aa.Filename != "" {
			matchedFilename = aa.Filename
		} else {
			continue
		}

		loc := artSvc.Resolve(agentName, userID, sessionID, matchedFilename)
		versionPtr := &aa.Version

		needsContent := aa.ExpectedContentText != "" || len(aa.ContentContains) > 0 ||
			len(aa.JSONContains) > 0 || aa.ExpectedContentBytesB64 != "" || aa.ContentBase64Exact != ""
		var contentBytes []byte
		if needsContent {
			loaded, _, err := artSvc.Inner.Load(ctx, loc, versionPtr)
			if err != nil {
				t.Errorf("%s: load artifact %q: %v", prefix, matchedFilename, err)
				continue
			}
			contentBytes = loaded
		}

		if aa.ExpectedContentText != "" && string(contentBytes) != aa.ExpectedContentText {
			t.Errorf("%s: content mismatch for %q\n  expected: %s\n  got:      %s",
				prefix, matchedFilename,
				truncate(aa.ExpectedContentText, 200),
				truncate(string(contentBytes), 200))
		}

		for _, substr := range aa.ContentContains {
			if !strings.Contains(string(contentBytes), substr) {
				t.Errorf("%s: artifact %q content does not contain %q\n  content (first 500 chars): %s",
					prefix, matchedFilename, substr, truncate(string(contentBytes), 500))
			}
		}

		if len(aa.JSONContains) > 0 {
			if err := MatchJSONContains(contentBytes, aa.JSONContains); err != nil {
				t.Errorf("%s: artifact %q JSON mismatch: %v\n  content (first 500 chars): %s",
					prefix, matchedFilename, err, truncate(string(contentBytes), 500))
			}
		}

		if aa.ExpectedContentBytesB64 != "" || aa.ContentBase64Exact != "" {
			expectedB64 := aa.ExpectedContentBytesB64
			if expectedB64 == "" {
				expectedB64 = aa.ContentBase64Exact
			}
			expectedBytes, err := base64.StdEncoding.DecodeString(expectedB64)
			if err != nil {
				trimmed := strings.TrimRight(expectedB64, "=")
				expectedBytes, err = base64.RawStdEncoding.DecodeString(trimmed)
				if err != nil {
					t.Errorf("%s: decode expected base64: %v", prefix, err)
					continue
				}
			}
			if string(contentBytes) != string(expectedBytes) {
				t.Errorf("%s: binary content mismatch for %q (length expected=%d, got=%d)",
					prefix, matchedFilename, len(expectedBytes), len(contentBytes))
			}
		}

		metaExpected := aa.ExpectedMetadataContains
		if len(metaExpected) == 0 {
			metaExpected = aa.MetadataContains
		}
		if len(metaExpected) > 0 {
			metaMap := loadArtifactMetadata(ctx, artSvc, agentName, userID, sessionID, matchedFilename, versionPtr)
			if metaMap == nil {
				t.Errorf("%s: no metadata for %q", prefix, matchedFilename)
				continue
			}
			for key, expectedVal := range metaExpected {
				actualVal, ok := metaMap[key]
				if !ok {
					t.Errorf("%s: metadata key %q not found for %q", prefix, key, matchedFilename)
					continue
				}
				if fmt.Sprintf("%v", actualVal) != fmt.Sprintf("%v", expectedVal) {
					t.Errorf("%s: metadata %q: expected %v, got %v", prefix, key, expectedVal, actualVal)
				}
			}
		}

		if len(aa.ExpectedTags) > 0 {
			tagMeta := loadArtifactMetadata(ctx, artSvc, agentName, userID, sessionID, matchedFilename, versionPtr)
			if tagMeta == nil {
				t.Errorf("%s: no metadata for %q (needed for tag assertion)", prefix, matchedFilename)
			} else {
				assertArtifactTags(t, prefix, matchedFilename, tagMeta, aa.ExpectedTags)
			}
		}
	}
}

func assertArtifactTags(t *testing.T, prefix, filename string, metaMap map[string]any, expectedTags []string) {
	t.Helper()
	var actualTags []string
	if rawTags, ok := metaMap["tags"]; ok && rawTags != nil {
		switch v := rawTags.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					actualTags = append(actualTags, s)
				}
			}
		case []string:
			actualTags = v
		}
	}
	if len(actualTags) != len(expectedTags) {
		t.Errorf("%s: artifact %q tags: got %v, want %v", prefix, filename, actualTags, expectedTags)
		return
	}
	for i, want := range expectedTags {
		if i >= len(actualTags) || actualTags[i] != want {
			t.Errorf("%s: artifact %q tag[%d]: got %q, want %q", prefix, filename, i, actualTags[i], want)
		}
	}
}

func loadArtifactMetadata(ctx context.Context, artSvc *artifact.ScopedService, agentName, userID, sessionID, filename string, version *int) map[string]any {
	companion := artifact.LoadCompanionMetadata(ctx, artSvc, agentName, userID, sessionID, filename, version)
	if companion != nil {
		return companion
	}
	loc := artSvc.Resolve(agentName, userID, sessionID, filename)
	meta, err := artSvc.Inner.GetVersionMetadata(ctx, loc, version)
	if err != nil || meta == nil || meta.Custom == nil {
		return nil
	}
	return meta.Custom
}
