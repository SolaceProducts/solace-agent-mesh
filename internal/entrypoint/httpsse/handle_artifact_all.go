// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

type ArtifactAllItemResponse struct {
	ArtifactInfoResponse
	SessionID   string `json:"sessionId"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
}

type artifactCache struct {
	mu      sync.Mutex
	entries map[string]*artifactCacheEntry
	ttl     time.Duration
	maxSize int
}

type artifactCacheEntry struct {
	items     []ArtifactAllItemResponse
	expiresAt time.Time
}

func newArtifactCache(ttl time.Duration, maxSize int) *artifactCache {
	return &artifactCache{
		entries: make(map[string]*artifactCacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

func (c *artifactCache) get(userID string) ([]ArtifactAllItemResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[userID]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(c.entries, userID)
		return nil, false
	}
	return entry.items, true
}

func (c *artifactCache) set(userID string, items []ArtifactAllItemResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range c.entries {
			if oldestKey == "" || v.expiresAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.expiresAt
			}
		}
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}
	c.entries[userID] = &artifactCacheEntry{
		items:     items,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *artifactCache) invalidate(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, userID)
}

func (h *artifactHandler) handleListAll(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, h.core)
	page := apicontract.Page{Number: queryInt(r, "pageNumber", 1), Size: queryInt(r, "pageSize", 20)}
	search := r.URL.Query().Get("search")
	projectID := r.URL.Query().Get("projectId")

	if projectID != "" && projectID != store.UnassignedProjectFilter {
		if _, err := uuid.Parse(projectID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid projectId: must be a UUID")
			return
		}
	}

	items, total, fail := h.listAllFiles(r.Context(), userID, page, search, projectID)
	if fail != nil {
		if r.Context().Err() == nil {
			fail.write(w)
		}
		return
	}
	shown := clampedAllPage(page)
	writeJSON(w, http.StatusOK, PaginatedResponse[ArtifactAllItemResponse]{
		Data: nonNilList(items),
		Meta: PaginationMeta{Pagination: newPaginationInfo(shown.Number, shown.Size, total)},
	})
}

func clampedAllPage(page apicontract.Page) apicontract.Page {
	size := page.Size
	switch {
	case size < 1:
		size = defaultAllPageSize
	case size > maxAllPageSize:
		size = maxAllPageSize
	}
	return apicontract.Page{Number: max(page.Number, 1), Size: size}
}

const (
	defaultAllPageSize = 20
	maxAllPageSize     = 100

	statusClientClosed = 499
)

func (h *artifactHandler) listAllFiles(ctx context.Context, userID string, page apicontract.Page, search, projectID string) ([]ArtifactAllItemResponse, int, *sessionFailure) {
	allItems, fail := h.cachedAllArtifacts(ctx, userID)
	if fail != nil {
		return nil, 0, fail
	}
	filtered := filterAllArtifacts(allItems, search, projectID)
	p := clampedAllPage(page)
	start := min((p.Number-1)*p.Size, len(filtered))
	end := min(start+p.Size, len(filtered))
	return filtered[start:end], len(filtered), nil
}

func (h *artifactHandler) cachedAllArtifacts(ctx context.Context, userID string) ([]ArtifactAllItemResponse, *sessionFailure) {
	if allItems, cached := h.allCache.get(userID); cached {
		return allItems, nil
	}
	items, failed, err := h.collectAllArtifacts(ctx, userID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failSession(statusClientClosed, "request canceled")
		}
		h.logger.Error("collect all artifacts error", "error", err)
		return nil, failSession(http.StatusInternalServerError, "failed to list artifacts")
	}

	if failed > 0 && len(items) == 0 {
		h.logger.Warn("artifact store unavailable: all session listings failed", "userID", userID, "failedSessions", failed)
		return nil, failSession(http.StatusServiceUnavailable, "artifact store temporarily unavailable")
	}
	if failed > 0 {

		h.logger.Warn("artifact list served degraded; some sessions failed to list", "userID", userID, "failedSessions", failed, "served", len(items))
		return items, nil
	}
	h.allCache.set(userID, items)
	return items, nil
}

func filterAllArtifacts(items []ArtifactAllItemResponse, search, projectID string) []ArtifactAllItemResponse {
	if search == "" && projectID == "" {
		return items
	}
	searchLower := strings.ToLower(search)
	filtered := make([]ArtifactAllItemResponse, 0, len(items))
	for _, item := range items {
		switch {
		case projectID == store.UnassignedProjectFilter:
			if item.ProjectID != "" {
				continue
			}
		case projectID != "":
			if item.ProjectID != projectID {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(item.Filename), searchLower) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

type sessionArtifactPrefilterer interface {
	ListSessionsWithArtifactsForUser(ctx context.Context, appName, userID string) (map[string]struct{}, bool, error)
}

type scopeArtifactVersionLister interface {
	ListArtifactVersions(ctx context.Context, scope artifact.KeyScope) (map[string][]int, error)
}

func (h *artifactHandler) logListingCapabilities() {
	backend := unwrapArtifactService(h.artSvc.Inner)
	_, hasVersionLister := backend.(scopeArtifactVersionLister)
	_, hasPrefilter := backend.(sessionArtifactPrefilterer)
	h.logger.Info("artifact listing capabilities",
		"backend", fmt.Sprintf("%T", backend),
		"scopeVersionLister", hasVersionLister,
		"sessionPrefilter", hasPrefilter)
}

func (h *artifactHandler) collectAllArtifacts(ctx context.Context, userID string) ([]ArtifactAllItemResponse, int, error) {

	seen := make(map[string]ArtifactAllItemResponse)

	var failed int

	if h.sessRepo != nil {
		sessions, _, err := h.sessRepo.ListByUser(ctx, userID, false, false, false, "", nil, nil, 1000, 0)
		if err != nil {
			return nil, 0, err
		}
		failed, err = h.collectFromUserSessions(ctx, userID, sessions, seen)
		if err != nil {
			return nil, 0, err
		}
	}

	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}

	result := make([]ArtifactAllItemResponse, 0, len(seen))
	for _, item := range seen {
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b ArtifactAllItemResponse) int {
		return cmp.Compare(a.Filename, b.Filename)
	})

	return result, failed, nil
}

type scopeJob struct {
	storageUserID    string
	storageSessionID string
	projectID        string
	projectName      string
	agentID          string
}

const scopeArtifactScanConcurrency = 16

func (h *artifactHandler) collectFromUserSessions(ctx context.Context, userID string, sessions []*store.Session, seen map[string]ArtifactAllItemResponse) (int, error) {
	jobs, projectsComplete := h.scopeJobs(ctx, userID, sessions)

	versionsByJob, failed, err := h.scanScopesParallel(ctx, userID, jobs)
	if err != nil {
		return 0, err
	}

	if !projectsComplete {
		failed++
	}

	itemsByJob := h.buildScopeItems(ctx, userID, jobs, versionsByJob)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	mergeScopeItems(jobs, itemsByJob, seen)
	return failed, nil
}

func (h *artifactHandler) scopeJobs(ctx context.Context, userID string, sessions []*store.Session) ([]scopeJob, bool) {
	sessionIDs := make([]string, 0, len(sessions))
	agentBySession := make(map[string]string, len(sessions))
	sessionProjectIDs := make(map[string]struct{})
	for _, sess := range sessions {
		sessionIDs = append(sessionIDs, sess.ID)
		if sess.AgentID != nil {
			agentBySession[sess.ID] = *sess.AgentID
		}
		if sess.ProjectID != nil && *sess.ProjectID != "" {
			sessionProjectIDs[*sess.ProjectID] = struct{}{}
		}
	}

	projectJobs, projectsComplete := h.projectScopeJobs(ctx, userID, sessionProjectIDs)

	sessionFilter, hasFilter := h.prefilterSessionsWithArtifacts(ctx, userID)

	jobs := make([]scopeJob, 0, len(projectJobs)+len(sessionIDs))
	jobs = append(jobs, projectJobs...)
	for _, sessID := range sessionIDs {
		if hasFilter && !keepSession(sessID, sessionFilter) {
			continue
		}
		jobs = append(jobs, scopeJob{storageUserID: userID, storageSessionID: sessID, agentID: agentBySession[sessID]})
	}
	return jobs, projectsComplete
}

func (h *artifactHandler) projectScopeJobs(ctx context.Context, userID string, sessionProjectIDs map[string]struct{}) ([]scopeJob, bool) {
	type projInfo struct {
		owner string
		name  string
	}
	byID := make(map[string]projInfo)
	complete := true

	if h.projectRepo != nil {
		projects, ok := h.listAccessibleProjects(ctx, userID)
		if !ok {
			complete = false
		}
		for _, p := range projects {
			byID[p.ID] = projInfo{owner: p.UserID, name: p.Name}
		}
	}

	for id := range sessionProjectIDs {
		if _, ok := byID[id]; !ok {
			byID[id] = projInfo{owner: userID}
		}
	}

	jobs := make([]scopeJob, 0, len(byID))
	for id, info := range byID {
		jobs = append(jobs, scopeJob{
			storageUserID:    cmp.Or(info.owner, userID),
			storageSessionID: projectSessionPrefix + id,
			projectID:        id,
			projectName:      info.name,
		})
	}
	return jobs, complete
}

func (h *artifactHandler) listAccessibleProjects(ctx context.Context, userID string) ([]*store.Project, bool) {
	if svc := h.core.ProjectSharing(); svc != nil {
		sharedIDs, err := svc.GetSharedProjectIDs(ctx, userID)
		if err != nil {
			h.logger.Warn("get shared project IDs failed; listing owned projects only", "userID", userID, "error", err)
			projects, listErr := h.projectRepo.ListByUser(ctx, userID)
			if listErr != nil {
				h.logger.Warn("list owned projects failed", "userID", userID, "error", listErr)
				return nil, false
			}
			return projects, false
		}
		projects, err := h.projectRepo.ListAccessible(ctx, userID, sharedIDs)
		if err != nil {
			h.logger.Warn("list accessible projects failed", "userID", userID, "error", err)
			return nil, false
		}
		return projects, true
	}
	projects, err := h.projectRepo.ListByUser(ctx, userID)
	if err != nil {
		h.logger.Warn("list owned projects failed", "userID", userID, "error", err)
		return nil, false
	}
	return projects, true
}

func (h *artifactHandler) scanScopesParallel(ctx context.Context, userID string, jobs []scopeJob) ([]map[string][]int, int, error) {
	versionsByJob := make([]map[string][]int, len(jobs))
	scanErrs := make([]error, len(jobs))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(scopeArtifactScanConcurrency)
	for i, job := range jobs {
		g.Go(func() error {
			scope := artifact.KeyScope{AppName: h.artSvc.Namespace, UserID: job.storageUserID, SessionID: job.storageSessionID}
			vbn, err := scopeArtifactVersions(gctx, h.artSvc, scope)
			if err != nil {
				scanErrs[i] = err
				return nil
			}
			versionsByJob[i] = vbn
			return nil
		})
	}
	_ = g.Wait()

	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}

	var failed int
	for i := range jobs {
		if scanErrs[i] != nil {
			h.logger.Warn("list artifact versions error", "userID", userID, "sessionID", jobs[i].storageSessionID, "error", scanErrs[i])
			failed++
		}
	}
	return versionsByJob, failed, nil
}

func (h *artifactHandler) buildScopeItems(ctx context.Context, requester string, jobs []scopeJob, versionsByJob []map[string][]int) [][]ArtifactAllItemResponse {
	type task struct {
		jobIdx           int
		loc              artifact.Locator
		versions         []int
		companionVersion *int
	}
	total := 0
	for _, versionsByName := range versionsByJob {
		total += len(versionsByName)
	}
	tasks := make([]task, 0, total)
	for ji, versionsByName := range versionsByJob {
		if versionsByName == nil {
			continue
		}
		storageUserID := jobs[ji].storageUserID
		storageSessionID := jobs[ji].storageSessionID
		for name, versions := range versionsByName {
			if artifact.IsInternalArtifact(name) || refusesForeignUserScopedName(requester, storageUserID, name) {
				continue
			}
			loc := h.artSvc.Resolve("", storageUserID, storageSessionID, name)
			tasks = append(tasks, task{
				jobIdx:           ji,
				loc:              loc,
				versions:         versions,
				companionVersion: latestCompanionVersion(versionsByName, name),
			})
		}
	}

	infos := make([]ArtifactInfoResponse, len(tasks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(artifactListMetadataConcurrency)
	for i, t := range tasks {
		g.Go(func() error {
			infos[i] = getArtifactInfoFromServiceWithVersions(gctx, h.artSvc, t.loc, t.versions, t.companionVersion)
			return nil
		})
	}
	_ = g.Wait()

	itemsByJob := make([][]ArtifactAllItemResponse, len(jobs))
	for i, t := range tasks {
		info := infos[i]
		if isWorkingArtifact(info) {
			continue
		}
		job := jobs[t.jobIdx]
		itemsByJob[t.jobIdx] = append(itemsByJob[t.jobIdx], ArtifactAllItemResponse{
			ArtifactInfoResponse: info,
			SessionID:            job.storageSessionID,
			ProjectID:            job.projectID,
			ProjectName:          job.projectName,
			AgentID:              job.agentID,
		})
	}
	return itemsByJob
}

func mergeScopeItems(jobs []scopeJob, itemsByJob [][]ArtifactAllItemResponse, seen map[string]ArtifactAllItemResponse) {
	for ji := range jobs {
		isProject := jobs[ji].projectID != ""
		for _, item := range itemsByJob[ji] {
			if isProject {
				seen[item.Filename] = item
				continue
			}
			if _, exists := seen[item.Filename]; !exists {
				seen[item.Filename] = item
			}
		}
	}
}

func (h *artifactHandler) prefilterSessionsWithArtifacts(ctx context.Context, userID string) (map[string]struct{}, bool) {
	prefilter, ok := unwrapArtifactService(h.artSvc.Inner).(sessionArtifactPrefilterer)
	if !ok {
		return nil, false
	}
	sessions, hasFilter, err := prefilter.ListSessionsWithArtifactsForUser(ctx, h.artSvc.Namespace, userID)
	if err != nil {
		h.logger.Warn("artifact session prefilter failed; falling back to full scan", "error", err)
		return nil, false
	}
	return sessions, hasFilter
}

func keepSession(sessionID string, filter map[string]struct{}) bool {
	_, ok := filter[sessionID]
	return ok
}

func (h *artifactHandler) invalidateAllCache(userID string) {
	if h.allCache != nil {
		h.allCache.invalidate(userID)
	}
}
