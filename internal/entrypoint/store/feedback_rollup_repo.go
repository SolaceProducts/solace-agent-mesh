// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	DayBucketMs  int64 = 86400000
	HourBucketMs int64 = 3600000
)

func bucketExpr(column string, widthMs int64) string {
	return fmt.Sprintf("(%s / %d) * %d", column, widthMs, widthMs)
}

type HandlerTaskCount struct {
	HandlerName   string
	HandlerType   string
	BucketStartMs int64
	Count         int
}

func (r *AnalyticsRepo) CountHandlerTasks(ctx context.Context, opts TaskAnalyticsOpts, handlers SubjectScope, bucketMs int64) ([]HandlerTaskCount, error) {
	if !handlers.All && len(handlers.IDs) == 0 {
		return []HandlerTaskCount{}, nil
	}
	joined, args := opts.taskWhere("t")
	where := "t.handler_name IS NOT NULL"
	if joined != "" {
		where = joined + " AND " + where
	}
	prefix, prefixArgs := opts.workflowNestedCTEPrefix()
	if opts.ExcludeWorkflowNested {
		where += ` AND NOT EXISTS (SELECT 1 FROM workflow_nested wn WHERE wn.task_id = t.id)`
	}
	if !handlers.All {
		placeholders, inArgs, err := dbutil.InClause(handlers.IDs)
		if err != nil {
			return nil, fmt.Errorf("AnalyticsRepo.CountHandlerTasks handler names: %w", err)
		}
		where += " AND t.handler_name IN (" + placeholders + ")"
		args = append(args, inArgs...)
	}

	bucketCol, groupBucket := "0", ""
	if bucketMs > 0 {
		expr := bucketExpr("t.start_time", bucketMs)
		bucketCol, groupBucket = expr, ", "+expr
	}
	type row struct {
		HandlerName string  `db:"handler_name"`
		HandlerType *string `db:"handler_type"`
		BucketMs    int64   `db:"bucket_ms"`
		Count       int     `db:"task_count"`
	}
	allArgs := append(append([]any{}, prefixArgs...), args...)
	rows, err := dbutil.QueryRowPtrs[row](ctx, r.db,
		prefix+`SELECT t.handler_name AS handler_name,
			MAX(t.handler_type) AS handler_type,
			`+bucketCol+` AS bucket_ms,
			COUNT(*) AS task_count
		 FROM tasks t
		 WHERE `+where+`
		 GROUP BY t.handler_name`+groupBucket, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.CountHandlerTasks: %w", err)
	}
	out := make([]HandlerTaskCount, 0, len(rows))
	for _, x := range rows {
		ht := ""
		if x.HandlerType != nil {
			ht = *x.HandlerType
		}
		out = append(out, HandlerTaskCount{HandlerName: x.HandlerName, HandlerType: ht, BucketStartMs: x.BucketMs, Count: x.Count})
	}
	return out, nil
}

type SubjectFeedbackCount struct {
	SubjectID     string
	SubjectType   string
	BucketStartMs int64
	Positive      int
	Negative      int
}

func (r *AnalyticsRepo) AggregateFeedbackBySubject(ctx context.Context, vis FeedbackVisibility, f CrossUserFeedbackFilter, bucketMs int64) ([]SubjectFeedbackCount, error) {
	if !vis.grantsAnything() {
		return []SubjectFeedbackCount{}, nil
	}
	reach, args, err := feedbackReachWhere(vis)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateFeedbackBySubject: %w", err)
	}
	where := "WHERE " + reach + " AND subject_type <> ?"
	args = append(args, SubjectTypeUnknown)
	if f.StartDate > 0 {
		where += " AND t.start_time >= ?"
		args = append(args, f.StartDate)
	}
	if f.EndDate > 0 {
		where += " AND t.start_time <= ?"
		args = append(args, f.EndDate)
	}
	if len(f.UserIDs) > 0 {
		placeholders, inArgs, inErr := dbutil.InClause(f.UserIDs)
		if inErr != nil {
			return nil, fmt.Errorf("AnalyticsRepo.AggregateFeedbackBySubject userIds: %w", inErr)
		}
		where += " AND feedback.user_id IN (" + placeholders + ")"
		args = append(args, inArgs...)
	}
	denominator := TaskAnalyticsOpts{StartDate: f.StartDate, EndDate: f.EndDate, Kind: TaskKindNormal, ExcludeWorkflowNested: true}
	where += " AND t.kind = ? AND t.handler_name IS NOT NULL AND t.handler_type = feedback.subject_type"
	args = append(args, denominator.Kind)
	prefix, prefixArgs := denominator.workflowNestedCTEPrefix()
	where += ` AND NOT (feedback.subject_type = ? AND EXISTS (SELECT 1 FROM workflow_nested wn WHERE wn.task_id = t.id))`
	args = append(args, SubjectTypeAgent)

	bucketCol, groupBucket := "0", ""
	if bucketMs > 0 {
		expr := bucketExpr("t.start_time", bucketMs)
		bucketCol, groupBucket = expr, ", "+expr
	}
	type row struct {
		SubjectType string `db:"subject_type"`
		SubjectID   string `db:"subject_id"`
		BucketMs    int64  `db:"bucket_ms"`
		Positive    int    `db:"positive"`
		Negative    int    `db:"negative"`
	}
	allArgs := append(append([]any{}, prefixArgs...), args...)
	rows, err := dbutil.QueryRowPtrs[row](ctx, r.db,
		prefix+`SELECT subject_type, subject_id, `+bucketCol+` AS bucket_ms,
			COUNT(DISTINCT CASE WHEN rating = 'up' THEN feedback.task_id END) AS positive,
			COUNT(DISTINCT CASE WHEN rating = 'down' THEN feedback.task_id END) AS negative
		 FROM feedback
		 JOIN tasks t ON t.id = feedback.task_id
		 `+where+`
		 GROUP BY subject_type, subject_id`+groupBucket, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateFeedbackBySubject: %w", err)
	}
	out := make([]SubjectFeedbackCount, 0, len(rows))
	for _, x := range rows {
		out = append(out, SubjectFeedbackCount{SubjectID: x.SubjectID, SubjectType: x.SubjectType, BucketStartMs: x.BucketMs, Positive: x.Positive, Negative: x.Negative})
	}
	return out, nil
}
