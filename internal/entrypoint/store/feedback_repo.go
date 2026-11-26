// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type FeedbackRepo struct {
	db *DB
}

func NewFeedbackRepo(db *DB) *FeedbackRepo {
	return &FeedbackRepo{db: db}
}

var ErrFeedbackConflict = errors.New("feedback already submitted with different content")

type FeedbackSubmission struct {
	TaskID         string
	UserID         string
	SessionID      string
	Rating         string
	Comment        *string
	ShareContent   bool
	SubjectID      string
	SubjectType    string
	EntrypointType string
}

func (s FeedbackSubmission) hasDetails() bool {
	return s.Comment != nil || s.ShareContent
}

type FeedbackOutcome string

const (
	FeedbackInserted     FeedbackOutcome = "inserted"
	FeedbackDetailsAdded FeedbackOutcome = "details_added"
	FeedbackIdempotent   FeedbackOutcome = "idempotent"
)

type FeedbackResult struct {
	Outcome  FeedbackOutcome
	Feedback *Feedback
}

func (r FeedbackResult) Changed() bool {
	return r.Outcome == FeedbackInserted || r.Outcome == FeedbackDetailsAdded
}

const feedbackColumns = `id, task_id, user_id, session_id, rating, comment, share_content,
	subject_id, subject_type, entrypoint_type, details_submitted_time, created_at, updated_at`

func validateAttribution(s FeedbackSubmission) error {
	if s.SubjectID == "" {
		return fmt.Errorf("FeedbackRepo.Submit(task %s): missing subjectId", s.TaskID)
	}
	switch s.SubjectType {
	case SubjectTypeAgent, SubjectTypeWorkflow:
	default:
		return fmt.Errorf("FeedbackRepo.Submit(task %s): invalid subjectType %q", s.TaskID, s.SubjectType)
	}
	switch s.EntrypointType {
	case EntrypointTypeWebUI, EntrypointTypeSlack, EntrypointTypeTeamsBot, EntrypointTypeWhatsApp, EntrypointTypeGoogleChat:
	default:
		return fmt.Errorf("FeedbackRepo.Submit(task %s): invalid entrypointType %q", s.TaskID, s.EntrypointType)
	}
	return nil
}

func (r *FeedbackRepo) Submit(ctx context.Context, s FeedbackSubmission) (FeedbackResult, error) {
	if s.Comment != nil && *s.Comment == "" {
		s.Comment = nil
	}
	if err := validateAttribution(s); err != nil {
		return FeedbackResult{}, err
	}
	now := time.Now().UnixMilli()
	var detailsAt *int64
	if s.hasDetails() {
		detailsAt = &now
	}

	inserted, err := dbutil.QueryRow[Feedback](ctx, r.db,
		`INSERT INTO feedback (`+feedbackColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (task_id, user_id) DO NOTHING
		 RETURNING `+feedbackColumns,
		idgen.New(), s.TaskID, s.UserID, s.SessionID, s.Rating, s.Comment, dbutil.BoolToInt(s.ShareContent),
		s.SubjectID, s.SubjectType, s.EntrypointType, detailsAt, now, now,
	)
	if err != nil {
		return FeedbackResult{}, fmt.Errorf("FeedbackRepo.Submit(task %s): insert: %w", s.TaskID, err)
	}
	if inserted != nil {
		inserted.Inserted = true
		return FeedbackResult{Outcome: FeedbackInserted, Feedback: inserted}, nil
	}

	if s.hasDetails() {
		added, addErr := dbutil.QueryRow[Feedback](ctx, r.db,
			`UPDATE feedback SET comment = ?, share_content = ?, details_submitted_time = ?,
				updated_at = CASE WHEN updated_at < ? THEN ? ELSE updated_at + 1 END
			 WHERE task_id = ? AND user_id = ? AND rating = ? AND details_submitted_time IS NULL
			 RETURNING `+feedbackColumns,
			s.Comment, dbutil.BoolToInt(s.ShareContent), now, now, now, s.TaskID, s.UserID, s.Rating,
		)
		if addErr != nil {
			return FeedbackResult{}, fmt.Errorf("FeedbackRepo.Submit(task %s): add details: %w", s.TaskID, addErr)
		}
		if added != nil {
			return FeedbackResult{Outcome: FeedbackDetailsAdded, Feedback: added}, nil
		}
	}

	stored, err := dbutil.QueryRow[Feedback](ctx, r.db,
		`SELECT `+feedbackColumns+` FROM feedback WHERE task_id = ? AND user_id = ?`, s.TaskID, s.UserID)
	if err != nil {
		return FeedbackResult{}, fmt.Errorf("FeedbackRepo.Submit(task %s): read stored: %w", s.TaskID, err)
	}
	if stored == nil {
		return FeedbackResult{}, fmt.Errorf("FeedbackRepo.Submit(task %s): row deleted during submit", s.TaskID)
	}
	if !resubmissionMatches(stored, s) {
		return FeedbackResult{Feedback: stored}, fmt.Errorf("FeedbackRepo.Submit(task %s): %w", s.TaskID, ErrFeedbackConflict)
	}
	return FeedbackResult{Outcome: FeedbackIdempotent, Feedback: stored}, nil
}

func resubmissionMatches(stored *Feedback, s FeedbackSubmission) bool {
	if stored.Rating != s.Rating {
		return false
	}
	if !s.hasDetails() {
		return true
	}
	return stored.DetailsSubmittedTime != nil &&
		stored.ShareContent == s.ShareContent &&
		equalComment(stored.Comment, s.Comment)
}

func equalComment(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (r *FeedbackRepo) Search(ctx context.Context, userID string, startDate, endDate int64,
	taskID, sessionID, rating string, limit, offset int) ([]*Feedback, error) {

	where := "WHERE 1=1"
	var args []any

	if userID != "" && userID != "*" {
		where += " AND user_id = ?"
		args = append(args, userID)
	}
	if startDate > 0 {
		where += " AND updated_at >= ?"
		args = append(args, startDate)
	}
	if endDate > 0 {
		where += " AND updated_at <= ?"
		args = append(args, endDate)
	}
	if taskID != "" {
		where += " AND task_id = ?"
		args = append(args, taskID)
	}
	if sessionID != "" {
		where += " AND session_id = ?"
		args = append(args, sessionID)
	}
	if rating != "" {
		where += " AND rating = ?"
		args = append(args, rating)
	}

	query := `SELECT ` + feedbackColumns + `
		FROM feedback ` + where + ` ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?`

	return dbutil.QueryRowPtrs[Feedback](ctx, r.db, query, append(args, limit, offset)...)
}

func (r *FeedbackRepo) GetByID(ctx context.Context, id string) (*Feedback, error) {
	query := `SELECT ` + feedbackColumns + ` FROM feedback WHERE id = ?`
	rows, err := dbutil.QueryRowPtrs[Feedback](ctx, r.db, query, id)
	if err != nil {
		return nil, fmt.Errorf("FeedbackRepo.GetByID: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

type SubjectScope struct {
	All bool
	IDs []string
}

type FeedbackVisibility struct {
	Agents    SubjectScope
	Workflows SubjectScope
}

func (v FeedbackVisibility) grantsAnything() bool {
	return v.Agents.All || len(v.Agents.IDs) > 0 || v.Workflows.All || len(v.Workflows.IDs) > 0
}

func kindClause(column, kind string, scope SubjectScope) (string, []any, error) {
	if scope.All {
		return column + " = ?", []any{kind}, nil
	}
	if len(scope.IDs) == 0 {
		return "1 = 0", nil, nil
	}
	placeholders, args, err := dbutil.InClause(scope.IDs)
	if err != nil {
		return "", nil, fmt.Errorf("subject ids for %s: %w", kind, err)
	}
	return "(" + column + " = ? AND subject_id IN (" + placeholders + "))", append([]any{kind}, args...), nil
}

func feedbackReachWhere(vis FeedbackVisibility) (string, []any, error) {
	agentClause, agentArgs, err := kindClause("subject_type", SubjectTypeAgent, vis.Agents)
	if err != nil {
		return "", nil, err
	}
	workflowClause, workflowArgs, err := kindClause("subject_type", SubjectTypeWorkflow, vis.Workflows)
	if err != nil {
		return "", nil, err
	}

	reach := "(" + agentClause + " OR " + workflowClause
	args := append(append([]any{}, agentArgs...), workflowArgs...)

	if vis.Agents.All && vis.Workflows.All {
		reach += " OR subject_type = ?"
		args = append(args, SubjectTypeUnknown)
	}
	reach += ")"
	return reach, args, nil
}

func crossUserWhere(vis FeedbackVisibility, f CrossUserFeedbackFilter) (string, []any, error) {
	reach, args, err := feedbackReachWhere(vis)
	if err != nil {
		return "", nil, err
	}

	where := "WHERE " + reach

	var requested []string
	if len(f.AgentIDs) > 0 {
		placeholders, inArgs, inErr := dbutil.InClause(f.AgentIDs)
		if inErr != nil {
			return "", nil, fmt.Errorf("agentIds filter: %w", inErr)
		}
		requested = append(requested, "(subject_type = ? AND subject_id IN ("+placeholders+"))")
		args = append(append(args, SubjectTypeAgent), inArgs...)
	}
	if len(f.WorkflowIDs) > 0 {
		placeholders, inArgs, inErr := dbutil.InClause(f.WorkflowIDs)
		if inErr != nil {
			return "", nil, fmt.Errorf("workflowIds filter: %w", inErr)
		}
		requested = append(requested, "(subject_type = ? AND subject_id IN ("+placeholders+"))")
		args = append(append(args, SubjectTypeWorkflow), inArgs...)
	}
	if len(requested) > 0 {
		where += " AND (" + strings.Join(requested, " OR ") + ")"
	}

	if len(f.UserIDs) > 0 {
		placeholders, inArgs, inErr := dbutil.InClause(f.UserIDs)
		if inErr != nil {
			return "", nil, fmt.Errorf("userIds filter: %w", inErr)
		}
		where += " AND user_id IN (" + placeholders + ")"
		args = append(args, inArgs...)
	}

	if len(f.IDs) > 0 {
		placeholders, inArgs, inErr := dbutil.InClause(f.IDs)
		if inErr != nil {
			return "", nil, fmt.Errorf("ids filter: %w", inErr)
		}
		where += " AND id IN (" + placeholders + ")"
		args = append(args, inArgs...)
	}

	if f.Rating != "" {
		where += " AND rating = ?"
		args = append(args, f.Rating)
	}
	if f.StartDate > 0 {
		where += " AND created_at >= ?"
		args = append(args, f.StartDate)
	}
	if f.EndDate > 0 {
		where += " AND created_at <= ?"
		args = append(args, f.EndDate)
	}
	return where, args, nil
}

func OptionalUserIDs(userID string) []string {
	if userID == "" {
		return nil
	}
	return []string{userID}
}

type CrossUserFeedbackFilter struct {
	IDs         []string
	UserIDs     []string
	AgentIDs    []string
	WorkflowIDs []string
	Rating      string
	StartDate   int64
	EndDate     int64
}

func (r *FeedbackRepo) SearchCrossUser(ctx context.Context, vis FeedbackVisibility,
	f CrossUserFeedbackFilter, limit, offset int) ([]*Feedback, int, error) {

	if !vis.grantsAnything() {
		return []*Feedback{}, 0, nil
	}

	where, args, err := crossUserWhere(vis, f)
	if err != nil {
		return nil, 0, err
	}

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM feedback `+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("count cross-user feedback: %w", err)
	}

	query := `SELECT id, task_id, user_id, session_id, rating, comment, share_content,
			subject_id, subject_type, entrypoint_type, created_at, updated_at
		FROM feedback ` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`

	rows, err := dbutil.QueryRowPtrs[Feedback](ctx, r.db, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list cross-user feedback: %w", err)
	}
	return rows, total, nil
}

func (r *FeedbackRepo) ConsentedTaskIDs(ctx context.Context, taskIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	ph, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("FeedbackRepo.ConsentedTaskIDs: %w", err)
	}
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT f.task_id FROM feedback f
		 JOIN tasks t ON t.id = f.task_id
		 WHERE f.share_content = 1 AND f.user_id = t.user_id AND f.task_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("FeedbackRepo.ConsentedTaskIDs: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func (r *FeedbackRepo) SubjectsWithFeedback(ctx context.Context, ids []string, vis FeedbackVisibility) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	found, err := r.subjectIDsWithFeedback(ctx, ids, vis)
	if err != nil {
		return nil, fmt.Errorf("FeedbackRepo.SubjectsWithFeedback: %w", err)
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

func (r *FeedbackRepo) subjectIDsWithFeedback(ctx context.Context, ids []string, vis FeedbackVisibility) ([]string, error) {
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, err
	}
	reach, reachArgs, err := feedbackReachWhere(vis)
	if err != nil {
		return nil, err
	}
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT subject_id FROM feedback WHERE subject_id IN (`+ph+`) AND `+reach, append(args, reachArgs...)...)
}

func (r *FeedbackRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	beforeMs := before.UnixMilli()
	var total int64
	query := `DELETE FROM feedback WHERE id IN (
				SELECT id FROM feedback WHERE updated_at < ? LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("FeedbackRepo.DeleteOlderThan: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("FeedbackRepo.DeleteOlderThan: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	return total, nil
}

func (r *FeedbackRepo) Delete(ctx context.Context, taskID, userID string) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM feedback WHERE task_id = ? AND user_id = ?`, taskID, userID)
	if err != nil {
		return false, fmt.Errorf("FeedbackRepo.Delete(task %s): %w", taskID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("FeedbackRepo.Delete(task %s): rows affected: %w", taskID, err)
	}
	return n > 0, nil
}
