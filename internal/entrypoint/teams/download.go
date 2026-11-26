// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var mimeTypeNormalization = map[string]string{
	"csv":  "text/csv",
	"json": "application/json",
	"yaml": "text/yaml",
	"yml":  "text/yaml",
	"xml":  "text/xml",
	"txt":  "text/plain",
	"md":   "text/markdown",
	"html": "text/html",
	"pdf":  "application/pdf",
}

type DownloadedFile struct {
	Name     string
	Content  []byte
	MIMEType string
}

func redactURLError(err error) error {
	urlErr, ok := err.(*neturl.Error)
	if !ok {
		return err
	}
	redacted := "[redacted]"
	if u, parseErr := neturl.Parse(urlErr.URL); parseErr == nil {
		redacted = u.Scheme + "://" + u.Host
	}
	return &neturl.Error{Op: urlErr.Op, URL: redacted, Err: urlErr.Err}
}

func extractDownloadURL(attachment Attachment) string {
	if attachment.ContentType == "application/vnd.microsoft.teams.file.download.info" {
		if contentMap, ok := attachment.Content.(map[string]any); ok {
			if u, ok := contentMap["downloadUrl"].(string); ok && u != "" {
				return u
			}
		}
	}
	return attachment.ContentURL
}

func headTeamsAttachmentSize(ctx context.Context, attachment Attachment, logger *slog.Logger) int64 {
	downloadURL := extractDownloadURL(attachment)
	if downloadURL == "" {
		return -1
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, downloadURL, nil)
	if err != nil {
		logger.Warn("HEAD request build failed", "error", redactURLError(err))
		return -1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Warn("HEAD request failed", "error", redactURLError(err))
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return -1
	}
	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		return -1
	}
	n, err := strconv.ParseInt(cl, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func downloadAttachment(ctx context.Context, attachment Attachment, maxSizeMB int, metrics runtime.Metrics, logger *slog.Logger) (file *DownloadedFile, err error) {
	m := observability.NewGatewayOutboundMonitor(teamsGatewayName, teamsOpDownloadAttachment)
	defer m.Stop(&err)

	filename := attachment.Name
	contentType := attachment.ContentType

	var downloadURL string
	var fileType string

	if contentType == "application/vnd.microsoft.teams.file.download.info" {
		if contentMap, ok := attachment.Content.(map[string]any); ok {
			if url, ok := contentMap["downloadUrl"].(string); ok {
				downloadURL = url
			}
			if ft, ok := contentMap["fileType"].(string); ok {
				fileType = ft
			}
		}
	}

	if downloadURL == "" {
		downloadURL = attachment.ContentURL
	}

	if downloadURL == "" {
		return nil, fmt.Errorf("no download URL found in attachment")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", redactURLError(err))
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", redactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	maxBytes := int64(maxSizeMB) * 1024 * 1024
	if resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file too large: %d bytes (max %d MB)", resp.ContentLength, maxSizeMB)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file content: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("file too large: exceeds %d MB limit", maxSizeMB)
	}

	mimeType := normalizeMIMEType(fileType, resp.Header.Get("Content-Type"), filename)

	logger.Debug("downloaded Teams attachment",
		"filename", filename,
		"size", len(content),
		"mimeType", mimeType,
	)

	return &DownloadedFile{
		Name:     sanitizeFilename(filename),
		Content:  content,
		MIMEType: mimeType,
	}, nil
}

func normalizeMIMEType(fileType, responseContentType, filename string) string {
	if fileType != "" {
		if resolved, ok := mimeTypeNormalization[strings.ToLower(fileType)]; ok {
			return resolved
		}
		if strings.Contains(fileType, "/") {
			return fileType
		}
	}

	if responseContentType != "" {
		mediaType, _, _ := mime.ParseMediaType(responseContentType)
		if mediaType != "" && mediaType != "application/octet-stream" {
			return mediaType
		}
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" {
		bare := strings.TrimPrefix(ext, ".")
		if resolved, ok := mimeTypeNormalization[bare]; ok {
			return resolved
		}
		if mimeByExt := mime.TypeByExtension(ext); mimeByExt != "" {
			return mimeByExt
		}
	}

	return "application/octet-stream"
}

func sanitizeFilename(name string) string {
	unsafe := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"\x00", "_",
		"<", "_",
		">", "_",
		"\"", "_",
		"|", "_",
		"?", "_",
		"*", "_",
	)
	safe := unsafe.Replace(name)
	safe = strings.Trim(safe, ". ")

	if len(safe) > 200 {
		ext := filepath.Ext(safe)
		if ext != "" && len(ext) < 20 {
			safe = safe[:195] + ext
		} else {
			safe = safe[:200]
		}
	}

	if safe == "" {
		return "file"
	}
	return safe
}
