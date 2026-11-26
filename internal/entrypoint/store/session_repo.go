// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const sessionColumns = `id, name, user_id, agent_id, project_id, created_time,
	updated_time, deleted_at, deleted_by, source, last_viewed_at, has_activity, external_gateway`

const sessionHasActivityClause = ` AND sessions.has_activity = 1`

const sessionSourceScheduler = "scheduler"

const sessionExcludeInternalClause = ` AND (sessions.source IS NULL OR sessions.source != 'eval')`

const UnassignedProjectFilter = "__no_project__"

func appendProjectFilter(where string, args []any, projectID string) (string, []any) {
	switch projectID {
	case "":
		return where, args
	case UnassignedProjectFilter:
		return where + " AND sessions.project_id IS NULL", args
	default:
		return where + " AND sessions.project_id = ?", append(args, projectID)
	}
}

const sessionExcludeExternalGatewayClause = ` AND sessions.external_gateway = 0`

type SessionRepo struct {
	db *DB
}

func NewSessionRepo(db *DB) *SessionRepo {
	return &SessionRepo{db: db}
}

func (r *SessionRepo) Create(ctx context.Context, s *Session) error {
	now := time.Now().UnixMilli()
	s.CreatedTime = now
	s.UpdatedTime = now
	s.HasActivity = s.HasActivity || isSchedulerSource(s.Source)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (id, name, user_id, agent_id, project_id, created_time, updated_time, source, has_activity)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.UserID, s.AgentID, s.ProjectID, s.CreatedTime, s.UpdatedTime, s.Source, dbutil.BoolToInt(s.HasActivity),
	)
	if err != nil {
		return fmt.Errorf("SessionRepo.Create(%s): %w", s.ID, err)
	}
	return nil
}

func isSchedulerSource(source *string) bool {
	return source != nil && *source == sessionSourceScheduler
}

func (r *SessionRepo) HealDenormalizedFlags(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET has_activity = 1
		 WHERE has_activity = 0
		   AND (EXISTS (SELECT 1 FROM chat_tasks WHERE chat_tasks.session_id = sessions.id)
		     OR EXISTS (SELECT 1 FROM tasks WHERE tasks.session_id = sessions.id)
		     OR sessions.source = ?)`,
		sessionSourceScheduler,
	); err != nil {
		return fmt.Errorf("heal sessions.has_activity: %w", err)
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET external_gateway = 1
		 WHERE external_gateway = 0
		   AND id IN (SELECT session_id FROM gateway_session_keys WHERE gateway_type != ?)`,
		GatewayTypeScheduled,
	); err != nil {
		return fmt.Errorf("heal sessions.external_gateway: %w", err)
	}
	return nil
}

func markSessionActiveTx(ctx context.Context, tx *dbutil.Tx, sessionID string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET has_activity = 1 WHERE id = ? AND has_activity = 0`,
		sessionID,
	); err != nil {
		return fmt.Errorf("mark session %s active: %w", sessionID, err)
	}
	return nil
}

func (r *SessionRepo) GetByID(ctx context.Context, id string) (*Session, error) {
	return dbutil.QueryRow[Session](ctx, r.db,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = ? AND deleted_at IS NULL`, id)
}

func (r *SessionRepo) GetByIDIncludingDeleted(ctx context.Context, id string) (*Session, error) {
	return dbutil.QueryRow[Session](ctx, r.db,
		`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
}

func (r *SessionRepo) NamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("SessionRepo.NamesByIDs: %w", err)
	}
	rows, err := dbutil.QueryRowPtrs[sessionName](ctx, r.db,
		`SELECT id, name FROM sessions
		 WHERE id IN (`+ph+`) AND name IS NOT NULL AND deleted_at IS NULL`, args...)
	if err != nil {
		return nil, fmt.Errorf("SessionRepo.NamesByIDs: %w", err)
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

type sessionName struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

func (r *SessionRepo) ListByUser(ctx context.Context, userID string, includeDeleted, nonEmptyOnly, excludeExternalGateways bool, projectID string, excludeAgentIDs []string, includeAgentIDs []string, limit, offset int) ([]*Session, int, error) {
	where, args := buildListByUserWhere(userID, SessionListFilter{
		IncludeDeleted: includeDeleted, NonEmptyOnly: nonEmptyOnly,
		ExcludeExternalGateways: excludeExternalGateways, ProjectID: projectID,
		ExcludeAgentIDs: excludeAgentIDs, IncludeAgentIDs: includeAgentIDs,
	})
	return listPageWithDerivedTotal[Session](ctx, r.db, sessionColumns, "sessions", where,
		sessionListOrder, args, limit, offset, "SessionRepo.ListByUser")
}

type SessionListFilter struct {
	IncludeDeleted          bool
	NonEmptyOnly            bool
	ExcludeExternalGateways bool
	ProjectID               string
	Source                  string
	ExcludeAgentIDs         []string
	IncludeAgentIDs         []string
}

const (
	SourceFilterChat      = "chat"
	SourceFilterScheduler = "scheduler"
)

func appendSourceFilter(where string, args []any, source string) (string, []any) {
	switch source {
	case SourceFilterScheduler:
		return where + " AND sessions.source = ?", append(args, sessionSourceScheduler)
	case SourceFilterChat:
		return where + " AND (sessions.source IS NULL OR sessions.source != ?)", append(args, sessionSourceScheduler)
	default:
		return where, args
	}
}

type SessionSearchFilter struct {
	NonEmptyOnly    bool
	ProjectID       string
	ExcludeAgentIDs []string
	IncludeAgentIDs []string
}

const sessionListOrder = "updated_time DESC, id DESC"

func (r *SessionRepo) ListByUserPage(ctx context.Context, userID string, f SessionListFilter, limit, offset int) ([]*Session, bool, error) {
	where, args := buildListByUserWhere(userID, f)
	return listPageWithHasMore[Session](ctx, r.db, pageQuery{
		columns: sessionColumns, table: "sessions", where: where, orderBy: sessionListOrder,
		args: args, limit: limit, offset: offset, errPrefix: "SessionRepo.ListByUserPage",
	})
}

func (r *SessionRepo) CountByUser(ctx context.Context, userID string, f SessionListFilter) (int, error) {
	where, args := buildListByUserWhere(userID, f)
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM sessions `+where, args...)
	if err != nil {
		return 0, fmt.Errorf("SessionRepo.CountByUser: %w", err)
	}
	return total, nil
}

func buildListByUserWhere(userID string, f SessionListFilter) (string, []any) {
	includeDeleted, nonEmptyOnly, excludeExternalGateways := f.IncludeDeleted, f.NonEmptyOnly, f.ExcludeExternalGateways
	projectID, excludeAgentIDs, includeAgentIDs := f.ProjectID, f.ExcludeAgentIDs, f.IncludeAgentIDs
	where := "WHERE user_id = ?"
	args := []any{userID}
	if !includeDeleted {
		where += " AND deleted_at IS NULL"
	}
	where += sessionExcludeInternalClause
	if nonEmptyOnly {
		where += sessionHasActivityClause
	}
	if excludeExternalGateways {
		where += sessionExcludeExternalGatewayClause
	}
	where, args = appendProjectFilter(where, args, projectID)
	where, args = appendSourceFilter(where, args, f.Source)
	if len(includeAgentIDs) > 0 {
		placeholders := make([]string, len(includeAgentIDs))
		for i, id := range includeAgentIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		where += " AND agent_id IN (" + strings.Join(placeholders, ",") + ")"
	} else {
		for _, agentID := range excludeAgentIDs {
			where += " AND (agent_id IS NULL OR agent_id != ?)"
			args = append(args, agentID)
		}
	}
	return where, args
}

func (r *SessionRepo) ListByProject(ctx context.Context, projectID string) ([]*Session, error) {
	return dbutil.QueryRowPtrs[Session](ctx, r.db,
		`SELECT `+sessionColumns+` FROM sessions
		 WHERE project_id = ? AND deleted_at IS NULL ORDER BY created_time ASC`,
		projectID)
}

func (r *SessionRepo) Touch(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET updated_time = ? WHERE id = ? AND deleted_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("SessionRepo.Touch(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *SessionRepo) Rename(ctx context.Context, id, name string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET name = ?, updated_time = ? WHERE id = ? AND deleted_at IS NULL`,
		name, now, id,
	)
	if err != nil {
		return fmt.Errorf("SessionRepo.Rename(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *SessionRepo) SetTitleIfUnset(ctx context.Context, id, name, placeholder string) (bool, error) {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET name = ?, updated_time = ?
		 WHERE id = ? AND deleted_at IS NULL AND COALESCE(TRIM(name), '') IN ('', ?)`,
		name, now, id, placeholder,
	)
	if err != nil {
		return false, fmt.Errorf("SessionRepo.SetTitleIfUnset(%s): %w", id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SessionRepo.SetTitleIfUnset(%s): %w", id, err)
	}
	return n > 0, nil
}

func (r *SessionRepo) SetAgentID(ctx context.Context, id, agentID string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET agent_id = ?, updated_time = ? WHERE id = ?`,
		agentID, now, id,
	)
	if err != nil {
		return err
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *SessionRepo) BackfillAgentID(ctx context.Context, sessionID, userID, agentID string) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET agent_id = ?
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL AND agent_id IS NULL`,
		agentID, sessionID, userID,
	)
	if err != nil {
		return false, fmt.Errorf("SessionRepo.BackfillAgentID(%s): %w", sessionID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SessionRepo.BackfillAgentID(%s) rows affected: %w", sessionID, err)
	}
	return n > 0, nil
}

func (r *SessionRepo) SoftDelete(ctx context.Context, id, userID string) (int64, error) {
	now := time.Now().UnixMilli()
	var cancelsOwed int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE sessions SET deleted_at = ?, deleted_by = ?, updated_time = ?
			 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
			now, userID, now, id, userID,
		)
		if err != nil {
			return fmt.Errorf("SessionRepo.SoftDelete(%s): %w", id, err)
		}
		if affectedErr := dbutil.CheckRowsAffected(result); affectedErr != nil {
			return affectedErr
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM gateway_session_keys WHERE session_id = ?`,
			id,
		); err != nil {
			return fmt.Errorf("SessionRepo.SoftDelete(%s) cleanup mapping: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM mcp_artifact_links WHERE session_id = ?`,
			id,
		); err != nil {
			return fmt.Errorf("SessionRepo.SoftDelete(%s) delete MCP artifact links: %w", id, err)
		}
		marked, markErr := tx.ExecContext(ctx,
			`UPDATE task_records SET cancel_requested_at = ? WHERE session_id = ? AND cancel_requested_at IS NULL`,
			now, id,
		)
		if markErr != nil {
			return fmt.Errorf("SessionRepo.SoftDelete(%s) request task cancels: %w", id, markErr)
		}
		cancelsOwed, _ = marked.RowsAffected()
		return nil
	})
	return cancelsOwed, err
}

func (r *SessionRepo) HideEmptySession(ctx context.Context, id, userID string) (bool, error) {
	now := time.Now().UnixMilli()
	hidden := false
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE sessions SET deleted_at = ?, deleted_by = ?, updated_time = ?
			 WHERE id = ? AND user_id = ? AND deleted_at IS NULL
			   AND NOT EXISTS (SELECT 1 FROM chat_tasks WHERE chat_tasks.session_id = sessions.id)`,
			now, userID, now, id, userID,
		)
		if err != nil {
			return fmt.Errorf("SessionRepo.HideEmptySession(%s): %w", id, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("SessionRepo.HideEmptySession(%s) rows affected: %w", id, err)
		}
		if n == 0 {
			return nil
		}
		hidden = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM gateway_session_keys WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("SessionRepo.HideEmptySession(%s) cleanup mapping: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_artifact_links WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("SessionRepo.HideEmptySession(%s) delete MCP artifact links: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return hidden, nil
}

func (r *SessionRepo) AssignProject(ctx context.Context, sessionID, projectID string) error {
	now := time.Now().UnixMilli()
	var pid any = projectID
	if projectID == "" {
		pid = nil
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET project_id = ?, updated_time = ? WHERE id = ?`,
		pid, now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("SessionRepo.AssignProject(%s): %w", sessionID, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *SessionRepo) MarkViewed(ctx context.Context, sessionID, userID string, viewedAt int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE sessions
		 SET last_viewed_at = CASE
		   WHEN COALESCE(updated_time, 0) > ? THEN COALESCE(updated_time, 0)
		   ELSE ?
		 END
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		viewedAt, viewedAt, sessionID, userID,
	)
	if err != nil {
		return false, fmt.Errorf("SessionRepo.MarkViewed(%s): %w", sessionID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SessionRepo.MarkViewed(%s) rows affected: %w", sessionID, err)
	}
	return n > 0, nil
}

func (r *SessionRepo) Search(ctx context.Context, userID, query string, f SessionSearchFilter, limit, offset int) ([]*Session, int, error) {
	namePred, namePattern := dbutil.LikeContainsFold("name", query)
	where := "WHERE user_id = ? AND deleted_at IS NULL AND " + namePred
	args := []any{userID, namePattern}
	where, args = appendProjectFilter(where, args, f.ProjectID)
	where += sessionExcludeInternalClause
	where += sessionExcludeExternalGatewayClause
	if f.NonEmptyOnly {
		where += sessionHasActivityClause
	}
	if len(f.IncludeAgentIDs) > 0 {
		placeholders := make([]string, len(f.IncludeAgentIDs))
		for i, id := range f.IncludeAgentIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		where += " AND agent_id IN (" + strings.Join(placeholders, ",") + ")"
	} else {
		for _, agentID := range f.ExcludeAgentIDs {
			where += " AND (agent_id IS NULL OR agent_id != ?)"
			args = append(args, agentID)
		}
	}

	return listPageWithDerivedTotal[Session](ctx, r.db, sessionColumns, "sessions", where,
		sessionListOrder, args, limit, offset, "SessionRepo.Search")
}
