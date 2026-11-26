// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

type artifactStoreError struct{ cause error }

func (e *artifactStoreError) Error() string { return e.cause.Error() }
func (e *artifactStoreError) Unwrap() error { return e.cause }

type artifactClientError struct {
	Index  int
	Reason string
}

func (e *artifactClientError) Error() string {
	return fmt.Sprintf("artifact %d: %s", e.Index, e.Reason)
}

func resolveTarget(r *RouteConfig) (name, targetType string, err error) {
	switch {
	case r.TargetWorkflowName != "":
		return r.TargetWorkflowName, "workflow", nil
	case r.TargetAgentName != "":
		return r.TargetAgentName, "agent", nil
	default:
		return "", "", fmt.Errorf("no target agent or workflow configured")
	}
}

func processArtifacts(
	ctx context.Context,
	exprCtx *sacexpr.Context,
	route *RouteConfig,
	userID string,
	artSvc *artifact.ScopedService,
) ([]a2a.Part, []any, error) {
	ap := route.ArtifactProcessing
	if ap == nil || ap.ExtractExpression == "" {
		return nil, nil, nil
	}
	if artSvc == nil {
		return nil, nil, &artifactStoreError{cause: fmt.Errorf("artifact service not configured")}
	}

	extracted, err := exprCtx.GetData(ap.ExtractExpression)
	if err != nil {
		return nil, nil, fmt.Errorf("extract_artifacts_expression: %w", err)
	}
	if extracted == nil {
		return nil, nil, nil
	}

	var items []any
	switch v := extracted.(type) {
	case []any:
		items = v
	default:
		items = []any{v}
	}

	sessionID := idgen.New()
	var fileParts []a2a.Part
	var artifactURIs []any

	defer exprCtx.SetIterationItem(nil)
	for i, item := range items {
		exprCtx.SetIterationItem(item)
		def := ap.Definition
		filename, err := resolveDefinitionField(exprCtx, def.Filename, fmt.Sprintf("artifact_%d", i))
		if err != nil {
			return nil, nil, fmt.Errorf("artifact %d: filename: %w", i, err)
		}
		filename = artifact.SanitizeFilename(filename)

		contentEncoding, err := resolveDefinitionField(exprCtx, def.ContentEncoding, "utf-8")
		if err != nil {
			return nil, nil, fmt.Errorf("artifact %d: content_encoding: %w", i, err)
		}
		encodingLow := strings.ToLower(contentEncoding)
		switch encodingLow {
		case "", "utf-8", "base64", "base64url", "base64raw", "base64rawurl":
		case "binary":
			return nil, nil, fmt.Errorf("artifact %d: content_encoding %q is not supported; use %q for standard base64", i, contentEncoding, "base64")
		default:
			return nil, nil, fmt.Errorf("artifact %d: content_encoding %q is unrecognised; use utf-8, base64, base64url, base64raw, or base64rawurl", i, contentEncoding)
		}

		var content string
		switch encodingLow {
		case "base64", "base64url", "base64raw", "base64rawurl":
			content, err = resolveStringField(exprCtx, def.Content, "")
		default:
			content, err = resolveDefinitionField(exprCtx, def.Content, "")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("artifact %d: content: %w", i, err)
		}

		mimeType, err := resolveStringField(exprCtx, def.MimeType, "application/octet-stream")
		if err != nil {
			return nil, nil, fmt.Errorf("artifact %d: mime_type: %w", i, err)
		}

		var contentBytes []byte
		switch encodingLow {
		case "base64":
			decoded, decErr := base64.StdEncoding.DecodeString(content)
			if decErr != nil {
				return nil, nil, &artifactClientError{Index: i, Reason: "base64_decode"}
			}
			contentBytes = decoded
		case "base64url":
			decoded, decErr := base64.URLEncoding.DecodeString(content)
			if decErr != nil {
				return nil, nil, &artifactClientError{Index: i, Reason: "base64url_decode"}
			}
			contentBytes = decoded
		case "base64raw":
			decoded, decErr := base64.RawStdEncoding.DecodeString(content)
			if decErr != nil {
				return nil, nil, &artifactClientError{Index: i, Reason: "base64raw_decode"}
			}
			contentBytes = decoded
		case "base64rawurl":
			decoded, decErr := base64.RawURLEncoding.DecodeString(content)
			if decErr != nil {
				return nil, nil, &artifactClientError{Index: i, Reason: "base64rawurl_decode"}
			}
			contentBytes = decoded
		default:
			contentBytes = []byte(content)
		}

		if len(contentBytes) == 0 {
			return nil, nil, fmt.Errorf("artifact %d: content resolved to empty", i)
		}

		loc := artSvc.Resolve("", userID, sessionID, filename)
		if _, err := artSvc.Inner.Save(ctx, loc, contentBytes, mimeType); err != nil {
			return nil, nil, &artifactStoreError{cause: fmt.Errorf("artifact %d: save: %w", i, err)}
		}

		uri := fmt.Sprintf("artifact://%s/%s/%s/%s",
			url.PathEscape(loc.AppName), url.PathEscape(userID),
			url.PathEscape(sessionID), url.PathEscape(filename))
		artifactURIs = append(artifactURIs, uri)
		fileParts = append(fileParts, a2a.NewFilePart(uri, filename, mimeType))
	}

	return fileParts, artifactURIs, nil
}

func resolveDefinitionField(ctx *sacexpr.Context, expr, defaultVal string) (string, error) {
	if expr == "" {
		return defaultVal, nil
	}
	val, err := ctx.GetData(expr)
	if err != nil {
		return "", fmt.Errorf("evaluate %q: %w", expr, err)
	}
	if val == nil {
		return defaultVal, nil
	}
	if s, ok := val.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(val)
	if err != nil {
		return "", fmt.Errorf("evaluate %q: marshal non-string result: %w", expr, err)
	}
	return string(b), nil
}

func resolveStringField(ctx *sacexpr.Context, expr, defaultVal string) (string, error) {
	if expr == "" {
		return defaultVal, nil
	}
	val, err := ctx.GetData(expr)
	if err != nil {
		return "", fmt.Errorf("evaluate %q: %w", expr, err)
	}
	if val == nil {
		return defaultVal, nil
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("evaluate %q: expected a string value, got %T", expr, val)
	}
	return s, nil
}
