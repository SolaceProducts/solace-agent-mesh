// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type feedbackRollupHandler struct {
	svc    *analytics.Service
	logger *slog.Logger
	now    func() time.Time
}

const (
	defaultRollupWindowDays = 30
	maxRollupWindowDays     = 366
	maxHourTrendWindowDays  = 7
	rollupDayMs             = int64(24 * time.Hour / time.Millisecond)

	msgFeedbackRollupRead     = "feedback rollup read"
	msgRollupValidationFailed = "feedback rollup query validation failed"
)

func (h *feedbackRollupHandler) handleRollup(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil {
		warnFeedbackReadDenied(h.logger, "", false)
		writeError(w, http.StatusForbidden, "feedback read access required")
		return
	}
	reach := rbac.ResolveFeedbackReach(id.Scopes())
	if !hasFeedbackReadReach(reach) {
		warnFeedbackReadDenied(h.logger, id.UserID(), true)
		writeError(w, http.StatusForbidden, "feedback read access required")
		return
	}
	agentIDs, workflowIDs := queryStringSlice(r, "agentIds"), queryStringSlice(r, "workflowIds")
	q, fieldErrs := h.parseRollupQuery(r)
	recordFieldError(fieldErrs, "agentIds", inClauseCapError(agentIDs))
	recordFieldError(fieldErrs, "workflowIds", inClauseCapError(workflowIDs))
	if len(fieldErrs) > 0 {
		writeValidationError(w, h.logger, msgRollupValidationFailed, fieldErrs)
		return
	}
	q.Visibility, q.AgentIDs, q.WorkflowIDs = visibilityFromReach(reach), agentIDs, workflowIDs

	page, err := h.svc.FeedbackRollup(r.Context(), q)
	if err != nil {
		if clientGone(err) {
			return
		}
		errID := idgen.New()
		h.logger.Error("feedback rollup query failed", "errorId", errID, "userID", id.UserID(), "error", err)
		writeErrorWithID(w, http.StatusInternalServerError, msgInternalErr, errID)
		return
	}
	auditFeedbackRollupRead(h.logger, id.UserID(), reach, len(page.Data))
	writeJSON(w, http.StatusOK, page)
}

func (h *feedbackRollupHandler) parseRollupQuery(r *http.Request) (analytics.FeedbackRollupQuery, map[string][]string) {
	q := r.URL.Query()
	fieldErrs := map[string][]string{}
	record := func(field string, err error) { recordFieldError(fieldErrs, field, err) }

	pageNumber, err := parsePageNumber(q)
	record("pageNumber", err)
	pageSize, err := parsePageSize(q)
	record("pageSize", err)
	start, end := h.rollupWindow(q, record)
	groupBy, subjectType, trend := parseRollupShape(q, record)
	sortBy, sortDesc, err := parseFeedbackRollupSort(q.Get("sort"))
	record("sort", err)

	if groupBy == string(analytics.RollupGroupByTenant) && trend != string(analytics.TrendNone) {
		if q.Get("trendGranularity") == "" {
			trend = string(analytics.TrendNone)
		} else {
			record("trendGranularity", errors.New("groupBy=tenant supports trendGranularity=none only"))
		}
	}
	if trend == string(analytics.TrendHour) && end-start > int64(maxHourTrendWindowDays)*rollupDayMs {
		record("trendGranularity", fmt.Errorf("trendGranularity=hour supports at most %d days; narrow the range or use trendGranularity=day", maxHourTrendWindowDays))
	}

	return analytics.FeedbackRollupQuery{
		SubjectType: subjectType, StartDate: start, EndDate: end,
		GroupBy: analytics.RollupGroupBy(groupBy), SortBy: sortBy, SortDesc: sortDesc,
		PageNumber: pageNumber, PageSize: pageSize, Trend: analytics.TrendGranularity(trend),
	}, fieldErrs
}

func recordFieldError(into map[string][]string, field string, err error) {
	if err != nil {
		into[field] = append(into[field], err.Error())
	}
}

func parseRollupShape(q url.Values, record func(string, error)) (groupBy, subjectType, trend string) {
	groupBy, err := enumParam(q, "groupBy", string(analytics.RollupGroupBySubject), string(analytics.RollupGroupBySubject), string(analytics.RollupGroupByTenant))
	record("groupBy", err)
	subjectType, err = enumParam(q, "subjectType", "", store.SubjectTypeAgent, store.SubjectTypeWorkflow)
	record("subjectType", err)
	trend, err = enumParam(q, "trendGranularity", string(analytics.TrendDay),
		string(analytics.TrendHour), string(analytics.TrendDay), string(analytics.TrendWeek), string(analytics.TrendNone))
	record("trendGranularity", err)
	return groupBy, subjectType, trend
}

func parseFeedbackRollupSort(raw string) (sortBy string, desc bool, err error) {
	if raw == "" {
		return analytics.RollupSortNegativeRatio, true, nil
	}
	parts := strings.SplitN(raw, ":", 2)
	fieldPart := strings.TrimSpace(parts[0])
	dirPart := ""
	if len(parts) == 2 {
		dirPart = strings.TrimSpace(parts[1])
	}
	if !analytics.IsRollupSortKey(fieldPart) {
		return "", false, fmt.Errorf("field must be one of: %s", strings.Join(analytics.RollupSortKeys(), ", "))
	}
	switch dirPart {
	case "", "desc":
		desc = true
	case "asc":
		desc = false
	default:
		return "", false, errors.New("direction must be asc or desc")
	}
	return fieldPart, desc, nil
}

func (h *feedbackRollupHandler) rollupWindow(q url.Values, record func(string, error)) (start, end int64) {
	start, startErr := parseFeedbackDateParam(q, "startDate")
	record("startDate", startErr)
	end, endErr := parseFeedbackDateParam(q, "endDate")
	record("endDate", endErr)
	beforeEpoch := errors.New("must not be before 1970-01-01T00:00:00Z")
	if startErr == nil && start < 0 {
		record("startDate", beforeEpoch)
		startErr = beforeEpoch
	}
	if endErr == nil && end < 0 {
		record("endDate", beforeEpoch)
		endErr = beforeEpoch
	}
	if startErr != nil || endErr != nil {
		return 0, 0
	}

	if end == 0 {
		end = h.clock().UnixMilli()
	}
	if start == 0 {
		start = end - defaultRollupWindowDays*rollupDayMs
	}
	if start > end {
		recordOnPair(record, errors.New("startDate must not be after endDate"))
		return 0, 0
	}
	if end-start > maxRollupWindowDays*rollupDayMs {
		recordOnPair(record, fmt.Errorf("date range must not exceed %d days", maxRollupWindowDays))
		return 0, 0
	}
	return start, end
}

func recordOnPair(record func(string, error), err error) {
	record("startDate", err)
	record("endDate", err)
}

func (h *feedbackRollupHandler) clock() time.Time {
	if h.now == nil {
		return time.Now()
	}
	return h.now()
}

func enumParam(q url.Values, key, def string, allowed ...string) (string, error) {
	v := q.Get(key)
	if v == "" {
		return def, nil
	}
	if !slices.Contains(allowed, v) {
		return "", fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
	}
	return v, nil
}

func auditFeedbackRollupRead(logger *slog.Logger, callerUserID string, reach rbac.FeedbackReach, rowCount int) {
	if logger == nil {
		return
	}
	logger.Info(msgFeedbackRollupRead,
		"callerUser", callerUserID,
		"agentReachAll", reach.AgentRead.All,
		"agentReachCount", len(reach.AgentRead.IDs),
		"workflowReachAll", reach.WorkflowRead.All,
		"workflowReachCount", len(reach.WorkflowRead.IDs),
		"rowCount", rowCount,
		"crossUser", true)
}
