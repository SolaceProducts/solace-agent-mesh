// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var (
	errExampleArtifactUnavailable = errors.New("example artifact bytes unavailable")
	errExampleArtifactReplaced    = errors.New("example artifact bytes no longer match the recorded hash")
)

type exampleArtifactRecord struct {
	Filename        string `json:"filename"`
	Version         int    `json:"version"`
	MIMEType        string `json:"mime_type,omitempty"`
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	SessionID       string `json:"artifact_session_id,omitempty"`
	JudgeUnreadable bool   `json:"judge_unreadable,omitempty"`
}

func markJudgeUnreadable(result *store.EvalRunResult, role string, blocks []ArtifactBlock) {
	if !exampleBlockDegraded(blocks) {
		return
	}
	field := &result.InputArtifactMetadata
	if role == store.EvalArtifactRoleExpected {
		field = &result.ExpectedArtifactMetadata
	}
	if *field == nil {
		return
	}
	var rec exampleArtifactRecord
	if json.Unmarshal([]byte(**field), &rec) != nil {
		return
	}
	rec.JudgeUnreadable = true
	if body, err := json.Marshal(rec); err == nil {
		encoded := string(body)
		*field = &encoded
	}
}

func artifactRecordForExample(e *store.EvalExample, role string) *exampleArtifactRecord {
	if e == nil {
		return nil
	}
	row := e.ArtifactByRole(role)
	if row == nil || row.Filename == "" {
		return nil
	}
	return &exampleArtifactRecord{
		Filename:    row.Filename,
		Version:     row.Version,
		MIMEType:    row.MIMEType,
		SizeBytes:   row.SizeBytes,
		ContentHash: row.ContentHash,
		SessionID:   row.SessionID,
	}
}

func artifactMetadataForExample(e *store.EvalExample, role string, logger *slog.Logger) *string {
	rec := artifactRecordForExample(e, role)
	if rec == nil {
		return nil
	}
	body, err := json.Marshal(rec)
	if err != nil {
		if logger != nil {
			logger.Warn("eval: marshal example artifact metadata failed",
				"exampleID", e.ID, "role", role, "error", err)
		}
		return nil
	}
	out := string(body)
	return &out
}

func artifactFromMetadata(metaJSON *string, resultID string, logger *slog.Logger) *exampleArtifactRecord {
	if metaJSON == nil || *metaJSON == "" {
		return nil
	}
	var rec exampleArtifactRecord
	if err := json.Unmarshal([]byte(*metaJSON), &rec); err != nil {
		if logger != nil {
			logger.Warn("eval: corrupt example artifact metadata JSON; treating result as having no file",
				"error", err, "resultID", resultID)
		}
		return nil
	}
	if rec.Filename == "" {
		return nil
	}
	return &rec
}

func (s *RunService) GetRunResultInputArtifact(ctx context.Context, resultID, filename string, version *int) ([]byte, string, error) {
	return s.getRunResultExampleArtifact(ctx, resultID, filename, version,
		"EvalRunResultInputArtifact", store.EvalArtifactRoleInput)
}

func (s *RunService) GetRunResultExpectedArtifact(ctx context.Context, resultID, filename string, version *int) ([]byte, string, error) {
	return s.getRunResultExampleArtifact(ctx, resultID, filename, version,
		"EvalRunResultExpectedArtifact", store.EvalArtifactRoleExpected)
}

func (s *RunService) getRunResultExampleArtifact(
	ctx context.Context,
	resultID, filename string,
	version *int,
	resource, role string,
) (content []byte, mimeType string, err error) {
	notFound := &NotFoundError{Resource: resource, ID: filename}
	if s.artifacts == nil {
		return nil, "", notFound
	}
	row, err := s.results.GetByID(ctx, resultID)
	if err != nil {
		return nil, "", mapEvalNotFound(err, "EvalRunResult", resultID)
	}
	rec := artifactFromMetadata(row.ArtifactMetadataByRole(role), resultID, s.logger)
	if rec == nil || rec.Filename != filename {
		return nil, "", notFound
	}
	if version != nil && *version != rec.Version {
		return nil, "", notFound
	}

	bytes, loadedMime, loadErr := loadExampleArtifactBytes(ctx, s.artifacts, rec)
	switch {
	case errors.Is(loadErr, errExampleArtifactReplaced):
		s.logger.Debug("eval: example artifact no longer matches the bytes this trial saw",
			"resultID", resultID, "filename", filename, "version", rec.Version)
		return nil, "", notFound
	case errors.Is(loadErr, errExampleArtifactUnavailable):
		return nil, "", notFound
	case loadErr != nil:
		return nil, "", fmt.Errorf("load eval example artifact %q for result %s: %w", filename, resultID, loadErr)
	}

	return bytes, loadedMime, nil
}

func loadExampleArtifactBytes(ctx context.Context, artifacts *artifact.ScopedService, rec *exampleArtifactRecord) ([]byte, string, error) {
	if artifacts == nil || rec == nil || rec.SessionID == "" {
		return nil, "", errExampleArtifactUnavailable
	}
	loadVersion := rec.Version
	ref := artifacts.Resolve("", EvalRunnerUserID, rec.SessionID, rec.Filename)
	bytes, loadedMime, err := artifacts.Inner.Load(ctx, ref, &loadVersion)
	if err != nil {
		return nil, "", err
	}
	if bytes == nil {
		return nil, "", errExampleArtifactUnavailable
	}
	if rec.ContentHash != "" && contentHash(bytes) != rec.ContentHash {
		return nil, "", errExampleArtifactReplaced
	}
	return bytes, cmp.Or(loadedMime, rec.MIMEType), nil
}

func exampleArtifactRecordResponse(rec *exampleArtifactRecord) *ArtifactResponse {
	if rec == nil {
		return nil
	}
	return &ArtifactResponse{
		Filename: rec.Filename,
		MimeType: rec.MIMEType,
		Version:  rec.Version,
		Size:     rec.SizeBytes,
	}
}

func configSnapshotArtifact(e *store.EvalExample, role string) any {
	rec := artifactRecordForExample(e, role)
	if rec == nil {
		return nil
	}
	return rec
}

func artifactCSVCell(metaJSON *string, resultID string, logger *slog.Logger) string {
	rec := artifactFromMetadata(metaJSON, resultID, logger)
	if rec == nil {
		return ""
	}
	return fmt.Sprintf("%s@%d", rec.Filename, rec.Version)
}
