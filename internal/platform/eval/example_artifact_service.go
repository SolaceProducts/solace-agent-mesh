// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ExampleArtifactService struct {
	examples    *store.EvalExampleRepo
	rows        *store.EvalExampleArtifactRepo
	artifacts   *artifact.ScopedService
	fileDeletes *FileDeletes
	cfg         ExampleArtifactConfig
	logger      *slog.Logger
}

type ExampleArtifactServiceParams struct {
	Examples     *store.EvalExampleRepo
	ArtifactRows *store.EvalExampleArtifactRepo
	Artifacts    *artifact.ScopedService
	FileDeletes  *FileDeletes
	Config       ExampleArtifactConfig
	Logger       *slog.Logger
}

func NewExampleArtifactService(p ExampleArtifactServiceParams) *ExampleArtifactService {
	logger := p.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if p.Artifacts != nil && p.Artifacts.Scope != "namespace" {
		logger.Warn("eval example artifacts need a namespace-scoped artifact service; uploads will not resolve at run time",
			"artifactScope", p.Artifacts.Scope)
	}
	return &ExampleArtifactService{
		examples:    p.Examples,
		rows:        p.ArtifactRows,
		artifacts:   p.Artifacts,
		fileDeletes: p.FileDeletes,
		cfg:         p.Config,
		logger:      logger,
	}
}

const (
	CodeExampleArtifactTooLarge = "EXAMPLE_ARTIFACT_TOO_LARGE"
	CodeExpectedArtifactNotText = "EXPECTED_ARTIFACT_NOT_TEXT"
	CodeDatasetArtifactTotalCap = "DATASET_ARTIFACT_TOTAL_BYTES_CAP_REACHED"
)

func (s *ExampleArtifactService) MaxBytes() int64 { return s.cfg.MaxBytes }

func validateArtifactRole(role string) error {
	if role == store.EvalArtifactRoleInput || role == store.EvalArtifactRoleExpected {
		return nil
	}
	return &ValidationError{
		Field:   "role",
		Message: fmt.Sprintf("role %q is not valid; use %q or %q", role, store.EvalArtifactRoleInput, store.EvalArtifactRoleExpected),
	}
}

func (s *ExampleArtifactService) Set(
	ctx context.Context,
	exampleID, role, filename, headerMIME string,
	content []byte,
	description string,
) (*ExampleArtifactResponse, error) {
	if err := s.requireArtifactService(); err != nil {
		return nil, err
	}
	if err := validateArtifactRole(role); err != nil {
		return nil, err
	}
	example, err := s.examples.GetByID(ctx, exampleID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalExample", exampleID)
	}
	if _, userScoped := artifact.ResolveFilename(filename); userScoped || !artifact.IsFilenameSafe(filename) || artifact.IsInternalArtifact(filename) {
		return nil, &ValidationError{
			Field:   "file",
			Message: fmt.Sprintf("filename %q is unsafe or reserved", filename),
		}
	}
	size := int64(len(content))
	if size > s.cfg.MaxBytes {
		return nil, &BusinessRuleViolationError{
			Code: CodeExampleArtifactTooLarge,
			Message: fmt.Sprintf("file is %d bytes; the limit is %d bytes",
				size, s.cfg.MaxBytes),
		}
	}
	mimeType := artifact.ResolveDataMIME(filename, headerMIME, content)
	if !s.mimeAllowed(mimeType) {
		return nil, &ValidationError{
			Field:   "file",
			Message: fmt.Sprintf("content type %q is not permitted", mimeType),
		}
	}
	if role == store.EvalArtifactRoleExpected && !IsTextLike(mimeType, filename) {
		return nil, &BusinessRuleViolationError{
			Code: CodeExpectedArtifactNotText,
			Message: fmt.Sprintf("expected files must be text (markdown, JSON, CSV, etc.) so evaluators can read them; %q resolves to %q",
				filename, mimeType),
		}
	}

	hash := contentHash(content)
	current := example.ArtifactByRole(role)
	if unchanged := existingArtifactMatching(current, filename, hash); unchanged != nil {
		return unchanged, nil
	}

	if capErr := s.checkDatasetTotal(ctx, example, role, size); capErr != nil {
		return nil, capErr
	}

	sessionID := evalExampleArtifactSessionID(exampleID, role, idgen.New())
	if _, uriErr := artifact.FormatURI(s.artifacts.Resolve("", EvalRunnerUserID, sessionID, filename), 0); uriErr != nil {
		s.logger.Warn("eval: artifact_service namespace cannot address example input files",
			"exampleID", exampleID, "error", uriErr)
		return nil, &BusinessRuleViolationError{
			Code:    "EXAMPLE_ARTIFACT_UNROUTABLE_NAMESPACE",
			Message: "the configured artifact_service namespace cannot address example input files",
		}
	}

	location := store.EvalExampleFile{SessionID: sessionID, Filename: filename}
	if s.fileDeletes != nil {
		if err = s.rows.RecordFiles(ctx, []store.EvalExampleFile{location}, uploadEvent(location)); err != nil {
			return nil, fmt.Errorf("record example artifact upload for example %s: %w", exampleID, err)
		}
	}

	_, version, err := artifact.SaveUpload(ctx, s.artifacts,
		artifact.UploadScope{UserID: EvalRunnerUserID, SessionID: sessionID},
		artifact.Upload{Filename: filename, MIMEType: mimeType, Content: content},
		artifact.MetadataOpts{
			Description: description,
			Source:      "eval_example_upload",
			Tags:        []string{a2a.ArtifactTagUserUploaded},
		},
		s.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("save example artifact %q for example %s: %w", filename, exampleID, err)
	}

	row := &store.EvalExampleArtifact{
		ExampleID:   exampleID,
		Role:        role,
		Filename:    filename,
		MIMEType:    mimeType,
		SizeBytes:   size,
		Version:     version,
		SessionID:   sessionID,
		ContentHash: hash,
	}
	if err := s.rows.Upsert(ctx, row, s.fileDeletes.event()); err != nil {
		return nil, mapEvalNotFound(err, "EvalExample", exampleID)
	}
	return &ExampleArtifactResponse{
		Filename:    filename,
		MimeType:    mimeType,
		Size:        size,
		Version:     version,
		ContentHash: hash,
	}, nil
}

func (s *ExampleArtifactService) Get(
	ctx context.Context,
	exampleID, role, filename string,
	version *int,
) (content []byte, mimeType string, err error) {
	if svcErr := s.requireArtifactService(); svcErr != nil {
		return nil, "", svcErr
	}
	if roleErr := validateArtifactRole(role); roleErr != nil {
		return nil, "", roleErr
	}
	example, err := s.examples.GetByID(ctx, exampleID)
	if err != nil {
		return nil, "", mapEvalNotFound(err, "EvalExample", exampleID)
	}
	notFound := &NotFoundError{Resource: "EvalExampleArtifact", ID: filename}
	row := example.ArtifactByRole(role)
	if row == nil || row.Filename != filename {
		return nil, "", notFound
	}
	loadVersion := row.Version
	if version != nil && *version != loadVersion {
		return nil, "", notFound
	}

	loc := s.artifacts.Resolve("", EvalRunnerUserID, row.SessionID, filename)
	bytes, loadedMIME, loadErr := s.artifacts.Inner.Load(ctx, loc, &loadVersion)
	if loadErr != nil {
		return nil, "", fmt.Errorf("load example artifact %q for example %s: %w", filename, exampleID, loadErr)
	}
	if bytes == nil {
		return nil, "", notFound
	}
	return bytes, cmp.Or(loadedMIME, row.MIMEType), nil
}

func (s *ExampleArtifactService) Clear(ctx context.Context, exampleID, role string) error {
	if err := s.requireArtifactService(); err != nil {
		return err
	}
	if err := validateArtifactRole(role); err != nil {
		return err
	}
	if err := s.rows.Delete(ctx, exampleID, role, s.fileDeletes.event()); err != nil {
		return mapEvalNotFound(err, "EvalExample", exampleID)
	}
	return nil
}

func (s *ExampleArtifactService) requireArtifactService() error {
	if s.artifacts != nil {
		return nil
	}
	return &BusinessRuleViolationError{
		Code:    "ARTIFACT_SERVICE_UNAVAILABLE",
		Message: "example input files require an artifact_service; none is configured on this platform",
	}
}

func (s *ExampleArtifactService) mimeAllowed(mimeType string) bool {
	if len(s.cfg.AllowedMIMETypes) == 0 {
		return true
	}
	return slices.Contains(s.cfg.AllowedMIMETypes, strings.ToLower(mimeType))
}

func (s *ExampleArtifactService) checkDatasetTotal(ctx context.Context, example *store.EvalExample, role string, incoming int64) error {
	total, err := s.rows.SumBytesByDataset(ctx, example.DatasetID)
	if err != nil {
		return fmt.Errorf("sum dataset artifact bytes: %w", err)
	}
	if current := example.ArtifactByRole(role); current != nil {
		total -= current.SizeBytes
	}
	if total+incoming > s.cfg.MaxTotalBytesPerDataset {
		return &BusinessRuleViolationError{
			Code: CodeDatasetArtifactTotalCap,
			Message: fmt.Sprintf("dataset input files would total %d bytes; the limit is %d bytes",
				total+incoming, s.cfg.MaxTotalBytesPerDataset),
		}
	}
	return nil
}

func existingArtifactMatching(row *store.EvalExampleArtifact, filename, hash string) *ExampleArtifactResponse {
	if row == nil || row.Filename != filename || row.ContentHash != hash {
		return nil
	}
	return exampleArtifactResponseFromRow(row)
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
