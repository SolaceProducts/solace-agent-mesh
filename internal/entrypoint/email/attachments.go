// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func sanitizeAttachmentFilename(raw string) string {
	cleaned := strings.ReplaceAll(raw, "\x00", "")
	cleaned = filepath.Base(strings.ReplaceAll(cleaned, `\`, "/"))
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" || cleaned == "." || cleaned == ".." || cleaned == "/" {
		return "attachment"
	}
	return cleaned
}

func attachmentsToFileParts(
	ctx context.Context,
	attachments []Attachment,
	artSvc *artifact.ScopedService,
	userID, sessionID string,
	logger *slog.Logger,
) (parts []a2a.Part, ok bool) {
	out := make([]a2a.Part, 0, len(attachments))
	for i := range attachments {
		if err := ctx.Err(); err != nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "attachments aborted: context cancelled",
				slog.String("eventType", "email.attachments_aborted"),
				slog.String("reason", "context_cancelled"),
				slog.Int("processed", i),
				slog.Int("total", len(attachments)),
			)
			return nil, false
		}
		att := &attachments[i]
		mimeType := att.DetectedMIMEType
		if mimeType == "" {
			mimeType = att.DeclaredMIMEType
		}
		safeName := sanitizeAttachmentFilename(att.Filename)

		if att.Content.ArtifactURI != "" {
			out = append(out, a2a.NewFilePart(att.Content.ArtifactURI, safeName, mimeType))
			continue
		}

		if artSvc == nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "attachment dropped: no artifact service",
				slog.String("eventType", "email.attachment_dropped"),
				slog.String("extension", strings.ToLower(filepath.Ext(safeName))),
			)
			continue
		}

		uri, ok := entrypoint.SaveBytesAsArtifact(
			ctx, artSvc,
			entrypoint.ArtifactScope{UserID: userID, SessionID: sessionID},
			entrypoint.ArtifactUpload{
				Filename:    safeName,
				MIMEType:    mimeType,
				Content:     att.Content.InlineBytes,
				Description: fmt.Sprintf("Email attachment: %s", safeName),
			},
			logger,
		)
		if !ok {
			logger.LogAttrs(ctx, slog.LevelError, "attachment save failed: aborting message",
				slog.String("eventType", "email.attachment_save_failed"),
				slog.String("extension", strings.ToLower(filepath.Ext(safeName))),
			)
			return nil, false
		}
		out = append(out, a2a.NewFilePart(uri, safeName, mimeType))

		logger.LogAttrs(ctx, slog.LevelInfo, "email attachment saved",
			slog.String("eventType", "email.attachment_saved"),
			slog.String("extension", strings.ToLower(filepath.Ext(safeName))),
			slog.String("mimeType", mimeType),
			slog.Int64("size_bytes", att.SizeBytes),
		)
	}
	return out, true
}
