// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

const indexEventType = "index_message"

type projectIndexer struct {
	artSvc     *artifact.ScopedService
	sseManager *SSEManager
	eventLog   eventlog.TaskEventLog
	logger     *slog.Logger

	pdfExtractorFactory func(userID, projectID, authToken string) convert.PDFExtractor

	inFlight rebuildsInFlight
}

const maxIndexSaveAttempts = 3

func (ix *projectIndexer) publishIndexEvent(taskID string, payload map[string]any, final bool) {
	if ix.sseManager == nil || taskID == "" {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		ix.logger.Warn("marshal index event", "error", err)
		return
	}
	var seq int64
	if ix.eventLog != nil {
		detachedCtx := context.Background()
		sessionID, userID := ix.sseManager.TaskContext(taskID)
		eventSessionID := strings.TrimPrefix(sessionID, projectSessionPrefix)
		seq, err = ix.eventLog.Append(detachedCtx, taskID, eventSessionID, userID, indexEventType, data)
		if err != nil {
			if final {
				ix.logger.Error("append terminal index event to event log; reconnect will miss completion", "taskID", taskID, "error", err)
			} else {
				ix.logger.Warn("append index event to event log", "taskID", taskID, "error", err)
			}
		}
		if final {
			if seq == 0 {
				ix.sseManager.Publish(taskID, &SSEEvent{EventType: indexEventType, Data: data, Final: true})
			}
			if err := ix.eventLog.Complete(detachedCtx, taskID); err != nil {
				ix.logger.Error("mark indexed task complete in durable log failed; reconnect after forget/restart may poll until client disconnect",
					"taskID", taskID, "error", err)
			}
			if seq == 0 {
				return
			}
		}
	}
	ix.sseManager.Publish(taskID, &SSEEvent{EventType: indexEventType, Data: data, Final: final, Sequence: seq})
}

func (ix *projectIndexer) startIndexRebuild(w http.ResponseWriter, r *http.Request, projectID, userID string) string {
	var taskID string
	if ix.sseManager != nil {
		taskID = idgen.New()
		ix.sseManager.RegisterTask(taskID, projectSessionPrefix+projectID, userID)
		w.Header().Set("Sse-Location", "/api/v1/sse/subscribe/"+taskID)
	}
	ix.triggerIndexRebuild(projectID, userID, bearerToken(r), taskID)
	return taskID
}

func bearerToken(r *http.Request) string {
	token, _ := apikey.BearerCredential(r.Header.Get("Authorization"))
	return token
}

func (ix *projectIndexer) triggerIndexRebuild(projectID, userID, authToken, taskID string) {
	leave := ix.inFlight.enter(projectID)
	go ix.runRebuild(leave, projectID, userID, authToken, taskID)
}

func (ix *projectIndexer) triggerIndexRebuildIfIdle(projectID, userID, authToken string) {
	leave, ok := ix.inFlight.enterIfIdle(projectID)
	if !ok {
		return
	}
	go ix.runRebuild(leave, projectID, userID, authToken, "")
}

func (ix *projectIndexer) runRebuild(leave func(), projectID, userID, authToken, taskID string) {
	defer leave()
	ix.rebuildIndex(context.Background(), projectID, userID, authToken, taskID)
}

type rebuildsInFlight struct {
	mu      sync.Mutex
	running map[string]int
}

func (r *rebuildsInFlight) enter(projectID string) (leave func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enterLocked(projectID)
}

func (r *rebuildsInFlight) enterIfIdle(projectID string) (leave func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[projectID] > 0 {
		return nil, false
	}
	return r.enterLocked(projectID), true
}

func (r *rebuildsInFlight) enterLocked(projectID string) func() {
	if r.running == nil {
		r.running = make(map[string]int)
	}
	r.running[projectID]++
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.running[projectID]--; r.running[projectID] == 0 {
			delete(r.running, projectID)
		}
	}
}

func (r *rebuildsInFlight) count(projectID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[projectID]
}

func (ix *projectIndexer) rebuildIndex(ctx context.Context, projectID, userID, authToken, taskID string) {
	defer ix.publishIndexEvent(taskID, map[string]any{"type": "task_completed"}, true)
	defer func() {
		if rec := recover(); rec != nil {
			ix.logger.Error("index rebuild panicked", "projectID", projectID, "panic", rec)
			ix.publishIndexEvent(taskID, map[string]any{"type": "task_error", "error": fmt.Sprintf("%v", rec)}, true)
		}
	}()

	var pdfExtractor convert.PDFExtractor
	if ix.pdfExtractorFactory != nil {
		pdfExtractor = ix.pdfExtractorFactory(userID, projectID, authToken)
	}

	for attempt := 1; ; attempt++ {
		indexed, failures, err := ix.saveNextIndex(ctx, projectID, userID, pdfExtractor, attempt > 1)
		if errors.Is(err, artifact.ErrVersionConflict) && attempt < maxIndexSaveAttempts {
			ix.logger.Warn("index version saved by another rebuild; rebuilding", "projectID", projectID, "attempt", attempt)
			continue
		}
		if errors.Is(err, artifact.ErrVersionConflict) {
			if n, ok := ix.currentIndex(ctx, projectID, userID); ok {
				indexed, err = n, nil
			}
		}
		for _, f := range failures {
			ix.publishIndexEvent(taskID, map[string]any{"type": "conversion_failed", "file": f.file, "error": f.err}, false)
		}
		switch {
		case errors.Is(err, artifact.ErrVersionConflict):
			ix.logger.Error("index rebuild gave up after repeated version conflicts", "projectID", projectID, "attempts", attempt)
			ix.publishIndexEvent(taskID, map[string]any{"type": "indexing_failed", "error": err.Error()}, false)
		case err != nil:
			ix.logger.Warn("index rebuild failed", "projectID", projectID, "error", err)
			ix.publishIndexEvent(taskID, map[string]any{"type": "indexing_failed", "error": err.Error()}, false)
		case indexed == 0:
			ix.logger.Info("project index cleared (no indexable files)", "projectID", projectID)
		default:
			ix.logger.Info("project index rebuilt", "projectID", projectID)
		}
		return
	}
}

type conversionFailure struct {
	file, err string
}

func (ix *projectIndexer) saveNextIndex(ctx context.Context, projectID, userID string, pdfExtractor convert.PDFExtractor, skipIfCurrent bool) (indexed int, failures []conversionFailure, err error) {
	loc := ix.artSvc.Resolve("", userID, projectSessionPrefix+projectID, artifact.BM25IndexFilename)
	versions, err := ix.artSvc.Inner.ListVersions(ctx, loc)
	if err != nil {
		return 0, nil, fmt.Errorf("list index versions: %w", err)
	}
	latest := -1
	if len(versions) > 0 {
		latest = versions[len(versions)-1]
	}

	sources, err := projectSourceFiles(ctx, ix.artSvc, projectID, userID)
	if err != nil {
		return 0, nil, err
	}

	if skipIfCurrent && latest >= 0 {
		if current, ok := ix.indexSourcesAt(ctx, loc, latest); ok && indexing.SameSourceFiles(current.Files, sources) {
			return current.IndexedFiles, nil, nil
		}
	}

	onConversionFailure := func(filename, errMsg string) {
		failures = append(failures, conversionFailure{file: filename, err: errMsg})
	}
	zipData, err := indexing.BuildProjectIndex(ctx, projectID, userID, ix.artSvc, sources, pdfExtractor, ix.logger, onConversionFailure)
	if err != nil {
		return 0, failures, err
	}
	if err := ix.artSvc.Inner.SaveVersion(ctx, loc, latest+1, zipData, "application/zip"); err != nil {
		if errors.Is(err, artifact.ErrVersionConflict) {
			return 0, nil, err
		}
		return 0, failures, fmt.Errorf("save index: %w", err)
	}
	built, _, _ := indexing.ReadSources(zipData)
	return built.IndexedFiles, failures, nil
}

func (ix *projectIndexer) currentIndex(ctx context.Context, projectID, userID string) (int, bool) {
	loc := ix.artSvc.Resolve("", userID, projectSessionPrefix+projectID, artifact.BM25IndexFilename)
	versions, err := ix.artSvc.Inner.ListVersions(ctx, loc)
	if err != nil || len(versions) == 0 {
		return 0, false
	}
	sources, err := projectSourceFiles(ctx, ix.artSvc, projectID, userID)
	if err != nil {
		return 0, false
	}
	current, ok := ix.indexSourcesAt(ctx, loc, versions[len(versions)-1])
	if !ok || !indexing.SameSourceFiles(current.Files, sources) {
		return 0, false
	}
	return current.IndexedFiles, true
}

func (ix *projectIndexer) indexSourcesAt(ctx context.Context, loc artifact.Locator, version int) (indexing.Sources, bool) {
	data, _, err := ix.artSvc.Inner.Load(ctx, loc, &version)
	if err != nil || len(data) == 0 {
		return indexing.Sources{}, false
	}
	sources, found, err := indexing.ReadSources(data)
	if err != nil || !found {
		return indexing.Sources{}, false
	}
	return sources, true
}

func projectSourceFiles(ctx context.Context, artSvc *artifact.ScopedService, projectID, ownerID string) ([]indexing.SourceFile, error) {
	scope := artifact.KeyScope{AppName: artSvc.Namespace, UserID: ownerID, SessionID: projectSessionPrefix + projectID}
	if vl, ok := unwrapArtifactService(artSvc.Inner).(scopeArtifactVersionLister); ok {
		versionsByName, err := vl.ListArtifactVersions(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("list project files: %w", err)
		}
		return indexing.ProjectSourceFiles(versionsByName), nil
	}
	keys, err := artSvc.Inner.ListKeys(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list project files: %w", err)
	}
	versionsByName := make(map[string][]int, len(keys))
	for _, key := range keys {
		if !indexing.IsIndexSource(key) {
			continue
		}
		versions, err := artSvc.Inner.ListVersions(ctx, artSvc.Resolve("", ownerID, scope.SessionID, key))
		if err != nil {
			return nil, fmt.Errorf("list versions of project file: %w", err)
		}
		versionsByName[key] = versions
	}
	return indexing.ProjectSourceFiles(versionsByName), nil
}

func (ix *projectIndexer) reconcileIndex(ctx context.Context, projectID, ownerID, authToken string, indexData []byte) bool {
	var sources indexing.Sources
	found := false
	if len(indexData) > 0 {
		sources, found, _ = indexing.ReadSources(indexData)
	}
	searchable := len(indexData) > 0 && (!found || sources.IndexedFiles > 0)

	current, err := projectSourceFiles(ctx, ix.artSvc, projectID, ownerID)
	if err != nil {
		ix.logger.Warn("project index freshness check failed", "projectID", projectID, "error", err)
		return searchable
	}
	stale := len(current) > 0
	if len(indexData) > 0 {
		stale = !found || !indexing.SameSourceFiles(sources.Files, current)
	}
	if stale {
		ix.logger.Debug("project index is behind the project files; rebuilding", "projectID", projectID)
		ix.triggerIndexRebuildIfIdle(projectID, ownerID, authToken)
	}
	return searchable
}
