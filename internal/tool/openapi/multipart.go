// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

const artifactURIPrefix = "artifact://"

type artifactResolver func(ctx context.Context, uri string) (content []byte, filename string, mimeType string, err error)

func buildMultipartBody(
	ctx context.Context,
	tmpl operationTemplate,
	args map[string]any,
	resolve artifactResolver,
) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for _, p := range tmpl.Params {
		if p.In != locBody {
			continue
		}
		raw, ok := args[p.PyName]
		if !ok {
			continue
		}

		if isBinaryProperty(p.Schema) {
			if err := writeFilePart(ctx, w, p.OriginalName, raw, resolve); err != nil {
				return nil, "", fmt.Errorf("multipart field %q: %w", p.OriginalName, err)
			}
			continue
		}

		if err := writeFormField(w, p.OriginalName, raw); err != nil {
			return nil, "", fmt.Errorf("multipart field %q: %w", p.OriginalName, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func writeFormField(w *multipart.Writer, name string, value any) error {
	var strVal string
	switch v := value.(type) {
	case string:
		strVal = v
	case []byte:
		strVal = string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		strVal = string(b)
	}
	return w.WriteField(name, strVal)
}

func writeFilePart(
	ctx context.Context,
	w *multipart.Writer,
	fieldName string,
	value any,
	resolve artifactResolver,
) error {
	content, filename, mimeType, err := resolveBinaryValue(ctx, value, resolve)
	if err != nil {
		return err
	}
	if filename == "" {
		filename = fieldName
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	part, err := createFilePart(w, fieldName, filename, mimeType)
	if err != nil {
		return fmt.Errorf("create file part: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		return fmt.Errorf("write file part: %w", err)
	}
	return nil
}

func createFilePart(w *multipart.Writer, fieldName, filename, mimeType string) (io.Writer, error) {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, escapeQuoted(fieldName), escapeQuoted(filename)))
	h.Set("Content-Type", mimeType)
	return w.CreatePart(h)
}

func escapeQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func resolveBinaryValue(
	ctx context.Context,
	value any,
	resolve artifactResolver,
) (content []byte, filename, mimeType string, err error) {
	switch v := value.(type) {
	case []byte:
		return v, "", "", nil
	case string:
		if strings.HasPrefix(v, artifactURIPrefix) {
			if resolve == nil {
				return nil, "", "", fmt.Errorf("artifact URI %q provided but no artifact service available", v)
			}
			return resolve(ctx, v)
		}
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			return decoded, "", "", nil
		}
		return []byte(v), "", "", nil
	case nil:
		return nil, "", "", fmt.Errorf("nil binary value")
	default:
		return nil, "", "", fmt.Errorf("unsupported binary value type %T", value)
	}
}

func NewArtifactResolver(svc *artifact.ScopedService, agentName, userID, sessionID string) func(ctx context.Context, uri string) ([]byte, string, string, error) {
	return defaultArtifactResolver(svc, agentName, userID, sessionID)
}

func defaultArtifactResolver(svc *artifact.ScopedService, agentName, userID, sessionID string) artifactResolver {
	return func(ctx context.Context, uri string) ([]byte, string, string, error) {
		if svc == nil {
			return nil, "", "", fmt.Errorf("no artifact service configured")
		}
		rest := strings.TrimPrefix(uri, artifactURIPrefix)
		filename := rest
		versionPtr := (*int)(nil)
		if idx := strings.Index(rest, "?"); idx >= 0 {
			filename = rest[:idx]
			query := rest[idx+1:]
			for _, kv := range strings.Split(query, "&") {
				if strings.HasPrefix(kv, "version=") {
					var v int
					if _, err := fmt.Sscanf(kv[len("version="):], "%d", &v); err == nil {
						versionPtr = &v
					}
				}
			}
		}
		loc := svc.Resolve(agentName, userID, sessionID, filename)
		content, mimeType, err := svc.Inner.Load(ctx, loc, versionPtr)
		if err != nil {
			return nil, "", "", fmt.Errorf("load artifact %q: %w", filename, err)
		}
		if content == nil {
			return nil, "", "", fmt.Errorf("artifact %q not found", filename)
		}
		return content, filename, mimeType, nil
	}
}
