// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/ingestlimit"
)

const maxAttachmentBytes = 25 << 20

const maxAttachmentsPerMessage = 10

var errAttachmentTooLarge = errors.New("google chat attachment exceeds the size limit")

func attachmentLabel(att chatAttachment) string {
	if att.ContentName != "" {
		return att.ContentName
	}
	return "attachment"
}

func validAttachmentResource(name string) bool {
	return name != "" && !strings.ContainsAny(name, "?#\\") && !strings.Contains(name, "..") && !strings.HasPrefix(name, "/")
}

type storeFile func(ctx context.Context, upload entrypoint.ArtifactUpload) (uri string, ok bool)

func (a *Adapter) inboundFileParts(ctx context.Context, attachments []chatAttachment, store storeFile) (parts []a2a.Part, dropped []string) {
	downloadCtx, cancel := context.WithTimeout(ctx, a.attachmentsBudget)
	defer cancel()
	unattempted := 0
	for i, att := range attachments {
		if i >= maxAttachmentsPerMessage {
			dropped = append(dropped, attachmentLabel(att))
			continue
		}
		if att.Source != attachmentUploaded || att.AttachmentDataRef == nil || !validAttachmentResource(att.AttachmentDataRef.ResourceName) {
			dropped = append(dropped, attachmentLabel(att))
			continue
		}
		if downloadCtx.Err() != nil {
			dropped = append(dropped, attachmentLabel(att))
			unattempted++
			continue
		}
		part, ok := a.keepAttachment(ctx, downloadCtx, att, store)
		if !ok {
			dropped = append(dropped, attachmentLabel(att))
			continue
		}
		parts = append(parts, part)
	}
	if unattempted > 0 {
		a.logAttachmentsDropped(ctx, "google chat attachments dropped: the message's download budget ran out before they were tried", "files", unattempted)
	}
	return parts, dropped
}

func (a *Adapter) keepAttachment(ctx, downloadCtx context.Context, att chatAttachment, store storeFile) (a2a.Part, bool) {
	hold, err := a.attachmentBytes.Acquire(downloadCtx, maxAttachmentBytes)
	if err != nil {
		reason := "budget_busy"
		switch {
		case ctx.Err() != nil:
			reason = "stopping"
		case errors.Is(err, ingestlimit.ErrOverCapacity):
			reason = "over_capacity"
		}
		a.logAttachmentsDropped(ctx, "google chat attachment dropped: no room to hold it within the message's download budget", "contentType", att.ContentType, "reason", reason)
		return a2a.Part{}, false
	}
	defer hold.Release()
	data, err := a.downloadAttachment(downloadCtx, att.AttachmentDataRef.ResourceName)
	if err != nil {
		a.logAttachmentsDropped(ctx, "google chat attachment could not be downloaded", "contentType", att.ContentType, "error", err)
		return a2a.Part{}, false
	}
	hold.Shrink(int64(len(data)))
	storeCtx, cancel := context.WithTimeout(ctx, a.attachmentStoreTimeout)
	defer cancel()
	name := attachmentLabel(att)
	uri, ok := store(storeCtx, entrypoint.ArtifactUpload{Filename: name, MIMEType: att.ContentType, Content: data, DeriveMIME: true})
	if !ok {
		return a2a.Part{}, false
	}
	return a2a.Part{Kind: "file", File: &a2a.FileContent{Name: name, MIMEType: att.ContentType, URI: uri}}, true
}

func (a *Adapter) logAttachmentsDropped(ctx context.Context, msg string, fields ...any) {
	level := slog.LevelWarn
	if ctx.Err() != nil {
		level = slog.LevelDebug
	}
	a.logger.Log(ctx, level, msg, fields...)
}

func (a *Adapter) downloadAttachment(ctx context.Context, resourceName string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, a.attachmentTimeout)
	defer cancel()
	return a.downloader.DownloadAttachment(ctx, resourceName, maxAttachmentBytes)
}

func artifactNames(text string, task *a2a.Task, saved []entrypoint.SavedArtifact) []string {
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, match := range artifactReturnPattern.FindAllStringSubmatch(text, -1) {
		name, _ := agent.ParseArtifactSpec(match[1])
		add(name)
	}
	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if part.Kind == "file" && part.File != nil {
				add(part.File.Name)
			}
		}
	}
	for _, s := range saved {
		if !slices.Contains(s.Tags, a2a.ArtifactTagWorking) {
			add(s.Filename)
		}
	}
	return names
}

func artifactNotice(names []string, webUI string) string {
	list := strings.Join(names, ", ")
	open := "Open them in Solace Agent Mesh"
	if len(names) == 1 {
		open = "Open it in Solace Agent Mesh"
	}
	if webUI == "" {
		return fmt.Sprintf("📎 The agent created %s. %s.", list, open)
	}
	return fmt.Sprintf("📎 The agent created %s. <%s|%s>.", list, webUI, open)
}

const inlineArtifactMaxBytes = 24 << 10

const artifactLoadTimeout = 5 * time.Second

type proseKind int

const (
	notProse proseKind = iota
	plainProse
	markdownProse
)

func proseKindOf(name, mimeType string) proseKind {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0])) {
	case "text/markdown", "text/x-markdown":
		return markdownProse
	case "text/plain":
		return plainProse
	case "", "application/octet-stream":
		switch strings.ToLower(path.Ext(name)) {
		case ".md", ".markdown":
			return markdownProse
		case ".txt":
			return plainProse
		}
	}
	return notProse
}

func inlineableArtifact(name, mimeType string) bool {
	return proseKindOf(name, mimeType) != notProse
}

func (a *Adapter) inlineArtifacts(ctx context.Context, taskID string, st *taskState, q *replyQueue, saved []entrypoint.SavedArtifact) []string {
	if a.core == nil || len(saved) == 0 {
		return nil
	}
	store := a.core.Services().Artifacts()
	info := a.taskRegistry.Get(taskID)
	if store == nil || info == nil {
		return nil
	}
	sessionID := st.snapshot().SessionID
	var shown []string
	for _, s := range saved {
		if slices.Contains(s.Tags, a2a.ArtifactTagWorking) || !inlineableArtifact(s.Filename, s.MIMEType) {
			continue
		}
		text, ok := a.loadInlineText(ctx, store, store.Resolve(info.AgentName, info.UserID, sessionID, s.Filename), s.Version)
		if !ok {
			continue
		}
		for _, chunk := range splitForChat(st.citations.renderAnswer("📄 *"+s.Filename+"*\n\n"+text), maxMessageRunes) {
			q.Notice(chunk)
		}
		shown = append(shown, s.Filename)
	}
	return shown
}

func (a *Adapter) loadInlineText(ctx context.Context, store *artifact.ScopedService, loc artifact.Locator, version *int) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, artifactLoadTimeout)
	defer cancel()
	meta, err := store.Inner.GetVersionMetadata(ctx, loc, version)
	if err != nil || meta == nil || meta.SizeBytes > inlineArtifactMaxBytes {
		return "", false
	}
	content, _, err := store.Inner.Load(ctx, loc, version)
	if err != nil || len(content) > inlineArtifactMaxBytes || !utf8.Valid(content) {
		return "", false
	}
	text := strings.TrimSpace(string(content))
	return text, text != ""
}
