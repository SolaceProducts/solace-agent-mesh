// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func copyProjectArtifactsToSession(
	ctx context.Context,
	artSvc *artifact.ScopedService,
	projectID, ownerID, userID, sessionID string,
	indexingEnabled, overwriteExisting bool,
	logger *slog.Logger,
) (int, []string, error) {
	if artSvc == nil || projectID == "" {
		return 0, nil, nil
	}

	projectSessionID := projectSessionPrefix + projectID

	projectScope := artifact.KeyScope{
		AppName:   artSvc.Namespace,
		UserID:    ownerID,
		SessionID: projectSessionID,
	}
	projectKeys, err := artSvc.Inner.ListKeys(ctx, projectScope)
	if err != nil {
		return 0, nil, fmt.Errorf("list project artifacts (project %s): %w", projectID, err)
	}
	if len(projectKeys) == 0 {
		return 0, nil, nil
	}

	var keysToProcess []string
	for _, key := range projectKeys {
		if !indexingEnabled && artifact.IsInternalArtifact(key) {
			continue
		}
		if refusesForeignUserScopedName(userID, ownerID, key) {
			continue
		}
		keysToProcess = append(keysToProcess, key)
	}
	if len(keysToProcess) == 0 {
		return 0, nil, nil
	}

	var sessionKeySet map[string]struct{}
	if !overwriteExisting {
		sessScope := artifact.KeyScope{
			AppName:   artSvc.Namespace,
			UserID:    userID,
			SessionID: sessionID,
		}
		sessKeys, err := artSvc.Inner.ListKeys(ctx, sessScope)
		if err != nil {
			return 0, nil, fmt.Errorf("list session artifacts for dedup (session %s): %w", sessionID, err)
		}
		sessionKeySet = make(map[string]struct{}, len(sessKeys))
		for _, k := range sessKeys {
			sessionKeySet[k] = struct{}{}
		}
	}

	var copied int
	var copiedNames []string
	var failures int

	for _, key := range keysToProcess {
		if sessionKeySet != nil {
			if _, exists := sessionKeySet[key]; exists {
				continue
			}
		}

		srcLoc := artSvc.Resolve("", ownerID, projectSessionID, key)
		content, mimeType, err := artSvc.Inner.Load(ctx, srcLoc, nil)
		if err != nil {
			logger.Warn("failed to load project artifact", "filename", key, "error", err)
			failures++
			continue
		}

		if mimeType == "" || mimeType == "application/octet-stream" {
			if vm, vmErr := artSvc.Inner.GetVersionMetadata(ctx, srcLoc, nil); vmErr == nil && vm != nil && vm.MIMEType != "" {
				mimeType = vm.MIMEType
			}
		}

		var description string
		var tags []string
		var customMeta map[string]any
		if parsed := artifact.LoadCompanionMetadata(ctx, artSvc, "", ownerID, projectSessionID, key, nil); parsed != nil {
			description, _ = parsed["description"].(string)
			if t, ok := parsed["tags"].([]any); ok {
				for _, v := range t {
					if s, ok := v.(string); ok {
						tags = append(tags, s)
					}
				}
			}
			customMeta = make(map[string]any, len(parsed))
			maps.Copy(customMeta, parsed)
		}

		if customMeta == nil {
			customMeta = make(map[string]any)
		}
		customMeta["project_context_pending"] = true

		_, saveErr := artifact.SaveWithMetadata(ctx, artSvc,
			"", userID, sessionID,
			key, content, mimeType,
			artifact.MetadataOpts{
				Description: description,
				Tags:        tags,
				Custom:      customMeta,
			},
			logger,
		)
		if saveErr != nil {
			logger.Warn("failed to copy artifact to session",
				"filename", key, "sessionID", sessionID, "error", saveErr)
			failures++
			continue
		}

		copied++
		copiedNames = append(copiedNames, key)
	}

	if copied > 0 {
		logger.Info("copied project artifacts to session",
			"projectID", projectID, "sessionID", sessionID, "count", copied)
	}

	if copied == 0 && failures > 0 {
		return 0, copiedNames, fmt.Errorf("copy project artifacts to session %s: all %d artifact(s) failed", sessionID, failures)
	}

	return copied, copiedNames, nil
}

func forkableArtifacts(
	ctx context.Context,
	artSvc *artifact.ScopedService,
	srcUserID, srcSessionID string,
	refuses func(filename string) bool,
) (keys []string, refused int, err error) {
	if artSvc == nil {
		return nil, 0, nil
	}
	all, err := artSvc.Inner.ListKeys(ctx, artifact.KeyScope{
		AppName:   artSvc.Namespace,
		UserID:    srcUserID,
		SessionID: srcSessionID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list source artifacts for fork copy (session %s): %w", srcSessionID, err)
	}
	for _, key := range all {
		switch {
		case artifact.IsInternalArtifact(key):
		case refuses(key):
			refused++
		default:
			keys = append(keys, key)
		}
	}
	return keys, refused, nil
}

type forkCopy struct {
	artSvc                  *artifact.ScopedService
	srcUserID, srcSessionID string
	dstUserID, dstSessionID string
	logger                  *slog.Logger
}

func copySessionArtifacts(ctx context.Context, c forkCopy, keys []string, pinned map[string]int) (int, []string) {
	if c.artSvc == nil {
		return 0, nil
	}

	var copiedNames []string
	for _, key := range keys {
		if _, userScoped := artifact.ResolveFilename(key); userScoped && c.srcUserID == c.dstUserID {
			continue
		}
		var version *int
		if pinnedVersion, isPinned := pinned[key]; isPinned {
			version = &pinnedVersion
		}
		if c.copyOne(ctx, key, version) {
			copiedNames = append(copiedNames, key)
		}
	}

	if len(copiedNames) > 0 {
		c.logger.Info("copied artifacts to forked session", "count", len(copiedNames), "dstSessionID", c.dstSessionID)
	}
	return len(copiedNames), copiedNames
}

func (c forkCopy) copyOne(ctx context.Context, key string, version *int) bool {
	src := c.artSvc.Resolve("", c.srcUserID, c.srcSessionID, key)
	content, mimeType, loadErr := c.artSvc.Inner.Load(ctx, src, version)
	if loadErr != nil {
		c.logger.Warn("load source artifact for fork copy", "filename", key, "error", loadErr)
		return false
	}
	if content == nil {
		c.logger.Warn("source artifact for fork copy is gone", "filename", key)
		return false
	}

	if mimeType == "" || mimeType == "application/octet-stream" {
		if vm, vmErr := c.artSvc.Inner.GetVersionMetadata(ctx, src, version); vmErr == nil && vm != nil && vm.MIMEType != "" {
			mimeType = vm.MIMEType
		}
	}

	opts := companionMetadataOpts(artifact.LoadCompanionMetadataAtLocator(ctx, c.artSvc, src, version))
	if _, saveErr := artifact.SaveWithMetadata(ctx, c.artSvc, "", c.dstUserID, c.dstSessionID, key, content, mimeType, opts, c.logger); saveErr != nil {
		c.logger.Warn("save forked artifact", "filename", key, "dstSessionID", c.dstSessionID, "error", saveErr)
		return false
	}
	return true
}

func companionMetadataOpts(parsed map[string]any) artifact.MetadataOpts {
	if parsed == nil {
		return artifact.MetadataOpts{}
	}
	description, _ := parsed["description"].(string)
	var tags []string
	if list, ok := parsed["tags"].([]any); ok {
		for _, v := range list {
			if tag, ok := v.(string); ok {
				tags = append(tags, tag)
			}
		}
	}
	custom := make(map[string]any, len(parsed))
	maps.Copy(custom, parsed)
	return artifact.MetadataOpts{Description: description, Tags: tags, Custom: custom}
}
