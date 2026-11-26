// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yosida95/uritemplate/v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const artifactDescPrefix = "Artifact: "

type artifactClass string

const (
	classImage        artifactClass = "image"
	classAudio        artifactClass = "audio"
	classTextEmbedded artifactClass = "text_embedded"
	classBlobEmbedded artifactClass = "blob_embedded"
	classResourceLink artifactClass = "resource_link"
)

func classifyArtifact(mimeType string, size int, cfg *Config) artifactClass {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if mt == "" {
		mt = "application/octet-stream"
	}
	switch {
	case strings.HasPrefix(mt, "image/"):
		if size <= cfg.InlineImageMaxBytes {
			return classImage
		}
		return classResourceLink
	case strings.HasPrefix(mt, "audio/"):
		if size <= cfg.InlineAudioMaxBytes {
			return classAudio
		}
		return classResourceLink
	case embed.IsTextBasedMIME(mt):
		if size <= cfg.InlineTextMaxBytes {
			return classTextEmbedded
		}
		return classResourceLink
	default:
		if size <= cfg.InlineBinaryMaxBytes {
			return classBlobEmbedded
		}
		return classResourceLink
	}
}

func (a *Adapter) resolveFinalText(ctx context.Context, taskID, text string, ts *taskState) string {
	if text == "" || !strings.Contains(text, "«") || a.core == nil {
		return text
	}
	maxArtifact := embed.DefaultMaxArtifactSizeBytes
	if cfg := a.core.Config(); cfg != nil && cfg.GatewayArtifactContentLimitBytes > 0 {
		maxArtifact = cfg.GatewayArtifactContentLimitBytes
	}
	rctx := &embed.ResolveContext{
		Artifacts: a.core.Services().Artifacts(),
		AgentName: ts.agentName,
		UserID:    ts.userID,
		SessionID: ts.sessionID,
		Config: embed.ResolveConfig{
			MaxDepth:             12,
			MaxArtifactSizeBytes: maxArtifact,
			MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
		},
		Logger: a.logger,
	}
	resolved, signals, err := embed.ResolveEmbedsInString(ctx, text, embed.ModeA2AMessageToUser, rctx)
	if err != nil {
		a.logger.Warn("mcp: failed to resolve embeds in final text; returning unresolved",
			"taskID", taskID, "error", err)
		return text
	}
	a.appendFinalTextSignals(ctx, taskID, ts, signals)
	return resolved
}

func (a *Adapter) appendFinalTextSignals(ctx context.Context, taskID string, ts *taskState, signals []embed.Signal) {
	for _, sig := range signals {
		switch data := sig.Data.(type) {
		case embed.InlineBinaryData:
			a.appendInlineBinary(ctx, ts, data)
		case embed.ArtifactReturnData:
			if data.Filename != "" {
				_, version := directive.ParseArtifactSpec(data.Version)
				a.convertArtifactFromStore(ctx, taskID, ts, artifactRef{filename: data.Filename, version: version}, slog.LevelWarn)
			}
		}
	}
}

func (a *Adapter) appendInlineBinary(ctx context.Context, ts *taskState, data embed.InlineBinaryData) {
	if len(data.Bytes) == 0 {
		return
	}
	name := cmp.Or(data.Name, "file")
	if !markProcessed(ts, name) {
		return
	}
	ref := artifactRef{sessionID: ts.sessionID, filename: name, mimeType: data.MIMEType}
	a.appendArtifactContent(ctx, ts, ref, data.Bytes, false)
}

func buildToolResult(text string, contents []mcp.Content) *mcp.CallToolResult {
	if len(contents) == 0 {
		return mcp.NewToolResultText(text)
	}
	out := make([]mcp.Content, 0, 1+len(contents))
	if text != "" {
		out = append(out, mcp.NewTextContent(text))
	}
	out = append(out, contents...)
	return &mcp.CallToolResult{Content: out}
}

type artifactRef struct {
	agentName   string
	userID      string
	sessionID   string
	filename    string
	displayName string
	mimeType    string
	version     *int
}

func (r artifactRef) label() string {
	if r.displayName != "" {
		return r.displayName
	}
	return r.filename
}

func markProcessed(ts *taskState, filename string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.processedFiles == nil {
		ts.processedFiles = make(map[string]struct{})
	}
	if _, seen := ts.processedFiles[filename]; seen {
		return false
	}
	ts.processedFiles[filename] = struct{}{}
	return true
}

func (a *Adapter) handleArtifactSignal(ctx context.Context, taskID string, ts *taskState, part a2a.Part) {
	dataMap, ok := part.Data.(map[string]any)
	if !ok {
		return
	}
	if dataMap["type"] != "artifact_creation_progress" {
		return
	}
	if status, _ := dataMap["status"].(string); status != "completed" {
		return
	}
	if entrypoint.DataTagsContainWorking(dataMap) {
		return
	}
	filename, _ := dataMap["filename"].(string)
	if filename == "" {
		return
	}
	mimeHint, _ := dataMap["mime_type"].(string)
	a.convertArtifactFromStore(ctx, taskID, ts,
		artifactRef{filename: filename, mimeType: mimeHint, version: entrypoint.DataIntPtr(dataMap["version"])}, slog.LevelError)
}

func (a *Adapter) handleFilePart(ctx context.Context, taskID string, ts *taskState, part a2a.Part) {
	fc := part.File
	if fc == nil {
		return
	}
	if fc.URI != "" {
		filename, version, ok := entrypoint.ParseArtifactURI(fc.URI)
		if ok && filename != "" {
			a.convertArtifactFromStore(ctx, taskID, ts,
				artifactRef{filename: filename, displayName: fc.Name, mimeType: fc.MIMEType, version: version}, slog.LevelError)
			return
		}
		name := fc.Name
		if name == "" {
			name = fc.URI
		}
		if !markProcessed(ts, name) {
			return
		}
		ts.mu.Lock()
		ts.contents = append(ts.contents, mcp.ResourceLink{
			Type: "resource_link", URI: fc.URI, Name: name,
			Description: artifactDescPrefix + name, MIMEType: fc.MIMEType,
		})
		ts.mu.Unlock()
		return
	}
	if fc.Bytes == "" {
		return
	}
	filename := fc.Name
	if filename == "" {
		filename = "file"
	}
	content, err := base64.StdEncoding.DecodeString(fc.Bytes)
	if err != nil {
		a.logger.Error("mcp artifact: failed to decode inline file bytes",
			"taskID", taskID, "filename", filename, "error", err)
		return
	}
	if len(content) == 0 {
		return
	}
	if !markProcessed(ts, filename) {
		return
	}
	ref := artifactRef{sessionID: ts.sessionID, filename: filename, mimeType: fc.MIMEType}
	a.appendArtifactContent(ctx, ts, ref, content, false)
}

func (a *Adapter) convertArtifactFromStore(ctx context.Context, taskID string, ts *taskState, want artifactRef, loadFailureLevel slog.Level) {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Warn("mcp artifact: no artifact service configured; artifact dropped",
			"taskID", taskID, "filename", want.filename)
		return
	}
	loc := artSvc.Resolve(ts.agentName, ts.userID, ts.sessionID, want.filename)
	content, mime, err := artSvc.Inner.Load(ctx, loc, want.version)
	if err != nil {
		a.logger.Log(ctx, loadFailureLevel, "mcp artifact: failed to load artifact",
			"taskID", taskID, "agentName", ts.agentName, "filename", want.filename, "version", versionLabel(want.version), "error", err)
		return
	}
	if len(content) == 0 {
		a.logger.Warn("mcp artifact: produced artifact loaded empty; skipping",
			"taskID", taskID, "agentName", ts.agentName, "filename", want.filename)
		return
	}
	if !markProcessed(ts, want.filename) {
		return
	}
	ref := want
	ref.agentName, ref.userID, ref.sessionID = ts.agentName, ts.userID, ts.sessionID
	ref.mimeType = cmp.Or(want.mimeType, mime)
	a.appendArtifactContent(ctx, ts, ref, content, true)
}

func versionLabel(version *int) string {
	if version == nil {
		return "latest"
	}
	return strconv.Itoa(*version)
}

func (a *Adapter) appendArtifactContent(ctx context.Context, ts *taskState, ref artifactRef, content []byte, storeBacked bool) {
	mime := ref.mimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	ref.mimeType = mime
	uri := a.contentURI(ts, ref)
	class := classifyArtifact(mime, len(content), a.cfg)
	if class == classResourceLink && (!storeBacked || !a.cfg.EnableArtifactResources || !a.modernLinkReadable(ts) || !a.recordSessionLink(ctx, ts, ref, uri)) {
		class = classBlobEmbedded
	}

	var c mcp.Content
	b64 := base64.StdEncoding.EncodeToString(content)
	switch class {
	case classImage:
		c = mcp.ImageContent{Type: "image", Data: b64, MIMEType: mime}
	case classAudio:
		c = mcp.AudioContent{Type: "audio", Data: b64, MIMEType: mime}
	case classTextEmbedded:
		c = mcp.EmbeddedResource{Type: "resource", Resource: mcp.TextResourceContents{
			URI: uri, MIMEType: mime, Text: string(content),
		}}
	case classBlobEmbedded:
		c = mcp.EmbeddedResource{Type: "resource", Resource: mcp.BlobResourceContents{
			URI: uri, MIMEType: mime, Blob: b64,
		}}
	case classResourceLink:
		c = mcp.ResourceLink{
			Type: "resource_link", URI: uri, Name: ref.label(),
			Description: artifactDescPrefix + ref.label(), MIMEType: mime,
		}
	}
	if c == nil {
		return
	}
	ts.mu.Lock()
	ts.contents = append(ts.contents, c)
	ts.mu.Unlock()
}

func (a *Adapter) artifactURI(sessionID, filename string) string {
	return fmt.Sprintf("%s://%s/%s", a.artifactURIPrefix(), sessionID, filename)
}

func (a *Adapter) artifactURIPrefix() string {
	return cmp.Or(a.cfg.ResourceURIPrefix, "artifact")
}

func (a *Adapter) contentURI(ts *taskState, ref artifactRef) string {
	if !ts.modern || ts.handle == "" || a.artifactTemplate == nil {
		return a.artifactURI(ref.sessionID, ref.filename)
	}
	values := uritemplate.Values{
		artifactArgSession:  uritemplate.String(ts.handle),
		artifactArgFilename: uritemplate.String(templateEscape(ref.filename)),
	}
	if ref.agentName != "" {
		values[artifactArgAgent] = uritemplate.String(templateEscape(ref.agentName))
	}
	if ref.version != nil {
		values[artifactArgVersion] = uritemplate.String(strconv.Itoa(*ref.version))
	}
	uri, err := a.artifactTemplate.Expand(values)
	if err != nil {
		a.logger.Debug("mcp artifact: template link did not expand; linking the per-session URI instead",
			"sessionID", ref.sessionID)
		return a.artifactURI(ref.sessionID, ref.filename)
	}
	return uri
}

func (a *Adapter) modernLinkReadable(ts *taskState) bool {
	return !ts.modern || (ts.handle != "" && a.artifactTemplate != nil)
}

func templateEscape(value string) string { return url.PathEscape(value) }

func templateUnescape(value string) (string, bool) {
	unescaped, err := url.PathUnescape(value)
	return unescaped, err == nil
}

const (
	artifactArgSession  = "sessionId"
	artifactArgFilename = "filename"
	artifactArgAgent    = "agent"
	artifactArgVersion  = "version"
)

func (a *Adapter) registerArtifactTemplate() {
	tpl, err := uritemplate.New(a.artifactURIPrefix() + "://{" + artifactArgSession + "}/{" + artifactArgFilename + "}{?" + artifactArgAgent + "," + artifactArgVersion + "}")
	if err != nil {
		a.logger.Warn("mcp artifact: resource_uri_prefix does not form a URI template; protocol 2026-07-28 clients cannot read artifact links",
			"error", err)
		return
	}
	a.artifactTemplate = tpl
	a.mcpServer.AddResourceTemplate(mcp.ResourceTemplate{
		URITemplate: &mcp.URITemplate{Template: tpl},
		Name:        "artifact",
		Description: "An artifact an agent produced in one of your conversations",
	}, a.readArtifactTemplate)
}

func (a *Adapter) readArtifactTemplate(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	if !server.IsModernRequest(ctx) {
		return nil, artifactNotFound(ctx)
	}
	userID := a.readerUserID(ctx)
	sessionID, err := a.ownedConversation(ctx, templateArg(req, artifactArgSession), userID)
	if errors.Is(err, errUnknownSessionID) {
		return nil, artifactNotFound(ctx)
	}
	if err != nil {
		return nil, err
	}
	filename, ok := templateUnescape(templateArg(req, artifactArgFilename))
	if _, userScoped := artifact.ResolveFilename(filename); !ok || filename == "" || userScoped {
		a.logger.Debug(logArtifactReadRefused, "userID", userID, "sessionID", sessionID, "reason", "filename_outside_conversation")
		return nil, artifactNotFound(ctx)
	}
	agentName, ok := templateUnescape(templateArg(req, artifactArgAgent))
	if !ok || agentName == "" {
		a.logger.Debug(logArtifactReadRefused, "userID", userID, "sessionID", sessionID, "reason", "no_agent")
		return nil, artifactNotFound(ctx)
	}
	ref := artifactRef{agentName: agentName, userID: userID, sessionID: sessionID, filename: filename}
	if v := templateArg(req, artifactArgVersion); v != "" {
		version, convErr := strconv.Atoi(v)
		if convErr != nil {
			return nil, artifactNotFound(ctx)
		}
		ref.version = &version
	}
	return a.readArtifactResource(ctx, ref, req.Params.URI)
}

const logArtifactReadRefused = "mcp artifact: read refused"

func templateArg(req mcp.ReadResourceRequest, name string) string {
	switch v := req.Params.Arguments[name].(type) {
	case []string:
		if len(v) == 1 {
			return v[0]
		}
	case string:
		return v
	}
	return ""
}

type artifactNotFoundError struct{ code int }

func (e artifactNotFoundError) Error() string { return server.ErrResourceNotFound.Error() }

func (e artifactNotFoundError) JSONRPCError() mcp.JSONRPCError {
	return mcp.JSONRPCError{
		JSONRPC: mcp.JSONRPC_VERSION,
		Error:   mcp.JSONRPCErrorDetails{Code: e.code, Message: e.Error()},
	}
}

func artifactNotFound(ctx context.Context) error {
	code := mcp.RESOURCE_NOT_FOUND
	if server.IsModernRequest(ctx) {
		code = mcp.INVALID_PARAMS
	}
	return server.NewHandlerError(artifactNotFoundError{code: code})
}

func (a *Adapter) recordSessionLink(ctx context.Context, ts *taskState, ref artifactRef, uri string) bool {
	if ts.modern || ts.mcpSessionID == "" {
		return true
	}
	if a.links == nil || ref.agentName == "" {
		return false
	}
	if _, err := url.ParseRequestURI(uri); err != nil {
		a.logger.Warn("mcp artifact: link is not a valid URI; embedding the artifact", "sessionID", ref.sessionID, "error", err)
		return false
	}
	err := a.links.Put(ctx, store.MCPArtifactLink{
		MCPSessionID: ts.mcpSessionID, URI: uri, AgentName: ref.agentName, UserID: ref.userID,
		SessionID: ref.sessionID, Filename: ref.filename, DisplayName: ref.displayName,
		MIMEType: ref.mimeType, Version: ref.version,
	})
	if err != nil {
		a.logger.Warn("mcp artifact: link not recorded; embedding the artifact", "sessionID", ref.sessionID, "error", err)
		return false
	}
	if a.mcpServer != nil {
		_ = a.mcpServer.SendNotificationToSpecificClient(ts.mcpSessionID, mcp.MethodNotificationResourcesListChanged, nil)
	}
	return true
}

func (a *Adapter) loadSessionLinks(ctx context.Context) {
	if a.links == nil || server.IsModernRequest(ctx) {
		return
	}
	sess, ok := server.ClientSessionFromContext(ctx).(server.SessionWithResources)
	if !ok || sess.SessionID() == "" {
		return
	}
	links, err := a.links.ListBySession(ctx, sess.SessionID())
	if err != nil {
		a.logger.Warn("mcp artifact: session links not read; the session lists and reads none", "error", err)
		return
	}
	resources := make(map[string]server.ServerResource, len(links))
	for _, l := range links {
		ref := artifactRef{
			agentName: l.AgentName, userID: l.UserID, sessionID: l.SessionID,
			filename: l.Filename, displayName: l.DisplayName, mimeType: l.MIMEType, version: l.Version,
		}
		uri := l.URI
		resources[uri] = server.ServerResource{
			Resource: mcp.Resource{
				URI: uri, Name: ref.sessionID + "_" + ref.filename,
				Description: artifactDescPrefix + ref.label(), MIMEType: ref.mimeType,
			},
			Handler: func(ctx context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				return a.readArtifactResource(ctx, ref, uri)
			},
		}
	}
	sess.SetSessionResources(resources)
}

func (a *Adapter) readArtifactResource(ctx context.Context, ref artifactRef, uri string) ([]mcp.ResourceContents, error) {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		return nil, fmt.Errorf("no artifact service configured")
	}
	loc := artSvc.Resolve(ref.agentName, ref.userID, ref.sessionID, ref.filename)
	content, mime, err := artSvc.Inner.Load(ctx, loc, ref.version)
	if err != nil {
		return nil, fmt.Errorf("load artifact %q: %w", ref.filename, err)
	}
	if len(content) == 0 {
		return nil, artifactNotFound(ctx)
	}
	if ref.mimeType != "" {
		mime = ref.mimeType
	}
	if embed.IsTextBasedMIME(mime) {
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI: uri, MIMEType: mime, Text: string(content),
		}}, nil
	}
	return []mcp.ResourceContents{mcp.BlobResourceContents{
		URI: uri, MIMEType: mime, Blob: base64.StdEncoding.EncodeToString(content),
	}}, nil
}
