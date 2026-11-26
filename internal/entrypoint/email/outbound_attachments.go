// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func (a *EmailAdapter) buildOutboundAttachments(ctx context.Context, taskID string, captured []capturedArtifact) (atts []ReplyAttachment, dropAny bool) {
	if len(captured) == 0 {
		return nil, false
	}
	var artSvc *artifact.ScopedService
	if a.core != nil && a.core.Services() != nil {
		artSvc = a.core.Services().Artifacts()
	}
	var maxBytes int64
	if a.cfg != nil {
		maxBytes = a.cfg.MaxOutboundAttachmentSizeBytes
	}
	return buildOutboundAttachmentsImpl(ctx, captured, artSvc, maxBytes, a.logger, taskID)
}

func buildOutboundAttachmentsImpl(ctx context.Context, captured []capturedArtifact, artSvc *artifact.ScopedService, maxBytes int64, logger *slog.Logger, taskID string) (atts []ReplyAttachment, dropAny bool) {
	if len(captured) == 0 {
		return nil, false
	}

	if maxBytes == 0 {
		for _, c := range captured {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "outbound_attachments_disabled", -1)
			dropAny = true
		}
		return nil, dropAny
	}

	if artSvc == nil {
		for _, c := range captured {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "no_artifact_service", -1)
			dropAny = true
		}
		return nil, dropAny
	}

	var total int64
	for _, c := range captured {
		loc, version, err := parseArtifactURI(c.URI)
		if err != nil {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "uri_parse_error", -1)
			dropAny = true
			continue
		}
		var versionPtr *int
		if version > 0 {
			v := version
			versionPtr = &v
		}
		content, mime, err := artSvc.Inner.Load(ctx, loc, versionPtr)
		if err != nil {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "artifact_load_error", -1)
			dropAny = true
			continue
		}
		if content == nil {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "artifact_not_found", -1)
			dropAny = true
			continue
		}

		size := int64(len(content))
		if total+size > maxBytes {
			logAttachmentDropped(ctx, logger, taskID, c.Filename, "size_cap_exceeded", size)
			dropAny = true
			continue
		}
		total += size

		contentType := c.ContentType
		if contentType == "" {
			contentType = mime
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		atts = append(atts, ReplyAttachment{
			Filename:    c.Filename,
			ContentType: contentType,
			Bytes:       content,
		})
	}
	return atts, dropAny
}

func filterUnsupportedAttachments(ctx context.Context, logger *slog.Logger, taskID string, supportsAttachments bool, atts []ReplyAttachment) (kept []ReplyAttachment, droppedAny bool) {
	if supportsAttachments || len(atts) == 0 {
		return atts, false
	}
	for _, att := range atts {
		logAttachmentDropped(ctx, logger, taskID, att.Filename, "transport_unsupported", int64(len(att.Bytes)))
	}
	return nil, true
}

func logAttachmentDropped(ctx context.Context, logger *slog.Logger, taskID, name, reason string, sizeBytes int64) {
	if logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("eventType", "email.reply_attachment_dropped"),
		slog.String("taskID", taskID),
		slog.String("name", name),
		slog.String("reason", reason),
	}
	if sizeBytes >= 0 {
		attrs = append(attrs, slog.Int64("size_bytes", sizeBytes))
	}
	logger.LogAttrs(ctx, slog.LevelWarn, "reply attachment dropped", attrs...)
}

func buildArtifactURI(loc artifact.Locator, version int) string {
	return fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		url.PathEscape(loc.AppName),
		url.PathEscape(loc.UserID),
		url.PathEscape(loc.SessionID),
		url.PathEscape(loc.Filename),
		version,
	)
}

func parseArtifactURI(uri string) (artifact.Locator, int, error) {
	if !strings.HasPrefix(uri, "artifact://") {
		return artifact.Locator{}, 0, &uriParseError{msg: "missing artifact:// scheme", uri: uri}
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return artifact.Locator{}, 0, &uriParseError{msg: "url parse failed: " + err.Error(), uri: uri}
	}
	appName, hostErr := url.QueryUnescape(parsed.Host)
	if hostErr != nil {
		appName = parsed.Host
	}
	parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 3)
	if len(parts) != 3 {
		return artifact.Locator{}, 0, &uriParseError{msg: "path must have user_id/session_id/filename", uri: uri}
	}
	loc := artifact.Locator{
		AppName:   appName,
		UserID:    decodeURISegment(parts[0]),
		SessionID: decodeURISegment(parts[1]),
		Filename:  decodeURISegment(parts[2]),
	}
	version := 0
	if v := parsed.Query().Get("version"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			version = n
		}
	}
	return loc, version, nil
}

func decodeURISegment(s string) string {
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

type uriParseError struct {
	msg string
	uri string
}

func (e *uriParseError) Error() string { return e.msg + ": " + e.uri }
