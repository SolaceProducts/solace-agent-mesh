// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	feedbackNegativeSampleSize = 10
	feedbackRowsLimit          = 50
	feedbackSummaryPageSize    = 50
	feedbackRatingDown         = "down"
)

type activityFeedbackRequest struct {
	Window         string   `json:"window"`
	StartDate      int64    `json:"startDate"`
	EndDate        int64    `json:"endDate"`
	Subjects       []string `json:"subjects"`
	FeedbackUserID string   `json:"feedbackUserId"`
	Rating         string   `json:"rating"`
	SubjectType    string   `json:"subjectType"`
	SortBy         string   `json:"sortBy"`
	FeedbackID     string   `json:"feedbackId"`
}

const maxFeedbackSubjects = 100

type feedbackQueryParts struct {
	scope       activityFeedbackScope
	match       subjectMatch
	userID      string
	start, end  int64
	candidates  []analytics.AgentRef
	unmatched   []string
	unresolved  bool
	otherUser   string
	subjectType string
	ratingsOnly bool
}

func (e *GatewayExecutor) planActivityFeedback(req activityFeedbackRequest, identity *auth.Identity, callerUserID string, isAdmin bool) (feedbackQueryParts, error) {
	var p feedbackQueryParts
	switch req.SubjectType {
	case "", store.SubjectTypeAgent, store.SubjectTypeWorkflow:
	default:
		return p, fmt.Errorf("activity: subjectType must be 'agent' or 'workflow'")
	}
	if len(req.Subjects) > maxFeedbackSubjects {
		return p, fmt.Errorf("activity: at most %d subjects per request, got %d", maxFeedbackSubjects, len(req.Subjects))
	}
	switch req.Rating {
	case "", "up", feedbackRatingDown:
	default:
		return p, fmt.Errorf("activity: rating must be 'up' or 'down'")
	}
	start, end, err := resolveActivityWindow(req.Window, req.StartDate, req.EndDate, time.Now())
	if err != nil {
		return p, err
	}
	p.start, p.end, p.subjectType = start, end, req.SubjectType
	p.scope = resolveActivityFeedbackScope(identity, callerUserID, isAdmin)
	p.userID = req.FeedbackUserID
	if p.scope.ForcedUserID != "" {
		if req.FeedbackUserID != "" && req.FeedbackUserID != p.scope.ForcedUserID {
			p.otherUser = req.FeedbackUserID
		}
		p.userID = p.scope.ForcedUserID
	}
	p.ratingsOnly = p.userID != "" && p.scope.ForcedUserID == "" && !isAdmin
	if len(req.Subjects) > 0 {
		p.match = e.resolveSubjectNames(req.Subjects, narrowVisByType(p.scope.Vis, req.SubjectType))
		p.candidates = p.match.Candidates
		p.unmatched = p.match.Unmatched
		if p.scope.ForcedUserID != "" {
			p.candidates, p.unmatched = nil, nil
		}
		p.unresolved = p.unresolved || len(p.match.Agents)+len(p.match.Workflows) == 0
	}
	return p, nil
}

func decodeActivityFeedbackRequest(body json.RawMessage) (activityFeedbackRequest, error) {
	var req activityFeedbackRequest
	if len(body) == 0 {
		return req, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("activity: invalid request body: %w", err)
	}
	if dec.More() {
		return req, errors.New("activity: invalid request body: trailing data")
	}
	return req, nil
}

func (e *GatewayExecutor) handleActivityFeedback(ctx context.Context, svc *analytics.Service, report string, body json.RawMessage, identity *auth.Identity, callerUserID string, isAdmin bool) (any, error) {
	req, err := decodeActivityFeedbackRequest(body)
	if err != nil {
		return nil, err
	}
	p, err := e.planActivityFeedback(req, identity, callerUserID, isAdmin)
	if err != nil {
		return nil, err
	}
	if p, err = confirmGuessedSubjects(ctx, svc, p); err != nil {
		errID := idgen.New()
		e.logger.Error(msgActivityQueryFailed, "errorId", errID, "report", "feedback/"+report, "error", err)
		return nil, fmt.Errorf("activity feedback/%s failed (errorId=%s)", report, errID)
	}

	reportName := "feedback/" + report
	var (
		res      any
		rowCount int
	)
	switch report {
	case "summary":
		res, rowCount, err = e.activityFeedbackSummary(ctx, svc, req, p)
	case "rows":
		res, rowCount, err = e.activityFeedbackRows(ctx, svc, req, p)
	default:
		return nil, fmt.Errorf("activity: unknown report %q", reportName)
	}
	if err != nil {
		var validation feedbackValidationError
		if errors.As(err, &validation) {
			return nil, err
		}
		errID := idgen.New()
		e.logger.Error(msgActivityQueryFailed, "errorId", errID, "report", reportName, "error", err)
		return nil, fmt.Errorf("activity %s failed (errorId=%s)", reportName, errID)
	}
	e.logger.Info("activity read",
		"report", reportName,
		"callerUser", callerUserID,
		"targetUser", p.userID,
		"crossUser", p.userID != "" && p.userID != callerUserID,
		"startDate", p.start,
		"endDate", p.end,
		"subjectCount", len(p.match.Agents)+len(p.match.Workflows),
		"rowCount", rowCount)
	return res, nil
}

type feedbackValidationError string

func (v feedbackValidationError) Error() string { return string(v) }

const msgFeedbackSummaryOwnData = "activity: feedback summaries need read_feedback on an agent or workflow, and you hold none; " +
	"for the feedback you gave yourself, use list_feedback instead (no filter needed)"

func (e *GatewayExecutor) activityFeedbackSummary(ctx context.Context, svc *analytics.Service, req activityFeedbackRequest, p feedbackQueryParts) (any, int, error) {
	if p.scope.ForcedUserID != "" {
		return nil, 0, feedbackValidationError(msgFeedbackSummaryOwnData)
	}
	out := analytics.FeedbackSummaryResponse{
		Rows:              []analytics.FeedbackRollupRow{},
		NegativeSample:    []analytics.ActivityFeedbackRow{},
		Candidates:        p.candidates,
		UnmatchedSubjects: p.unmatched,
	}
	if p.unresolved {
		return out, 0, nil
	}
	sortBy, err := feedbackSummarySort(req.SortBy)
	if err != nil {
		return nil, 0, err
	}
	page, err := svc.FeedbackRollup(ctx, analytics.FeedbackRollupQuery{
		Visibility:  p.scope.Vis,
		AgentIDs:    p.match.Agents,
		WorkflowIDs: p.match.Workflows,
		SubjectType: p.subjectType,
		StartDate:   p.start,
		EndDate:     p.end,
		GroupBy:     analytics.RollupGroupBySubject,
		SortBy:      sortBy,
		SortDesc:    true,
		PageNumber:  1,
		PageSize:    feedbackSummaryPageSize,
		Trend:       analytics.TrendNone,
		UserID:      p.userID,
		RatingsOnly: p.ratingsOnly,
	})
	if err != nil {
		return nil, 0, err
	}
	out.Rows = page.Data
	out.Truncated = page.Meta.Pagination.TotalPages > 1

	if len(req.Subjects) == 0 && p.userID == "" {
		sample, _, serr := svc.FeedbackRows(ctx, narrowVisByType(p.scope.Vis, p.subjectType),
			store.CrossUserFeedbackFilter{Rating: feedbackRatingDown, StartDate: p.start, EndDate: p.end},
			feedbackNegativeSampleSize, 0)
		if serr != nil {
			return nil, 0, serr
		}
		out.NegativeSample = sample
	}
	return out, len(out.Rows) + len(out.NegativeSample), nil
}

func confirmGuessedSubjects(ctx context.Context, svc *analytics.Service, p feedbackQueryParts) (feedbackQueryParts, error) {
	if len(p.match.Guessed) == 0 || p.scope.ForcedUserID != "" {
		return p, nil
	}
	vis := narrowVisByType(p.scope.Vis, p.subjectType)
	wholeKinds := store.FeedbackVisibility{
		Agents:    store.SubjectScope{All: vis.Agents.All},
		Workflows: store.SubjectScope{All: vis.Workflows.All},
	}
	known, err := svc.SubjectsWithFeedback(ctx, p.match.Guessed, wholeKinds)
	if err != nil {
		return p, fmt.Errorf("activity: guessed subjects: %w", err)
	}
	unknown := func(id string) bool { return slices.Contains(p.match.Guessed, id) && !known[id] }
	p.match.Agents = slices.DeleteFunc(slices.Clone(p.match.Agents), unknown)
	p.match.Workflows = slices.DeleteFunc(slices.Clone(p.match.Workflows), unknown)
	p.unmatched = slices.Clone(p.unmatched)
	for _, id := range p.match.Guessed {
		if !known[id] {
			p.unmatched = append(p.unmatched, id)
		}
	}
	p.unresolved = p.unresolved || len(p.match.Agents)+len(p.match.Workflows) == 0
	return p, nil
}

const msgFeedbackRowsOtherUser = "activity: without read_feedback you can list only the feedback you gave yourself; " +
	"another user's feedback needs read_feedback on the agent or workflow they rated"

func (e *GatewayExecutor) activityFeedbackRows(ctx context.Context, svc *analytics.Service, req activityFeedbackRequest, p feedbackQueryParts) (any, int, error) {
	if p.otherUser != "" {
		e.logger.Warn("activity feedback read refused",
			"report", "feedback/rows",
			"callerUser", p.scope.ForcedUserID,
			"targetUser", p.otherUser,
			"crossUser", true)
		return nil, 0, feedbackValidationError(msgFeedbackRowsOtherUser)
	}
	if len(req.Subjects) == 0 && p.userID == "" && req.FeedbackID == "" {
		return nil, 0, feedbackValidationError("activity: feedback rows need an agent, workflow, user or feedback id filter")
	}
	filter := store.CrossUserFeedbackFilter{
		AgentIDs:    p.match.Agents,
		WorkflowIDs: p.match.Workflows,
		Rating:      req.Rating,
		StartDate:   p.start,
		EndDate:     p.end,
		UserIDs:     store.OptionalUserIDs(p.userID),
	}
	if req.FeedbackID != "" {
		if err := validateActivityPathUUID(req.FeedbackID); err != nil {
			return nil, 0, feedbackValidationError("activity: feedbackId must be a valid UUID")
		}
		filter.IDs = []string{req.FeedbackID}
		filter.StartDate, filter.EndDate = 0, 0
	}
	out := analytics.FeedbackRowsResponse{
		Paginated:         feedbackRowsPage([]analytics.ActivityFeedbackRow{}, 0),
		Candidates:        p.candidates,
		UnmatchedSubjects: p.unmatched,
	}
	if p.unresolved {
		return out, 0, nil
	}
	rows, total, err := svc.FeedbackRows(ctx, narrowVisByType(p.scope.Vis, p.subjectType), filter, feedbackRowsLimit, 0)
	if err != nil {
		return nil, 0, err
	}
	out.Paginated = feedbackRowsPage(rows, total)
	return out, len(rows), nil
}

func feedbackRowsPage(rows []analytics.ActivityFeedbackRow, total int) analytics.Paginated[analytics.ActivityFeedbackRow] {
	return analytics.Paginated[analytics.ActivityFeedbackRow]{
		Data: rows,
		Meta: analytics.Meta{
			Pagination: analytics.Pagination{PageNumber: 1, PageSize: feedbackRowsLimit, TotalPages: 1, Count: len(rows)},
			Truncated:  total > feedbackRowsLimit,
		},
	}
}

func narrowVisByType(vis store.FeedbackVisibility, subjectType string) store.FeedbackVisibility {
	switch subjectType {
	case store.SubjectTypeAgent:
		vis.Workflows = store.SubjectScope{}
	case store.SubjectTypeWorkflow:
		vis.Agents = store.SubjectScope{}
	}
	return vis
}

func feedbackSummarySort(raw string) (string, error) {
	switch raw {
	case "", analytics.RollupSortNegativeRatio:
		return analytics.RollupSortNegativeRatio, nil
	case analytics.RollupSortNegativeFeedback:
		return analytics.RollupSortNegativeFeedback, nil
	}
	return "", feedbackValidationError("activity: sortBy must be 'negativeRatio' or 'negativeFeedback'")
}

const msgActivityQueryFailed = "activity query failed"

const msgFeedbackSessionNotServed = "activity session not served"

const msgFeedbackNotFound = "activity: feedback %q not found"

const msgTaskFeedbackNotFound = "activity: no feedback found for task %q"

func (e *GatewayExecutor) isReadableFeedbackID(ctx context.Context, id string, identity *auth.Identity, callerUserID string, isAdmin bool) bool {
	db := e.activityStoreDB()
	if db == nil {
		return false
	}
	row, err := store.NewFeedbackRepo(db).GetByID(ctx, id)
	if err != nil {
		e.logger.Warn("activity feedback-id check failed", "taskID", id, "error", err)
		return false
	}
	if row == nil {
		return false
	}
	return feedbackRowReadable(row, callerUserID, isAdmin, callerFeedbackReach(identity))
}

const maxFeedbackRowsPerTask = 50

const (
	feedbackSessionReport        = "feedback/{id}/session"
	feedbackTaskSessionReport    = "feedback/task/{taskId}/session"
	feedbackDenyNotReadable      = "feedback_not_readable"
	feedbackDenyForeignNode      = "node_owned_by_another_user"
	feedbackDenyRatedTreeMissing = TraceDenyRootRowMissing
	feedbackDenyNoArm            = "no_access_arm"
)

type feedbackSession struct {
	rated   *store.Task
	top     *store.Task
	roots   []*store.Task
	forest  []store.ForestNode
	walkCut bool
}

type sessionArm int

const (
	_ sessionArm = iota
	armAdmin
	armOwner
	armTrace
)

func (e *GatewayExecutor) handleFeedbackSession(ctx context.Context, svc *analytics.Service, feedbackID string, identity *auth.Identity, callerUserID string, isAdmin bool) (*analytics.SessionTraceResponse, error) {
	if err := validateActivityPathUUID(feedbackID); err != nil {
		return nil, errors.New("activity: path feedbackId must be a valid UUID")
	}
	db := e.activityStoreDB()
	if db == nil {
		return nil, fmt.Errorf("activity: services unavailable")
	}
	row, err := store.NewFeedbackRepo(db).GetByID(ctx, feedbackID)
	if err != nil {
		return nil, e.feedbackSessionFailed(feedbackSessionReport, "feedbackID", feedbackID, err)
	}
	if row == nil {
		return nil, fmt.Errorf(msgFeedbackNotFound, feedbackID)
	}
	reach := callerFeedbackReach(identity)
	if !feedbackRowReadable(row, callerUserID, isAdmin, reach) {
		e.logActivityAccessDenied(feedbackSessionReport, callerUserID, "feedbackID", feedbackID, feedbackDenyNotReadable)
		return nil, fmt.Errorf(msgFeedbackNotFound, feedbackID)
	}
	return e.openFeedbackSession(ctx, svc, db, row, feedbackSessionReport, sessionCaller{userID: callerUserID, isAdmin: isAdmin, reach: reach})
}

func (e *GatewayExecutor) handleFeedbackSessionByTask(ctx context.Context, svc *analytics.Service, taskID string, identity *auth.Identity, callerUserID string, isAdmin bool) (*analytics.SessionTraceResponse, error) {
	if err := validateActivityPathUUID(taskID); err != nil {
		return nil, errors.New("activity: path taskId must be a valid UUID")
	}
	db := e.activityStoreDB()
	if db == nil {
		return nil, fmt.Errorf("activity: services unavailable")
	}
	rows, err := store.NewFeedbackRepo(db).Search(ctx, "*", 0, 0, taskID, "", "", maxFeedbackRowsPerTask, 0)
	if err != nil {
		return nil, e.feedbackSessionFailed(feedbackTaskSessionReport, "taskID", taskID, err)
	}
	reach := callerFeedbackReach(identity)
	for _, row := range rows {
		if feedbackRowReadable(row, callerUserID, isAdmin, reach) {
			return e.openFeedbackSession(ctx, svc, db, row, feedbackTaskSessionReport, sessionCaller{userID: callerUserID, isAdmin: isAdmin, reach: reach})
		}
	}
	if len(rows) > 0 {
		e.logActivityAccessDenied(feedbackTaskSessionReport, callerUserID, "taskID", taskID, feedbackDenyNotReadable)
	}
	return nil, fmt.Errorf(msgTaskFeedbackNotFound, taskID)
}

func feedbackRowReadable(row *store.Feedback, callerUserID string, isAdmin bool, reach rbac.FeedbackReach) bool {
	return row.UserID == callerUserID || isAdmin || reach.ReadsSubject(row.SubjectType, row.SubjectID)
}

func (e *GatewayExecutor) logActivityAccessDenied(report, callerUserID, idKey, id, reason string) {
	e.logger.Warn("activity access denied",
		"report", report,
		"callerUser", callerUserID,
		idKey, id,
		"reason", reason)
}

func (e *GatewayExecutor) feedbackSessionFailed(report, idKey, id string, err error) error {
	errID := idgen.New()
	e.logger.Error(msgActivityQueryFailed, "errorId", errID, "report", report, idKey, id, "error", err)
	return fmt.Errorf("activity %s failed (errorId=%s)", report, errID)
}

type sessionCaller struct {
	userID  string
	isAdmin bool
	reach   rbac.FeedbackReach
}

func (e *GatewayExecutor) openFeedbackSession(ctx context.Context, svc *analytics.Service, db *store.DB, row *store.Feedback, report string, caller sessionCaller) (*analytics.SessionTraceResponse, error) {
	feedbackID := row.ID
	reach, callerUserID, isAdmin := caller.reach, caller.userID, caller.isAdmin
	deny := func(reason string) (*analytics.SessionTraceResponse, error) {
		e.logActivityAccessDenied(report, callerUserID, "feedbackID", feedbackID, reason)
		return svc.RefusedSession(row, analytics.SessionRefusalNoAccess), nil
	}
	fail := func(err error) (*analytics.SessionTraceResponse, error) {
		return nil, e.feedbackSessionFailed(report, "feedbackID", feedbackID, err)
	}

	sess, err := loadFeedbackSession(ctx, db, row)
	if err != nil {
		return fail(err)
	}
	if sess.walkCut {
		e.logger.Info(msgFeedbackSessionNotServed, "report", report, "callerUser", callerUserID,
			"feedbackID", feedbackID, "reason", TraceDenyTreeTooDeep)
		return svc.RefusedSession(row, analytics.SessionRefusalTooDeep), nil
	}
	in := analytics.SessionInput{Roots: sess.roots, Forest: sess.forest}
	if sess.rated != nil {
		in.RatedRoot = sess.top
	}
	sess = sess.servedOnly(in)
	switch sessionArmFor(sess, row, callerUserID, isAdmin) {
	case armAdmin:
	case armOwner:
		if !forestOwnedBy(sess.forest, callerUserID) {
			return deny(feedbackDenyForeignNode)
		}
	case armTrace:
		open, reason, terr := traceOpensSession(ctx, store.NewTaskEventRepo(db), reach, sess)
		if terr != nil {
			return fail(terr)
		}
		if !open {
			return deny(reason)
		}
	default:
		return deny(feedbackDenyNoArm)
	}

	resp, err := svc.SessionTrace(ctx, row, in)
	if err != nil {
		return fail(err)
	}
	e.logger.Info("activity read",
		"report", report,
		"feedbackID", feedbackID,
		"callerUser", callerUserID,
		"targetUser", resp.Owner,
		"crossUser", resp.Owner != callerUserID)
	return resp, nil
}

func sessionArmFor(sess feedbackSession, row *store.Feedback, callerUserID string, isAdmin bool) sessionArm {
	switch {
	case isAdmin:
		return armAdmin
	case sess.ownedBy(callerUserID, row):
		return armOwner
	default:
		return armTrace
	}
}

func loadFeedbackSession(ctx context.Context, db *store.DB, row *store.Feedback) (feedbackSession, error) {
	lineage, err := store.NewAnalyticsRepo(db).WalkAncestors(ctx, row.TaskID, TraceParentChainMaxDepth)
	if err != nil {
		return feedbackSession{}, fmt.Errorf("feedback session: rated lineage: %w", err)
	}
	tasks := store.NewTaskRepo(db)
	if len(lineage) == 0 {
		return loadOrphanedFeedbackSession(ctx, tasks, row)
	}
	rated, top := &lineage[0], &lineage[len(lineage)-1]
	sessionID := cmp.Or(ptrOrZero(top.SessionID), ptrOrZero(rated.SessionID), row.SessionID)
	var roots []*store.Task
	switch {
	case sessionID != "":
		roots, err = tasks.ListSessionRootsUpTo(ctx, sessionID, top.ID, top.StartTime, analytics.MaxSessionTurns+1)
		if err != nil {
			return feedbackSession{}, fmt.Errorf("feedback session: roots: %w", err)
		}
	case top.ParentTaskID == nil:
		roots = []*store.Task{top}
	}
	forest, err := tasks.SessionForest(ctx, taskIDs(roots))
	if err != nil {
		return feedbackSession{}, fmt.Errorf("feedback session: trees: %w", err)
	}
	walkCut := top.ParentTaskID != nil && len(lineage) > TraceParentChainMaxDepth
	return feedbackSession{rated: rated, top: top, roots: roots, forest: forest, walkCut: walkCut}, nil
}

func loadOrphanedFeedbackSession(ctx context.Context, tasks *store.TaskRepo, row *store.Feedback) (feedbackSession, error) {
	if row.SessionID == "" {
		return feedbackSession{}, nil
	}
	roots, err := tasks.ListSessionRootsUpTo(ctx, row.SessionID, "", row.CreatedAt, analytics.MaxSessionTurns+1)
	if err != nil {
		return feedbackSession{}, fmt.Errorf("feedback session: roots: %w", err)
	}
	forest, err := tasks.SessionForest(ctx, taskIDs(roots))
	if err != nil {
		return feedbackSession{}, fmt.Errorf("feedback session: trees: %w", err)
	}
	return feedbackSession{roots: roots, forest: forest}, nil
}

func (s feedbackSession) servedOnly(in analytics.SessionInput) feedbackSession {
	s.roots, _ = analytics.ServedTurns(in)
	served := make(map[string]bool, len(s.roots))
	for _, r := range s.roots {
		served[r.ID] = true
	}
	s.forest = slices.DeleteFunc(slices.Clone(s.forest), func(n store.ForestNode) bool { return !served[n.RootID] })
	return s
}

func taskIDs(tasks []*store.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func (s feedbackSession) ownedBy(callerUserID string, row *store.Feedback) bool {
	ratedBy := row.UserID
	if s.rated != nil {
		ratedBy = s.rated.UserID
	}
	if ratedBy != callerUserID {
		return false
	}
	for _, r := range s.roots {
		if r.UserID != callerUserID {
			return false
		}
	}
	return true
}

func forestOwnedBy(forest []store.ForestNode, callerUserID string) bool {
	for _, n := range forest {
		if n.UserID != callerUserID && n.UserID != store.TaskUserUnknown {
			return false
		}
	}
	return true
}

func traceOpensSession(ctx context.Context, events *store.TaskEventRepo, reach rbac.FeedbackReach, sess feedbackSession) (open bool, reason string, err error) {
	if reach.AgentTrace.IsEmpty() {
		return false, TraceDenyNoGrant, nil
	}
	if sess.rated == nil || !slices.ContainsFunc(sess.roots, func(r *store.Task) bool { return r.ID == sess.top.ID }) {
		return false, feedbackDenyRatedTreeMissing, nil
	}
	trees := groupForest(sess.forest)
	for _, root := range sess.roots {
		tree := trees[root.ID]
		for _, n := range tree {
			if n.Depth >= store.TaskTreeMaxDepth {
				return false, TraceDenyTreeTooDeep, nil
			}
		}
		if reason := traceRowsCovered(root, forestHandlers(tree), reach); reason != "" {
			return false, reason, nil
		}
	}
	ids := make([]string, 0, len(sess.forest))
	for _, n := range sess.forest {
		ids = append(ids, n.ID)
	}
	peersByTask, perr := events.ForwardedPeersByTask(ctx, ids)
	if perr != nil {
		return false, TraceDenyLookupFailed, fmt.Errorf("trace access: forwarded peers: %w", perr)
	}
	for _, root := range sess.roots {
		tree := trees[root.ID]
		var peers []string
		for _, n := range tree {
			peers = append(peers, peersByTask[n.ID]...)
		}
		if !everyPeerCovered(peers, forestHandlers(tree), reach) {
			return false, TraceDenyParticipantUncovered, nil
		}
	}
	return true, "", nil
}

func groupForest(forest []store.ForestNode) map[string][]store.ForestNode {
	out := make(map[string][]store.ForestNode)
	for _, n := range forest {
		out[n.RootID] = append(out[n.RootID], n)
	}
	return out
}

func forestHandlers(tree []store.ForestNode) []store.TaskHandler {
	seen := make(map[store.TaskHandler]bool, len(tree))
	out := make([]store.TaskHandler, 0, len(tree))
	for _, n := range tree {
		h := store.TaskHandler{HandlerName: n.HandlerName, HandlerType: n.HandlerType}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func ptrOrZero[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func callerFeedbackReach(identity *auth.Identity) rbac.FeedbackReach {
	if identity == nil || len(identity.Scopes) == 0 {
		return rbac.FeedbackReach{}
	}
	return rbac.ResolveFeedbackReach(identity.Scopes)
}

func (e *GatewayExecutor) activityStoreDB() *store.DB {
	if core := e.storeCore(); core != nil {
		return core.Store()
	}
	return nil
}
