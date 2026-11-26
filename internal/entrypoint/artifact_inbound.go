// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type ArtifactScope struct {
	AppName   string
	UserID    string
	SessionID string
}

func ConvertInlineFilePartsToArtifacts(
	ctx context.Context,
	msg *a2a.Message,
	artSvc *artifact.ScopedService,
	scope ArtifactScope,
	logger *slog.Logger,
) (droppedFiles []string) {
	kept := msg.Parts[:0]
	for i := range msg.Parts {
		p := msg.Parts[i]
		if !isInlineFilePart(p) {
			kept = append(kept, p)
			continue
		}
		uri, name, ok := saveInlineFilePart(ctx, p, artSvc, scope, i, logger)
		if !ok {
			droppedFiles = append(droppedFiles, name)
			continue
		}
		p.File.URI = uri
		p.File.Bytes = ""
		kept = append(kept, p)
	}
	msg.Parts = kept
	return droppedFiles
}

func isInlineFilePart(p a2a.Part) bool {
	return p.Kind == "file" && p.File != nil && p.File.Bytes != "" && p.File.URI == ""
}

func AppendDroppedFilesNote(msg *a2a.Message, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	msg.Parts = append(msg.Parts, a2a.NewTextPart(fmt.Sprintf(
		"Note: %d uploaded file(s) could not be processed and were not included: %s.",
		len(dropped), strings.Join(dropped, ", "))))
}

type ArtifactUpload struct {
	Filename    string
	MIMEType    string
	Content     []byte
	Description string
	DeriveMIME  bool
}

const scopeSelectorPrefix = "user:"

func stripScopeSelector(filename string) (string, bool) {
	stripped := filename
	for strings.HasPrefix(stripped, scopeSelectorPrefix) {
		stripped = strings.TrimPrefix(stripped, scopeSelectorPrefix)
	}
	return stripped, stripped != filename
}

const maxInferredUploadBytes = 1 << 20

func tooLargeToInferSchema(upload ArtifactUpload) bool {
	if len(upload.Content) <= maxInferredUploadBytes {
		return false
	}
	mimeType := upload.MIMEType
	if upload.DeriveMIME {
		mimeType = artifact.ResolveDataMIME(upload.Filename, mimeType, upload.Content)
	}
	return artifact.IsJSONMIME(mimeType) || artifact.IsYAMLMIME(mimeType)
}

func SaveBytesAsArtifact(
	ctx context.Context,
	artSvc *artifact.ScopedService,
	scope ArtifactScope,
	upload ArtifactUpload,
	logger *slog.Logger,
) (uri string, ok bool) {
	if stripped, had := stripScopeSelector(upload.Filename); had {
		logger.Warn("stripped an artifact scope selector from a client-supplied filename",
			"filename", upload.Filename, "storedAs", stripped)
		upload.Filename = stripped
	}
	uri, _, err := artifact.SaveUpload(ctx, artSvc,
		artifact.UploadScope{AgentName: scope.AppName, UserID: scope.UserID, SessionID: scope.SessionID},
		artifact.Upload{
			Filename:   upload.Filename,
			MIMEType:   upload.MIMEType,
			Content:    upload.Content,
			DeriveMIME: upload.DeriveMIME,
		},
		artifact.MetadataOpts{
			Description:    upload.Description,
			Source:         "gateway_upload",
			Tags:           []string{a2a.ArtifactTagUserUploaded},
			SuppressSchema: tooLargeToInferSchema(upload),
		},
		logger,
	)
	switch {
	case errors.Is(err, artifact.ErrNoArtifactService):
		logger.Warn("cannot save artifact: no artifact service configured", "filename", upload.Filename)
		return "", false
	case errors.Is(err, artifact.ErrUnsafeFilename):
		logger.Warn("refusing to save artifact under unsafe or reserved filename", "filename", upload.Filename)
		return "", false
	case err != nil:
		logger.Error("artifact save failed", "filename", upload.Filename, "error", err)
		return "", false
	}
	return uri, true
}

func saveInlineFilePart(
	ctx context.Context,
	p a2a.Part,
	artSvc *artifact.ScopedService,
	scope ArtifactScope,
	index int,
	logger *slog.Logger,
) (uri, name string, ok bool) {
	filename := p.File.Name
	if filename == "" {
		filename = fmt.Sprintf("upload_%d", index)
	}

	content, err := base64.StdEncoding.DecodeString(p.File.Bytes)
	if err != nil {
		logger.Warn("dropping inline file part: base64 decode failed", "filename", filename, "error", err)
		return "", filename, false
	}

	uri, ok = SaveBytesAsArtifact(ctx, artSvc, scope,
		ArtifactUpload{Filename: filename, MIMEType: p.File.MIMEType, Content: content, DeriveMIME: true}, logger)
	return uri, filename, ok
}
