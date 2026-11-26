// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func evalArtifactSnapshotSessionID(runID, exampleID string, trialIndex int) string {
	return fmt.Sprintf("eval-%s-%s-%d", runID, exampleID, trialIndex)
}

func evalRunSnapshotSessionID(runID string) string {
	return "eval-run-" + runID
}

func evalExampleArtifactSessionID(exampleID, role, uploadID string) string {
	return "eval-example-" + exampleID + "-" + role + "-" + uploadID
}

type snapshotArtifactsForJudgeArgs struct {
	Ctx            context.Context
	Logger         *slog.Logger
	Artifacts      *artifact.ScopedService
	RunID          string
	ExampleID      string
	AgentSessionID string
	Principal      string
	TrialIndex     int
	Metas          []snapshotArtifactMeta
}

func snapshotArtifactsForJudge(args snapshotArtifactsForJudgeArgs) ([]ArtifactBlock, []snapshotArtifactMeta) {
	ctx := args.Ctx
	logger := args.Logger
	artifacts := args.Artifacts
	runID := args.RunID
	metas := args.Metas
	if len(metas) == 0 {
		return nil, metas
	}
	if artifacts == nil {
		blocks := make([]ArtifactBlock, 0, len(metas))
		for _, m := range metas {
			blocks = append(blocks, ArtifactPlaceholder{
				Filename: m.Filename,
				MIMEType: m.MIMEType,
				Reason:   "Artifact service not configured on the eval execution service.",
			})
		}
		return blocks, metas
	}

	snapshotSessionID := evalRunSnapshotSessionID(runID)
	blocks := make([]ArtifactBlock, 0, len(metas))
	updated := make([]snapshotArtifactMeta, 0, len(metas))

	for _, m := range metas {
		from := artifacts.Resolve("", args.Principal, args.AgentSessionID, m.Filename)
		version := m.Version
		content, mimeType, err := artifacts.Inner.Load(ctx, from, &version)
		if err != nil || content == nil {
			if err != nil {
				logger.Warn("eval: snapshot load failed", "runID", runID, "filename", m.Filename, "error", err)
			}
			blocks = append(blocks, ArtifactPlaceholder{
				Filename: m.Filename,
				MIMEType: m.MIMEType,
				Reason:   "Artifact bytes unavailable at snapshot time.",
			})
			updated = append(updated, m)
			continue
		}
		if mimeType == "" {
			mimeType = m.MIMEType
		}

		to := artifacts.Resolve("", EvalRunnerUserID, snapshotSessionID, m.Filename)
		newVersion, serr := saveSnapshotVersion(ctx, artifacts.Inner, to, content, mimeType)
		if serr != nil {
			logger.Warn("eval: snapshot save failed", "runID", runID, "filename", m.Filename, "error", serr)
			blocks = append(blocks, artifactBlockFromBytes(m.Filename, mimeType, content))
			updated = append(updated, m)
			continue
		}

		copy := m
		if copy.MIMEType == "" {
			copy.MIMEType = mimeType
		}
		copy.AgentVersion = m.Version
		copy.Version = newVersion
		copy.SessionID = snapshotSessionID
		updated = append(updated, copy)

		blocks = append(blocks, artifactBlockFromBytes(m.Filename, mimeType, content))
	}

	return blocks, updated
}

func artifactBlockFromBytes(filename, mimeType string, content []byte) ArtifactBlock {
	if IsTextLike(mimeType, filename) {
		return PrepareTextArtifact(filename, mimeType, content)
	}
	return ArtifactPlaceholder{
		Filename: filename,
		MIMEType: mimeType,
		Reason:   "Binary artifact — bytes are stored but not inlined for the judge.",
	}
}

type snapshotArtifactMeta struct {
	Filename       string   `json:"filename"`
	Version        int      `json:"version"`
	SessionID      string   `json:"artifact_session_id,omitempty"`
	AgentVersion   int      `json:"agent_version,omitempty"`
	MIMEType       string   `json:"mime_type,omitempty"`
	SizeBytes      int64    `json:"size_bytes,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	FunctionCallID *string  `json:"function_call_id,omitempty"`
}

func extractArtifactsMetadata(messages []map[string]any) []snapshotArtifactMeta {
	acc := newArtifactAccumulator()
	for _, m := range messages {
		payload, _ := m["payload"].(map[string]any)
		if len(payload) == 0 {
			continue
		}
		for _, dataPart := range extractDataParts(payload) {
			acc.observe(dataPart)
		}
	}
	return acc.flatten()
}

type artifactKey struct {
	filename string
	version  int
}

type artifactEntry struct {
	meta              snapshotArtifactMeta
	fromArtifactSaved bool
}

type artifactAccumulator struct {
	byKey map[artifactKey]artifactEntry
	order []artifactKey
}

func newArtifactAccumulator() *artifactAccumulator {
	return &artifactAccumulator{byKey: map[artifactKey]artifactEntry{}}
}

func (a *artifactAccumulator) observe(dataPart map[string]any) {
	switch dataType, _ := dataPart["type"].(string); dataType {
	case "artifact_saved":
		a.absorbSaved(dataPart)
	case "artifact_creation_progress":
		a.absorbProgress(dataPart)
	}
}

func (a *artifactAccumulator) absorbSaved(dataPart map[string]any) {
	meta := metaFromDataPart(dataPart, "size_bytes")
	if meta.Filename == "" {
		return
	}
	k := artifactKey{filename: meta.Filename, version: meta.Version}
	a.upsert(k, artifactEntry{meta: meta, fromArtifactSaved: true})
}

func (a *artifactAccumulator) absorbProgress(dataPart map[string]any) {
	if status, _ := dataPart["status"].(string); status != "completed" {
		return
	}
	meta := metaFromDataPart(dataPart, "bytes_transferred")
	if meta.Filename == "" {
		return
	}
	k := artifactKey{filename: meta.Filename, version: meta.Version}
	if existing, exists := a.byKey[k]; exists && existing.fromArtifactSaved {
		return
	}
	a.upsert(k, artifactEntry{meta: meta, fromArtifactSaved: false})
}

func (a *artifactAccumulator) upsert(k artifactKey, e artifactEntry) {
	if _, exists := a.byKey[k]; !exists {
		a.order = append(a.order, k)
	}
	a.byKey[k] = e
}

func (a *artifactAccumulator) flatten() []snapshotArtifactMeta {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]snapshotArtifactMeta, 0, len(a.order))
	for _, k := range a.order {
		out = append(out, a.byKey[k].meta)
	}
	return out
}

func metaFromDataPart(dataPart map[string]any, sizeField string) snapshotArtifactMeta {
	return snapshotArtifactMeta{
		Filename:       artifactStringField(dataPart, "filename"),
		Version:        artifactIntField(dataPart, "version"),
		MIMEType:       artifactStringField(dataPart, "mime_type"),
		SizeBytes:      artifactInt64Field(dataPart, sizeField),
		Description:    artifactOptStringField(dataPart, "description"),
		Tags:           artifactStringSliceField(dataPart, "tags"),
		FunctionCallID: artifactOptStringField(dataPart, "function_call_id"),
	}
}

func encodeArtifactsMetadata(metas []snapshotArtifactMeta) string {
	if len(metas) == 0 {
		return ""
	}
	body, err := json.Marshal(metas)
	if err != nil {
		return ""
	}
	return string(body)
}

func extractDataParts(payload map[string]any) []map[string]any {
	result, _ := payload["result"].(map[string]any)
	if result == nil {
		return nil
	}
	status, _ := result["status"].(map[string]any)
	if status == nil {
		return nil
	}
	message, _ := status["message"].(map[string]any)
	if message == nil {
		return nil
	}
	parts, _ := message["parts"].([]any)
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		mp, _ := p.(map[string]any)
		if mp == nil {
			continue
		}
		kind, _ := mp["kind"].(string)
		if kind != "data" {
			continue
		}
		data, _ := mp["data"].(map[string]any)
		if data == nil {
			continue
		}
		out = append(out, data)
	}
	return out
}

func artifactStringField(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func artifactOptStringField(m map[string]any, k string) *string {
	v, ok := m[k].(string)
	if !ok || v == "" {
		return nil
	}
	return &v
}

func artifactIntField(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func artifactInt64Field(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func artifactStringSliceField(m map[string]any, k string) []string {
	raw, _ := m[k].([]any)
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

const maxSnapshotSaveAttempts = 5

func saveSnapshotVersion(ctx context.Context, svc artifact.Service, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	for attempt := 1; ; attempt++ {
		versions, err := svc.ListVersions(ctx, loc)
		if err != nil {
			return 0, fmt.Errorf("list snapshot versions: %w", err)
		}
		next := 0
		if len(versions) > 0 {
			next = versions[len(versions)-1] + 1
		}
		err = svc.SaveVersion(ctx, loc, next, content, mimeType)
		if err == nil {
			return next, nil
		}
		if !errors.Is(err, artifact.ErrVersionConflict) || attempt == maxSnapshotSaveAttempts {
			return 0, err
		}
	}
}
