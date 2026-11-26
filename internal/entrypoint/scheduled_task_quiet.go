// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const (
	webContentArtifactPrefix = "web_content_"
	toolResultArtifactPrefix = "tool_result_"
	toolResultArtifactSuffix = ".json"

	defaultQuietStoreCallTimeout = 5 * time.Second
)

var (
	errNoArtifactStore           = errors.New("the gateway has no artifact store")
	errArtifactScopeNotNamespace = errors.New("the gateway cannot list the artifact scope the agents save to")
)

func artifactCheckFailure(err error) string {
	switch {
	case errors.Is(err, errNoArtifactStore):
		return "no_artifact_store"
	case errors.Is(err, errArtifactScopeNotNamespace):
		return "scope_not_namespace"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "list_failed"
}

func (s *ScheduledTaskService) quietRunsEnabled() bool {
	if s.core == nil || s.core.svc == nil {
		return false
	}
	reg := s.core.svc.FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyQuietScheduledRuns)
}

func (s *ScheduledTaskService) allowQuietEnd(metadata map[string]any, task *store.ScheduledTask) {
	if task.TargetType == "workflow" || !s.quietRunsEnabled() {
		return
	}
	metadata[a2a.MetadataQuietEndAllowed] = true
}

func endedQuietly(task *a2a.Task) bool {
	if task == nil {
		return false
	}
	outcome, _ := task.Metadata[a2a.MetadataRunOutcome].(string)
	return outcome == a2a.RunOutcomeQuiet
}

func autoNamedArtifact(name string) bool {
	return strings.HasPrefix(name, webContentArtifactPrefix) ||
		strings.HasPrefix(name, toolResultArtifactPrefix) && strings.HasSuffix(name, toolResultArtifactSuffix)
}

func servableSessionKey(key string) bool {
	if key == "" || artifact.IsInternalArtifact(key) || autoNamedArtifact(key) || a2a.IsWorkflowHandoffArtifactName(key) {
		return false
	}
	_, userScoped := artifact.ResolveFilename(key)
	return !userScoped
}

func (s *ScheduledTaskService) quietStoreCallTimeout() time.Duration {
	if s.quietStoreTimeout > 0 {
		return s.quietStoreTimeout
	}
	return defaultQuietStoreCallTimeout
}

func (s *ScheduledTaskService) sessionHoldsServableFile(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, sessionID string, produced []string) (bool, error) {
	if s.core == nil || s.core.svc == nil {
		return false, errNoArtifactStore
	}
	artSvc := s.core.Services().Artifacts()
	if artSvc == nil {
		return false, errNoArtifactStore
	}
	if artSvc.Scope != artifact.ScopeNamespace {
		return false, errArtifactScopeNotNamespace
	}
	ctx, cancel := context.WithTimeout(ctx, s.quietStoreCallTimeout())
	defer cancel()
	owner := run.ArtifactUserID(task)
	if held, err := holdsServableFile(ctx, artSvc, owner, sessionID, produced); held || err != nil {
		return held, err
	}
	scope := artSvc.Resolve("", owner, sessionID, "")
	keys, err := artifact.ListKeysStrict(ctx, artSvc.Inner, artifact.KeyScope{AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.SessionID})
	if err != nil {
		return false, err
	}
	return holdsServableFile(ctx, artSvc, owner, sessionID, keys)
}

func holdsServableFile(ctx context.Context, artSvc *artifact.ScopedService, owner, sessionID string, names []string) (bool, error) {
	for _, name := range names {
		if !servableSessionKey(name) {
			continue
		}
		working, err := workingFile(ctx, artSvc, artSvc.Resolve("", owner, sessionID, name))
		if err != nil {
			return false, err
		}
		if !working {
			return true, nil
		}
	}
	return false, nil
}

func workingFile(ctx context.Context, artSvc *artifact.ScopedService, loc artifact.Locator) (bool, error) {
	tags := companionTags(ctx, artSvc, loc, nil)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return slices.Contains(tags, a2a.ArtifactTagWorking), nil
}
