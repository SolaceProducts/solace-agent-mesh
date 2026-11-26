// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	oapiruntime "github.com/oapi-codegen/runtime"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type Manager struct {
	httpClient *http.Client
	logger     *slog.Logger
	specStore  SpecStore
}

func NewManager(logger *slog.Logger, specStore SpecStore) *Manager {
	return &Manager{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
		specStore:  specStore,
	}
}

func (m *Manager) Start(_ context.Context) error { return nil }

func (m *Manager) Stop() {}

func (m *Manager) LoadAndDiscoverTools(cfg *ToolsetConfig) ([]*OpenAPITool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rawSpec, err := m.readSpecBytes(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if vErr := runConfigValidators(cfg, rawSpec); vErr != nil {
		return nil, vErr
	}

	doc, err := m.loadSpec(ctx, cfg)
	if err != nil {
		return nil, err
	}

	baseURL := getBaseURL(doc)
	if baseURL == "" {
		return nil, fmt.Errorf("openapi: no base URL found in spec or config")
	}

	if cfg.Auth == nil || cfg.Auth.Type == AuthTypeUnset {
		if derived, derr := DetectAuthFromSpec(rawSpec); derr == nil && derived != nil {
			cfg.Auth = derived
			m.logger.Info("auto-wired auth from OpenAPI spec", "type", derived.Type)
			if derived.Type.IsOAuth2() && derived.ClientID == "" {
				m.logger.Warn(
					"auto-wired OAuth2 auth requires client_id/client_secret — add an authentication block with credential material",
					"type", derived.Type,
					"specURL", cmp.Or(cfg.SpecURL, cfg.SpecFile),
					"authorizationURL", derived.AuthorizationURL,
					"tokenURL", derived.TokenURL,
				)
			}
		}
	}

	ops := extractOperations(doc)
	m.logger.Info("parsed OpenAPI spec", "operations", len(ops), "baseURL", baseURL)

	authHandler, err := NewAuthHandlerForOpenAPI(cfg.Auth, m.logger)
	if err != nil {
		return nil, fmt.Errorf("openapi: build auth handler: %w", err)
	}

	var tools []*OpenAPITool
	for _, op := range ops {
		if !shouldInclude(op.OperationID, cfg.AllowList, cfg.DenyList) {
			continue
		}

		desc := operationDescriptionText(op)

		toolName := OperationToolName(op.OperationID)

		tmpl := buildOperationTemplate(op)
		t := &OpenAPITool{
			name:         toolName,
			description:  desc,
			parameters:   buildParameters(tmpl),
			method:       op.Method,
			pathTemplate: op.Path,
			baseURL:      baseURL,
			auth:         cfg.Auth,
			headers:      cfg.Headers,
			manager:      m,
			scopes:       cfg.RequiredScopes,
			logger:       m.logger.With("tool", toolName),
			tmpl:         tmpl,
			authHandler:  authHandler,

			maxResponseSize: cfg.MaxResponseSize,
		}

		tools = append(tools, t)
		m.logger.Info("discovered OpenAPI tool", "tool", toolName, "operationID", op.OperationID, "method", op.Method, "path", op.Path)
	}

	return tools, nil
}

func (m *Manager) applyRuntimeAuth(
	ctx context.Context,
	t *OpenAPITool,
	req *http.Request,
	toolCtx *tool.Context,
	args map[string]any,
) (*tool.Result, error) {
	if t.authHandler == nil {
		return nil, nil
	}
	if toolCtx == nil {
		return tool.Error(fmt.Sprintf(
			"OpenAPI tool %q: OAuth2 authentication required but no tool context available",
			t.name,
		)), nil
	}
	headers, err := t.authHandler.GetAuthHeaders(ctx, toolCtx)
	if err != nil {
		if wrapsErrAuthRequired(err) {
			return m.triggerOpenAPIAuthFlow(ctx, t, toolCtx, args)
		}
		if errors.Is(err, toolauth.ErrTransientRefresh) {
			return toolauth.NewSignInProviderUnavailableResult(t.name, err), nil
		}
		return tool.Error(fmt.Sprintf("OpenAPI tool %q auth error: %v", t.name, err)), nil
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	return nil, nil
}

func (m *Manager) triggerOpenAPIAuthFlow(
	ctx context.Context,
	t *OpenAPITool,
	toolCtx *tool.Context,
	args map[string]any,
) (*tool.Result, error) {
	if t.authHandler.authType.RequiresToolsetExchange() {
		return tool.Error(fmt.Sprintf(
			"OpenAPI tool %q: %s exchange did not yield a usable token",
			t.name, t.authHandler.authType,
		)), nil
	}

	if r := toolauth.PreAuthShortCircuit(toolCtx, t.name, m.logger); r != nil {
		return r, nil
	}

	credKey := t.authHandler.CredentialKey()

	if toolCtx.AuthGaveUp != nil && toolCtx.AuthGaveUp(toolCtx.UserID, toolCtx.TraceID, credKey) {
		m.logger.Warn("auth flow given up — failing tool without re-prompting",
			"tool", t.name,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
			"traceID", toolCtx.TraceID,
		)
		return toolauth.NewAuthGaveUpResult(t.name, ""), nil
	}

	if toolCtx.BeginAuthFlow != nil && !toolCtx.BeginAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey) {
		m.logger.Info("auth flow coalesced (follower)",
			"tool", t.name,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
		)
		return toolauth.NewPendingAuthFollowerResult(toolCtx.ToolCallID, args, credKey), nil
	}

	leaderActive := true
	defer func() {
		if leaderActive && toolCtx.EndAuthFlow != nil {
			toolCtx.EndAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey)
		}
	}()

	if clearErr := t.authHandler.ClearCredentialForReauth(ctx, toolCtx, ""); clearErr != nil {
		m.logger.Debug("failed to clear credential before auth flow", "tool", t.name, "error", clearErr)
	}

	signal, err := t.authHandler.BuildAuthSignal(ctx, toolCtx)
	if err != nil {
		return tool.Error(fmt.Sprintf("build auth signal: %v", err)), nil
	}
	if toolCtx.SendSignal == nil {
		m.logger.Warn("no SendSignal available; cannot publish authentication_required",
			"tool", t.name,
		)
		return tool.AuthPromptChannelMissing(), nil
	}
	if sigErr := toolCtx.SendSignal(signal); sigErr != nil {
		m.logger.Warn("failed to send authentication_required signal",
			"tool", t.name, "error", sigErr,
		)
		return tool.AuthPromptUndelivered(sigErr), nil
	}
	leaderActive = false
	return toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), nil
}

func (m *Manager) maxResponseSize(t *OpenAPITool) int64 {
	var configured int64
	if t != nil {
		configured = t.maxResponseSize
	}
	return clampResponseSize(configured)
}

func (m *Manager) ExecuteTool(ctx context.Context, t *OpenAPITool, args map[string]any) (*tool.Result, error) {
	return m.executeTool(ctx, t, args, nil, nil)
}

func (m *Manager) ExecuteToolWithArtifacts(
	ctx context.Context,
	t *OpenAPITool,
	args map[string]any,
	resolve artifactResolver,
) (*tool.Result, error) {
	return m.executeTool(ctx, t, args, resolve, nil)
}

func (m *Manager) executeTool(
	ctx context.Context,
	t *OpenAPITool,
	args map[string]any,
	resolve artifactResolver,
	toolCtx *tool.Context,
) (*tool.Result, error) {
	bucketed, err := bucketArgs(t.tmpl, args)
	if err != nil {
		return nil, err
	}

	path, err := renderPath(t.pathTemplate, t.tmpl, bucketed.path)
	if err != nil {
		return nil, err
	}

	queryStr, err := renderQuery(t.tmpl, bucketed.query)
	if err != nil {
		return nil, fmt.Errorf("render query: %w", err)
	}

	fullURL := t.baseURL + path
	if queryStr != "" {
		if strings.Contains(fullURL, "?") {
			fullURL += "&" + queryStr
		} else {
			fullURL += "?" + queryStr
		}
	}

	bodyBytes, bodyContentType, err := buildRequestBody(ctx, t.tmpl, bucketed.body, resolve)
	if err != nil {
		return nil, fmt.Errorf("build request body: %w", err)
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, t.method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if accept := t.tmpl.AcceptHeader; accept != "" {
		req.Header.Set("Accept", accept)
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if bodyContentType != "" {
		req.Header.Set("Content-Type", bodyContentType)
	}
	if hErr := applyHeaderParams(req, t.tmpl, bucketed.header); hErr != nil {
		return nil, fmt.Errorf("apply headers: %w", hErr)
	}
	if cErr := applyCookieParams(req, t.tmpl, bucketed.cookie); cErr != nil {
		return nil, fmt.Errorf("apply cookies: %w", cErr)
	}

	authResult, authErr := m.applyRuntimeAuth(ctx, t, req, toolCtx, args)
	if authErr != nil {
		return nil, authErr
	}
	if authResult != nil {
		return authResult, nil
	}
	applyAuth(req, t.auth, t.headers)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limit := m.maxResponseSize(t)
	respBody, truncated, err := readBodyLimited(resp.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if truncated {
		m.logger.WarnContext(ctx, "openapi response exceeded configured cap",
			"tool", t.name,
			"method", t.method,
			"pathTemplate", t.pathTemplate,
			"limitBytes", limit,
			"httpStatus", resp.StatusCode,
		)
	}

	if resp.StatusCode >= 400 {
		msg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
		if truncated {
			msg += fmt.Sprintf(" (body truncated at %d bytes)", limit)
		}
		return tool.Error(msg), nil
	}

	if truncated {
		return tool.Error(
			fmt.Sprintf("response size exceeds limit %d bytes for %s %s", limit, t.method, t.pathTemplate),
		), nil
	}

	result := decodeResponse(respBody, resp.Header.Get("Content-Type"))
	return tool.OK(
		fmt.Sprintf("%s %s returned HTTP %d", t.method, t.pathTemplate, resp.StatusCode),
		tool.WithData(result),
	), nil
}

type argBuckets struct {
	path   map[string]any
	query  map[string]any
	header map[string]any
	cookie map[string]any
	body   map[string]any
}

func bucketArgs(tmpl operationTemplate, args map[string]any) (argBuckets, error) {
	out := argBuckets{
		path:   map[string]any{},
		query:  map[string]any{},
		header: map[string]any{},
		cookie: map[string]any{},
		body:   map[string]any{},
	}
	byName := map[string]*paramSpec{}
	for i := range tmpl.Params {
		byName[tmpl.Params[i].PyName] = &tmpl.Params[i]
	}
	for k, v := range args {
		p, ok := byName[k]
		if !ok {
			continue
		}
		v = restoreNestedKeys(v, p.Schema)
		switch p.In {
		case locPath:
			out.path[p.PyName] = v
		case locQuery:
			out.query[p.PyName] = v
		case locHeader:
			out.header[p.PyName] = v
		case locCookie:
			out.cookie[p.PyName] = v
		case locBody:
			out.body[p.PyName] = v
		}
	}
	return out, nil
}

func renderPath(tmpl string, opTmpl operationTemplate, args map[string]any) (string, error) {
	path := tmpl
	for _, p := range opTmpl.Params {
		if p.In != locPath {
			continue
		}
		v, ok := args[p.PyName]
		if !ok {
			if p.Required {
				return "", fmt.Errorf("missing required path parameter %q", p.OriginalName)
			}
			continue
		}
		rendered, err := oapiruntime.StyleParamWithLocation(
			p.Style, p.Explode, p.OriginalName, oapiruntime.ParamLocationPath, v,
		)
		if err != nil {
			return "", fmt.Errorf("style path param %q: %w", p.OriginalName, err)
		}
		path = strings.ReplaceAll(path, "{"+p.OriginalName+"}", rendered)
	}
	return path, nil
}

func renderQuery(opTmpl operationTemplate, args map[string]any) (string, error) {
	var parts []string
	for _, p := range opTmpl.Params {
		if p.In != locQuery {
			continue
		}
		v, ok := args[p.PyName]
		if !ok {
			continue
		}
		if hasNoQueryValue(v) {
			continue
		}
		rendered, err := oapiruntime.StyleParamWithLocation(
			p.Style, p.Explode, p.OriginalName, oapiruntime.ParamLocationQuery, v,
		)
		if err != nil {
			return "", fmt.Errorf("style query param %q: %w", p.OriginalName, err)
		}
		parts = append(parts, rendered)
	}
	return strings.Join(parts, "&"), nil
}

func applyHeaderParams(req *http.Request, opTmpl operationTemplate, args map[string]any) error {
	for _, p := range opTmpl.Params {
		if p.In != locHeader {
			continue
		}
		v, ok := args[p.PyName]
		if !ok {
			continue
		}
		rendered, err := oapiruntime.StyleParamWithLocation(
			p.Style, p.Explode, p.OriginalName, oapiruntime.ParamLocationHeader, v,
		)
		if err != nil {
			return fmt.Errorf("style header %q: %w", p.OriginalName, err)
		}
		req.Header.Set(p.OriginalName, rendered)
	}
	return nil
}

func applyCookieParams(req *http.Request, opTmpl operationTemplate, args map[string]any) error {
	var parts []string
	for _, p := range opTmpl.Params {
		if p.In != locCookie {
			continue
		}
		v, ok := args[p.PyName]
		if !ok {
			continue
		}
		rendered, err := oapiruntime.StyleParamWithLocation(
			p.Style, p.Explode, p.OriginalName, oapiruntime.ParamLocationCookie, v,
		)
		if err != nil {
			return fmt.Errorf("style cookie %q: %w", p.OriginalName, err)
		}
		parts = append(parts, rendered)
	}
	if len(parts) == 0 {
		return nil
	}
	req.Header.Set("Cookie", strings.Join(parts, "; "))
	return nil
}

func buildRequestBody(
	ctx context.Context,
	tmpl operationTemplate,
	bodyArgs map[string]any,
	resolve artifactResolver,
) ([]byte, string, error) {
	if tmpl.BodyMIME == "" {
		return nil, "", nil
	}
	if len(bodyArgs) == 0 && tmpl.BodyShape != "object" {
		return nil, "", nil
	}

	switch normalizeMIME(tmpl.BodyMIME) {
	case "application/x-www-form-urlencoded":
		values := url.Values{}
		for _, p := range tmpl.Params {
			if p.In != locBody {
				continue
			}
			v, ok := bodyArgs[p.PyName]
			if !ok {
				continue
			}
			rendered, err := oapiruntime.StyleParamWithLocation("form", true, p.OriginalName, oapiruntime.ParamLocationQuery, v)
			if err != nil {
				return nil, "", fmt.Errorf("form field %q: %w", p.OriginalName, err)
			}
			parsed, err := url.ParseQuery(rendered)
			if err != nil {
				return nil, "", fmt.Errorf("reparse form field %q: %w", p.OriginalName, err)
			}
			for k, vs := range parsed {
				for _, vv := range vs {
					values.Add(k, vv)
				}
			}
		}
		return []byte(values.Encode()), "application/x-www-form-urlencoded", nil

	case "multipart/form-data":
		bodyBytes, contentType, err := buildMultipartBody(ctx, tmpl, bodyArgs, resolve)
		if err != nil {
			return nil, "", err
		}
		return bodyBytes, contentType, nil

	case "application/octet-stream":
		raw, err := coerceOctetStream(tmpl, bodyArgs)
		if err != nil {
			return nil, "", err
		}
		return raw, "application/octet-stream", nil

	case "text/plain":
		raw, err := coerceTextBody(tmpl, bodyArgs)
		if err != nil {
			return nil, "", err
		}
		return raw, "text/plain", nil
	}

	if isJSONMIME(tmpl.BodyMIME) {
		payload, err := assembleJSONBody(tmpl, bodyArgs)
		if err != nil {
			return nil, "", err
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, "", fmt.Errorf("marshal JSON body: %w", err)
		}
		return encoded, tmpl.BodyMIME, nil
	}

	raw, err := coerceOctetStream(tmpl, bodyArgs)
	if err != nil {
		return nil, "", err
	}
	return raw, tmpl.BodyMIME, nil
}

func assembleJSONBody(tmpl operationTemplate, bodyArgs map[string]any) (any, error) {
	switch tmpl.BodyShape {
	case "array":
		for _, p := range tmpl.Params {
			if p.In == locBody {
				if v, ok := bodyArgs[p.PyName]; ok {
					return v, nil
				}
			}
		}
		return nil, nil
	case "primitive":
		for _, p := range tmpl.Params {
			if p.In == locBody {
				if v, ok := bodyArgs[p.PyName]; ok {
					return v, nil
				}
			}
		}
		return nil, nil
	default:
		out := make(map[string]any, len(bodyArgs))
		for _, p := range tmpl.Params {
			if p.In != locBody {
				continue
			}
			if v, ok := bodyArgs[p.PyName]; ok {
				out[p.OriginalName] = v
			}
		}
		return out, nil
	}
}

func coerceOctetStream(tmpl operationTemplate, bodyArgs map[string]any) ([]byte, error) {
	for _, p := range tmpl.Params {
		if p.In != locBody {
			continue
		}
		v, ok := bodyArgs[p.PyName]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case []byte:
			return val, nil
		case string:
			if decoded, err := base64.StdEncoding.DecodeString(val); err == nil {
				return decoded, nil
			}
			return []byte(val), nil
		case nil:
			return nil, nil
		default:
			b, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("marshal octet-stream body: %w", err)
			}
			return b, nil
		}
	}
	return nil, nil
}

func coerceTextBody(tmpl operationTemplate, bodyArgs map[string]any) ([]byte, error) {
	for _, p := range tmpl.Params {
		if p.In != locBody {
			continue
		}
		v, ok := bodyArgs[p.PyName]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case string:
			return []byte(val), nil
		case []byte:
			return val, nil
		case nil:
			return nil, nil
		default:
			return []byte(fmt.Sprintf("%v", val)), nil
		}
	}
	return nil, nil
}

func readBodyLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	limit = clampResponseSize(limit)
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > limit {
		return buf[:limit], true, nil
	}
	return buf, false, nil
}

func decodeResponse(body []byte, contentType string) map[string]any {
	ct := strings.ToLower(contentType)
	isJSON := strings.Contains(ct, "application/json") || strings.Contains(ct, "+json")
	if isJSON || (contentType == "" && json.Valid(body)) {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err == nil {
			return m
		}
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			return map[string]any{"response": v}
		}
	}
	return map[string]any{"response": string(body)}
}

func isJSONMIME(mime string) bool {
	lc := strings.ToLower(normalizeMIME(mime))
	return lc == "application/json" || strings.HasSuffix(lc, "+json")
}

func normalizeMIME(mime string) string {
	if idx := strings.Index(mime, ";"); idx >= 0 {
		return strings.TrimSpace(mime[:idx])
	}
	return strings.TrimSpace(mime)
}

func hasNoQueryValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case []byte:
		return len(x) == 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
