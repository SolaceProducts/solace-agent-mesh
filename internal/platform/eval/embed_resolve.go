// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
)

const evalEmbedMaxArtifactBytes = maxArtifactDecodeBytes

func (s *ExecutionService) resolveResponseEmbeds(ctx context.Context, task *pendingTask, text string) string {
	if s.artifacts == nil || task == nil {
		return text
	}
	rctx := &embed.ResolveContext{
		Artifacts: s.artifacts,
		AgentName: task.agentName,
		UserID:    task.userScope(),
		SessionID: task.taskID,
		Config: embed.ResolveConfig{
			MaxDepth:             12,
			MaxArtifactSizeBytes: evalEmbedMaxArtifactBytes,
			MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
		},
		Logger: s.logger,
	}
	resolved, _, err := embed.ResolveEmbedsInString(ctx, text, embed.ModeA2AMessageToUser, rctx)
	if err != nil {
		s.logger.Warn("eval: embed resolution failed; scoring raw response",
			"taskID", task.taskID, "error", err)
		return text
	}
	return resolved
}

func (s *ExecutionService) snapshotProducedArtifacts(ctx context.Context, task *pendingTask) []ArtifactBlock {
	if s.artifacts == nil || task == nil {
		return nil
	}
	base := s.artifacts.Resolve(task.agentName, task.userScope(), task.taskID, "")
	keys, err := s.artifacts.Inner.ListKeys(ctx, artifact.KeyScope{
		AppName:   base.AppName,
		UserID:    base.UserID,
		SessionID: base.SessionID,
	})
	if err != nil {
		s.logger.Warn("eval: list produced artifacts failed",
			"taskID", task.taskID, "error", err)
		return nil
	}
	var blocks []ArtifactBlock
	var totalBytes int64
	for _, key := range keys {
		loc := s.artifacts.Resolve(task.agentName, task.userScope(), task.taskID, key)
		blocks = append(blocks, s.snapshotArtifact(ctx, loc, key, &totalBytes))
	}
	return blocks
}

func (s *ExecutionService) snapshotArtifact(ctx context.Context, loc artifact.Locator, filename string, totalBytes *int64) ArtifactBlock {
	var mimeType string
	if meta, err := s.artifacts.Inner.GetVersionMetadata(ctx, loc, nil); err == nil && meta != nil {
		mimeType = meta.MIMEType
		if meta.SizeBytes > maxArtifactResolveBytes {
			return FilenameOnlyPlaceholder(filename, mimeType, artifactReasonTooLarge)
		}
		if *totalBytes+meta.SizeBytes > embed.DefaultMaxTotalSizeBytes {
			return FilenameOnlyPlaceholder(filename, mimeType, artifactReasonTooLarge)
		}
	}
	if !IsTextLike(mimeType, filename) {
		return FilenameOnlyPlaceholder(filename, mimeType, artifactReasonBinary)
	}
	content, loadedMIME, err := s.artifacts.Inner.Load(ctx, loc, nil)
	if err != nil || content == nil {
		return FilenameOnlyPlaceholder(filename, mimeType, artifactReasonLoadFailed)
	}
	*totalBytes += int64(len(content))
	if loadedMIME != "" {
		mimeType = loadedMIME
	}
	return PrepareTextArtifact(filename, mimeType, content)
}
