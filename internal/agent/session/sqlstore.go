// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const errUnmarshalMetadata = "unmarshal metadata: %w"

const errUnmarshalResults = "unmarshal results: %w"

type SQLStore struct {
	db *dbutil.DB

	msgCache *msgCache

	msgRowsRead atomic.Int64
}

func NewSQLStore(db *dbutil.DB) *SQLStore {
	return &SQLStore{
		db:       db,
		msgCache: newMsgCache(defaultMsgCacheEntries, defaultMsgCacheBytes),
	}
}

func (s *SQLStore) Close() error {
	return s.db.Close()
}

func (s *SQLStore) migrateConfig() dbutil.MigrateConfig {
	return dbutil.MigrateConfig{
		DB:         s.db.RawDB(),
		Dialect:    s.db.Dialect(),
		TableName:  "session_goose_version",
		SQLiteFS:   sqliteMigrations(),
		PostgresFS: postgresMigrations(),
		Logger:     s.db.Logger(),
	}
}

func (s *SQLStore) Migrate(ctx context.Context) error {
	return dbutil.Migrate(ctx, s.migrateConfig())
}

func (s *SQLStore) MigrateDown(ctx context.Context) error {
	return dbutil.MigrateDown(ctx, s.migrateConfig())
}

func (s *SQLStore) MigrateUpTo(ctx context.Context, version int64) error {
	return dbutil.MigrateUpTo(ctx, s.migrateConfig(), version)
}

func (s *SQLStore) Get(ctx context.Context, appName, userID, sessionID string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT version, context_id, task_id, metadata, created_at, updated_at, last_seq
		 FROM sessions WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		appName, userID, sessionID,
	)
	sess, lastSeq, err := scanSessionRow(row, appName, userID, sessionID)
	if err != nil || sess == nil {
		return sess, err
	}
	msgs, err := s.loadMessagesCached(ctx, msgKey{appName, userID, sessionID}, lastSeq)
	if err != nil {
		return nil, err
	}
	sess.Messages = msgs
	return sess, nil
}

func (s *SQLStore) loadMessagesCached(ctx context.Context, key msgKey, lastSeq int64) ([]Message, error) {
	if cached, ok := s.msgCache.get(key, lastSeq); ok {
		return cached, nil
	}
	msgs, err := s.loadMessages(ctx, key.appName, key.userID, key.sessionID)
	if err != nil {
		return nil, err
	}
	s.msgCache.put(key, msgs, lastSeq)
	return msgs, nil
}

func (s *SQLStore) GetByContextAndTask(ctx context.Context, appName, userID, contextID, taskID string) (*Session, error) {
	if taskID == "" {
		return nil, fmt.Errorf("GetByContextAndTask: taskID is required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT session_id, version, context_id, task_id, metadata, created_at, updated_at, last_seq
		 FROM sessions
		 WHERE app_name = ? AND user_id = ? AND context_id = ? AND task_id = ?`,
		appName, userID, contextID, taskID,
	)

	var sessionID, metadataJSON, createdStr, updatedStr string
	var loadedContextID string
	var loadedTaskID sql.NullString
	var version int
	var lastSeq int64

	err := row.Scan(&sessionID, &version, &loadedContextID, &loadedTaskID, &metadataJSON, &createdStr, &updatedStr, &lastSeq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	sess := &Session{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		ContextID: loadedContextID,
		Version:   version,
	}
	if loadedTaskID.Valid {
		v := loadedTaskID.String
		sess.TaskID = &v
	}
	if unmarshalErr := json.Unmarshal([]byte(metadataJSON), &sess.Metadata); unmarshalErr != nil {
		return nil, fmt.Errorf(errUnmarshalMetadata, unmarshalErr)
	}
	if t, parseErr := time.Parse(time.RFC3339Nano, createdStr); parseErr == nil {
		sess.CreatedAt = t
	}
	if t, parseErr := time.Parse(time.RFC3339Nano, updatedStr); parseErr == nil {
		sess.UpdatedAt = t
	}

	msgs, err := s.loadMessagesCached(ctx, msgKey{appName, userID, sessionID}, lastSeq)
	if err != nil {
		return nil, err
	}
	sess.Messages = msgs

	return sess, nil
}

func scanSessionRow(row *sql.Row, appName, userID, sessionID string) (*Session, int64, error) {
	var metadataJSON, createdStr, updatedStr string
	var contextID string
	var taskID sql.NullString
	var version int
	var lastSeq int64

	err := row.Scan(&version, &contextID, &taskID, &metadataJSON, &createdStr, &updatedStr, &lastSeq)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	sess := &Session{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
		ContextID: contextID,
		Version:   version,
	}
	if taskID.Valid {
		v := taskID.String
		sess.TaskID = &v
	}
	if err := json.Unmarshal([]byte(metadataJSON), &sess.Metadata); err != nil {
		return nil, 0, fmt.Errorf(errUnmarshalMetadata, err)
	}
	if t, parseErr := time.Parse(time.RFC3339Nano, createdStr); parseErr != nil {
		nlog().Warn("session: failed to parse created_at", "sessionID", sessionID, "error", parseErr)
	} else {
		sess.CreatedAt = t
	}
	if t, parseErr := time.Parse(time.RFC3339Nano, updatedStr); parseErr != nil {
		nlog().Warn("session: failed to parse updated_at", "sessionID", sessionID, "error", parseErr)
	} else {
		sess.UpdatedAt = t
	}

	return sess, lastSeq, nil
}

func (s *SQLStore) Create(ctx context.Context, sess *Session) error {
	metadataJSON, err := json.Marshal(sess.Metadata)
	if err != nil {
		return err
	}
	if sess.Metadata == nil {
		metadataJSON = []byte("{}")
	}

	if sess.ContextID == "" {
		sess.ContextID = sess.SessionID
	}
	var taskArg any
	if sess.TaskID != nil {
		taskArg = *sess.TaskID
	}

	now := time.Now()
	nowStr := now.Format(time.RFC3339Nano)
	err = s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, execErr := tx.ExecContext(ctx,
			`INSERT INTO sessions (app_name, user_id, session_id, context_id, task_id, version, metadata, last_seq, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
			sess.AppName, sess.UserID, sess.SessionID, sess.ContextID, taskArg,
			string(metadataJSON), len(sess.Messages), nowStr, nowStr,
		); execErr != nil {
			return execErr
		}
		return insertMessageRows(ctx, tx, msgKey{sess.AppName, sess.UserID, sess.SessionID}, 1, sess.Messages, nowStr)
	})
	if dbutil.IsUniqueViolation(err) {
		return fmt.Errorf("create session: %w: %w", ErrSessionExists, err)
	}
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	sess.Version = 0
	sess.CreatedAt = now
	sess.UpdatedAt = now
	s.msgCache.put(msgKey{sess.AppName, sess.UserID, sess.SessionID}, sess.Messages, int64(len(sess.Messages)))
	return nil
}

func (s *SQLStore) Save(ctx context.Context, sess *Session) error {
	metadataJSON, err := json.Marshal(sess.Metadata)
	if err != nil {
		return err
	}

	now := time.Now()
	newVersion := sess.Version + 1

	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions
		 SET version = ?, metadata = ?, updated_at = ?
		 WHERE app_name = ? AND user_id = ? AND session_id = ? AND version = ?`,
		newVersion, string(metadataJSON),
		now.Format(time.RFC3339Nano),
		sess.AppName, sess.UserID, sess.SessionID, sess.Version,
	)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: session not found or version changed", ErrOptimisticLock)
	}

	sess.Version = newVersion
	sess.UpdatedAt = now
	return nil
}

func (s *SQLStore) Delete(ctx context.Context, appName, userID, sessionID string) error {
	if err := s.deleteSessionRunningTasks(ctx, appName, userID, sessionID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		appName, userID, sessionID,
	)
	if err != nil {
		return err
	}
	s.msgCache.evict(msgKey{appName, userID, sessionID})
	return nil
}

func (s *SQLStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	key := msgKey{appName, userID, sessionID}
	var base int64
	var err error
	if s.db.Dialect() == "postgres" && len(msgs) <= messageInsertBatch {
		base, err = s.appendMessagesSingleStatement(ctx, key, msgs)
	} else {
		base, err = s.appendMessagesReserveThenInsert(ctx, key, msgs)
	}
	if err != nil {
		return err
	}
	s.msgCache.appendOrEvict(key, msgs, base)
	return nil
}

func seqBumpExpr(dialect string) string {
	scalarMax := "GREATEST"
	if dialect != "postgres" {
		scalarMax = "max"
	}
	return scalarMax + `(last_seq, COALESCE((SELECT MAX(sm.seq) FROM session_messages sm` +
		` WHERE sm.app_name = sessions.app_name AND sm.user_id = sessions.user_id` +
		` AND sm.session_id = sessions.session_id), 0)) + ?`
}

func (s *SQLStore) appendMessagesSingleStatement(ctx context.Context, key msgKey, msgs []Message) (int64, error) {
	n := int64(len(msgs))
	var b strings.Builder
	b.WriteString(`WITH bumped AS (` +
		` UPDATE sessions SET last_seq = ` + seqBumpExpr("postgres") +
		` WHERE app_name = ? AND user_id = ? AND session_id = ?` +
		` RETURNING last_seq - ? AS base ),` +
		` ins AS (` +
		` INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)` +
		` SELECT ?, ?, ?, bumped.base + v.ord, v.msg, ?` +
		` FROM bumped CROSS JOIN (VALUES `)

	args := make([]any, 0, 9+len(msgs))
	args = append(args, n, key.appName, key.userID, key.sessionID, n)
	createdAt := time.Now().Format(time.RFC3339Nano)
	args = append(args, key.appName, key.userID, key.sessionID, createdAt)
	for i := range msgs {
		msgJSON, err := json.Marshal(msgs[i])
		if err != nil {
			return 0, fmt.Errorf("append messages: marshal message %d: %w", i, err)
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "(%d, ?)", i+1)
		args = append(args, string(msgJSON))
	}
	b.WriteString(`) AS v(ord, msg) RETURNING 1 )` +
		` SELECT (SELECT base FROM bumped), (SELECT COUNT(*) FROM ins)`)

	var base sql.NullInt64
	var inserted int64
	if err := s.db.QueryRowContext(ctx, b.String(), args...).Scan(&base, &inserted); err != nil {
		return 0, fmt.Errorf("append messages: %w", err)
	}
	if inserted == 0 || !base.Valid {
		return 0, fmt.Errorf("append messages: session %s/%s/%s not found", key.appName, key.userID, key.sessionID)
	}
	return base.Int64, nil
}

func (s *SQLStore) appendMessagesReserveThenInsert(ctx context.Context, key msgKey, msgs []Message) (int64, error) {
	var base int64
	err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		n := int64(len(msgs))
		err := tx.QueryRowContext(ctx,
			`UPDATE sessions SET last_seq = `+seqBumpExpr(s.db.Dialect())+
				` WHERE app_name = ? AND user_id = ? AND session_id = ?`+
				` RETURNING last_seq - ?`,
			n, key.appName, key.userID, key.sessionID, n,
		).Scan(&base)
		if err == sql.ErrNoRows {
			return fmt.Errorf("append messages: session %s/%s/%s not found", key.appName, key.userID, key.sessionID)
		}
		if err != nil {
			return fmt.Errorf("append messages: reserve seq: %w", err)
		}
		return insertMessageRows(ctx, tx, key, base+1, msgs, time.Now().Format(time.RFC3339Nano))
	})
	if err != nil {
		return 0, err
	}
	return base, nil
}

func (s *SQLStore) loadMessages(ctx context.Context, appName, userID, sessionID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT message FROM session_messages
		 WHERE app_name = ? AND user_id = ? AND session_id = ? ORDER BY seq`,
		appName, userID, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var msgs []Message
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		var m Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("unmarshal message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	s.msgRowsRead.Add(int64(len(msgs)))
	return msgs, nil
}

const messageInsertBatch = 500

type msgKey struct {
	appName   string
	userID    string
	sessionID string
}

func insertMessageRows(ctx context.Context, tx *dbutil.Tx, key msgKey, startSeq int64, msgs []Message, createdAt string) error {
	for start := 0; start < len(msgs); start += messageInsertBatch {
		end := min(start+messageInsertBatch, len(msgs))
		var placeholders strings.Builder
		args := make([]any, 0, (end-start)*6)
		for i := start; i < end; i++ {
			b, err := json.Marshal(msgs[i])
			if err != nil {
				return fmt.Errorf("marshal message %d: %w", i, err)
			}
			if i > start {
				placeholders.WriteString(", ")
			}
			placeholders.WriteString("(?, ?, ?, ?, ?, ?)")
			args = append(args, key.appName, key.userID, key.sessionID, startSeq+int64(i), string(b), createdAt)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at) VALUES `+placeholders.String(),
			args...,
		); err != nil {
			return fmt.Errorf("insert messages: %w", err)
		}
	}
	return nil
}

func (s *SQLStore) ListSessions(ctx context.Context, appName, userID string) ([]*Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id, context_id, task_id, version, metadata, created_at, updated_at
		 FROM sessions WHERE app_name = ? AND user_id = ?`,
		appName, userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*Session
	for rows.Next() {
		var sessionID, contextID, metadataJSON, createdStr, updatedStr string
		var taskID sql.NullString
		var version int
		if err := rows.Scan(&sessionID, &contextID, &taskID, &version, &metadataJSON, &createdStr, &updatedStr); err != nil {
			return nil, err
		}

		sess := &Session{
			AppName:   appName,
			UserID:    userID,
			SessionID: sessionID,
			ContextID: contextID,
			Version:   version,
		}
		if taskID.Valid {
			v := taskID.String
			sess.TaskID = &v
		}
		if err := json.Unmarshal([]byte(metadataJSON), &sess.Metadata); err != nil {
			return nil, fmt.Errorf(errUnmarshalMetadata, err)
		}
		if t, parseErr := time.Parse(time.RFC3339Nano, createdStr); parseErr == nil {
			sess.CreatedAt = t
		}
		if t, parseErr := time.Parse(time.RFC3339Nano, updatedStr); parseErr == nil {
			sess.UpdatedAt = t
		}
		result = append(result, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []*Session{}
	}
	return result, nil
}

func (s *SQLStore) dbNowMillis() string {
	if s.db.Dialect() == "postgres" {
		return `(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT`
	}
	return `CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`
}

const heldBySQL = `(owner_id = ? OR owner_id IS NULL OR owner_id = '') AND final_message IS NULL`

func (s *SQLStore) ownerGone(silentFor time.Duration, cutoff time.Time, releasedOnly bool) (string, []any) {
	if releasedOnly {
		return `(owner_id IS NULL OR owner_id = '')`, nil
	}
	return `(owner_id IS NULL OR owner_id = ''
		OR (renewed_at IS NOT NULL AND renewed_at <= ` + s.dbNowMillis() + ` - ?)
		OR (renewed_at IS NULL AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))`,
		[]any{silentFor.Milliseconds(), formatDBTime(cutoff)}
}

func checkpointResultsJSON(cp *TaskCheckpoint) (string, error) {
	if cp.ToolResults == nil {
		return "[]", nil
	}
	b, err := json.Marshal(cp.ToolResults)
	return string(b), err
}

func checkpointLease(cp *TaskCheckpoint) any {
	if cp.LeaseExpiresAt == nil {
		return nil
	}
	return formatDBTime(*cp.LeaseExpiresAt)
}

func rowsWritten(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotOwner
	}
	return nil
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *SQLStore) SaveCheckpoint(ctx context.Context, cp *TaskCheckpoint) error {
	return s.writeCheckpoint(ctx, cp, s.upsertCheckpoint)
}

func (s *SQLStore) UpdateCheckpoint(ctx context.Context, cp *TaskCheckpoint) error {
	return s.writeCheckpoint(ctx, cp, s.updateCheckpointRow)
}

func (s *SQLStore) writeCheckpoint(ctx context.Context, cp *TaskCheckpoint, write func(context.Context, sqlExecer, *TaskCheckpoint) error) error {
	if !cp.ReplacePendingCalls {
		return write(ctx, s.db, cp)
	}
	return s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := write(ctx, tx, cp); err != nil {
			return err
		}
		return replacePendingCalls(ctx, tx, cp.TaskID, cp.PendingCalls)
	})
}

func (s *SQLStore) upsertCheckpoint(ctx context.Context, db sqlExecer, cp *TaskCheckpoint) error {
	resultsJSON, err := checkpointResultsJSON(cp)
	if err != nil {
		return err
	}
	now := formatDBTime(time.Now())
	return rowsWritten(db.ExecContext(ctx,
		`INSERT INTO task_checkpoints (task_id, app_name, user_id, session_id, agent_name, pending_count, state, tool_results, owner_id, lease_expires_at, trace_id, created_at, updated_at, renewed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+s.dbNowMillis()+`)
			ON CONFLICT (task_id) DO UPDATE SET
				pending_count = EXCLUDED.pending_count,
				state = EXCLUDED.state,
				tool_results = EXCLUDED.tool_results,
				owner_id = EXCLUDED.owner_id,
				lease_expires_at = EXCLUDED.lease_expires_at,
				trace_id = EXCLUDED.trace_id,
				updated_at = EXCLUDED.updated_at,
				renewed_at = EXCLUDED.renewed_at
			WHERE (task_checkpoints.owner_id = EXCLUDED.owner_id OR task_checkpoints.owner_id IS NULL OR task_checkpoints.owner_id = '')
			  AND task_checkpoints.final_message IS NULL`,
		cp.TaskID, cp.AppName, cp.UserID, cp.SessionID, cp.AgentName,
		cp.PendingCount, cp.State, resultsJSON, cp.OwnerID, checkpointLease(cp), cp.TraceID,
		now, now,
	))
}

func (s *SQLStore) updateCheckpointRow(ctx context.Context, db sqlExecer, cp *TaskCheckpoint) error {
	resultsJSON, err := checkpointResultsJSON(cp)
	if err != nil {
		return err
	}
	return rowsWritten(db.ExecContext(ctx,
		`UPDATE task_checkpoints
		    SET pending_count = ?, state = ?, tool_results = ?, owner_id = ?, lease_expires_at = ?,
		        trace_id = ?, updated_at = ?, renewed_at = `+s.dbNowMillis()+`
		  WHERE task_id = ? AND `+heldBySQL,
		cp.PendingCount, cp.State, resultsJSON, cp.OwnerID, checkpointLease(cp),
		cp.TraceID, formatDBTime(time.Now()), cp.TaskID, cp.OwnerID,
	))
}

func (s *SQLStore) MarkAnswering(ctx context.Context, taskID, ownerID string, final *FinalMessage, until time.Time) (bool, error) {
	finalJSON, err := json.Marshal(final)
	if err != nil {
		return false, fmt.Errorf("marshal final message: %w", err)
	}
	return dbutil.WithTxVal[bool](ctx, s.db, func(tx *dbutil.Tx) (bool, error) {
		err := rowsWritten(tx.ExecContext(ctx,
			`UPDATE task_checkpoints
			    SET final_message = ?, pending_count = 0, owner_id = ?, lease_expires_at = ?,
			        updated_at = ?, renewed_at = `+s.dbNowMillis()+`
			  WHERE task_id = ? AND `+heldBySQL,
			string(finalJSON), ownerID, formatDBTime(until), formatDBTime(time.Now()), taskID, ownerID))
		if errors.Is(err, ErrNotOwner) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_pending_calls WHERE task_id = ?`, taskID); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *SQLStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *ToolCallResult) (int, error) {
	if result == nil || result.ToolCallID == "" {
		return 0, errors.New("session: tool result has no tool call id")
	}
	return dbutil.WithTxVal[int](ctx, s.db, func(tx *dbutil.Tx) (int, error) {
		selectQuery := s.lockingRow(`SELECT pending_count, tool_results FROM task_checkpoints WHERE task_id = ?`)
		var pendingCount int
		var resultsJSON string
		err := tx.QueryRowContext(ctx, selectQuery, taskID).Scan(&pendingCount, &resultsJSON)
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("checkpoint %q not found", taskID)
		}
		if err != nil {
			return 0, err
		}

		var results []ToolCallResult
		if unmarshalErr := json.Unmarshal([]byte(resultsJSON), &results); unmarshalErr != nil {
			return 0, fmt.Errorf(errUnmarshalResults, unmarshalErr)
		}

		newPending, seen := pendingCount, false
		for i := range results {
			if results[i].ToolCallID == result.ToolCallID {
				seen = true
				break
			}
		}

		if corrID != "" {
			res, delErr := tx.ExecContext(ctx,
				`DELETE FROM task_pending_calls WHERE corr_id = ? AND task_id = ?`, corrID, taskID)
			if delErr != nil {
				return 0, delErr
			}
			consumed, delErr := res.RowsAffected()
			if delErr != nil {
				return 0, delErr
			}
			if consumed == 0 {
				return pendingCount, ErrCallNotOutstanding
			}
		}

		if !seen {
			results = append(results, *result)
			if !result.Uncounted {
				newPending = pendingCount - 1
			}
		}

		newResultsJSON, err := json.Marshal(results)
		if err != nil {
			return 0, err
		}

		if newPending < 0 {
			nlog().Warn("session: pending count went negative, clamping to 0",
				"taskID", taskID, "pendingCountBefore", pendingCount)
			newPending = 0
		}

		now := time.Now()
		_, err = tx.ExecContext(ctx,
			`UPDATE task_checkpoints SET pending_count = ?, tool_results = ?, updated_at = ? WHERE task_id = ?`,
			newPending, string(newResultsJSON), formatDBTime(now), taskID,
		)
		if err != nil {
			return 0, err
		}

		return newPending, nil
	})
}

func (s *SQLStore) checkpointColumns() string {
	return `task_id, app_name, user_id, session_id, agent_name,
	pending_count, state, tool_results, owner_id, lease_expires_at, trace_id, created_at, updated_at,
	renewed_at, final_message, ` + s.dbNowMillis()
}

func scanCheckpoint(row interface{ Scan(...any) error }) (*TaskCheckpoint, error) {
	var cp TaskCheckpoint
	var resultsJSON, createdStr, updatedStr string
	var ownerID, leaseStr, finalJSON sql.NullString
	var renewedAt sql.NullInt64
	var readAt int64
	if err := row.Scan(&cp.TaskID, &cp.AppName, &cp.UserID, &cp.SessionID, &cp.AgentName,
		&cp.PendingCount, &cp.State, &resultsJSON, &ownerID, &leaseStr, &cp.TraceID,
		&createdStr, &updatedStr, &renewedAt, &finalJSON, &readAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(resultsJSON), &cp.ToolResults); err != nil {
		return nil, fmt.Errorf(errUnmarshalResults, err)
	}
	cp.OwnerID = ownerID.String
	if leaseStr.Valid {
		t := parsePendingTime(leaseStr.String, "lease_expires_at")
		cp.LeaseExpiresAt = &t
	}
	if renewedAt.Valid {
		t := time.UnixMilli(renewedAt.Int64).UTC()
		cp.RenewedAt = &t
	}
	cp.ReadAt = time.UnixMilli(readAt).UTC()
	if finalJSON.Valid {
		var final FinalMessage
		if err := json.Unmarshal([]byte(finalJSON.String), &final); err != nil {
			return nil, fmt.Errorf("unmarshal final message: %w", err)
		}
		cp.Final = &final
	}
	cp.CreatedAt = parsePendingTime(createdStr, "created_at")
	cp.UpdatedAt = parsePendingTime(updatedStr, "updated_at")
	return &cp, nil
}

func (s *SQLStore) AdoptCheckpoint(ctx context.Context, taskID, newOwnerID string, now time.Time, leaseFor, silentFor time.Duration, releasedOnly bool) (*TaskCheckpoint, error) {
	gone, goneArgs := s.ownerGone(silentFor, now, releasedOnly)
	return dbutil.WithTxVal[*TaskCheckpoint](ctx, s.db, func(tx *dbutil.Tx) (*TaskCheckpoint, error) {
		result, err := tx.ExecContext(ctx,
			`UPDATE task_checkpoints
			    SET owner_id = ?, lease_expires_at = ?, updated_at = ?, renewed_at = `+s.dbNowMillis()+`
			  WHERE task_id = ?
			    AND pending_count = 0
			    AND `+gone,
			append([]any{newOwnerID, formatDBTime(now.Add(leaseFor)), formatDBTime(now), taskID}, goneArgs...)...,
		)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, nil
		}
		return scanCheckpoint(tx.QueryRowContext(ctx,
			`SELECT `+s.checkpointColumns()+` FROM task_checkpoints WHERE task_id = ?`, taskID))
	})
}

func (s *SQLStore) GetCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error) {
	cp, err := scanCheckpoint(s.db.QueryRowContext(ctx,
		`SELECT `+s.checkpointColumns()+` FROM task_checkpoints WHERE task_id = ?`, taskID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cp, nil
}

func (s *SQLStore) ReclaimUnowned(ctx context.Context, taskID, ownerID string, until time.Time) (bool, error) {
	if ownerID == "" {
		return false, nil
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE task_checkpoints SET owner_id = ?, lease_expires_at = ?, renewed_at = `+s.dbNowMillis()+`
		  WHERE task_id = ? AND owner_id IS NULL`,
		ownerID, formatDBTime(until), taskID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLStore) CheckpointOwner(ctx context.Context, taskID string) (string, bool, error) {
	var ownerID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_id FROM task_checkpoints WHERE task_id = ?`, taskID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return ownerID.String, true, nil
}

func (s *SQLStore) RenewLeases(ctx context.Context, ownerID string, taskIDs []string, until time.Time) (int, error) {
	if ownerID == "" || len(taskIDs) == 0 {
		var one int
		return 0, s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	}
	renewed := 0
	for chunk := range slices.Chunk(taskIDs, dbutil.InClauseCap) {
		clause, inArgs, err := dbutil.InClause(chunk)
		if err != nil {
			return renewed, err
		}
		args := append([]any{formatDBTime(until), ownerID}, inArgs...)
		result, err := s.db.ExecContext(ctx,
			`UPDATE task_checkpoints SET lease_expires_at = ?, renewed_at = `+s.dbNowMillis()+` WHERE owner_id = ? AND task_id IN (`+clause+`)`,
			args...)
		if err != nil {
			return renewed, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return renewed, err
		}
		renewed += int(affected)
	}
	return renewed, nil
}

func (s *SQLStore) ReleaseOwnership(ctx context.Context, taskID, ownerID string) error {
	if ownerID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_checkpoints SET owner_id = NULL, lease_expires_at = ?
		  WHERE task_id = ? AND owner_id = ?`,
		formatDBTime(time.Now()), taskID, ownerID)
	return err
}

func (s *SQLStore) PopCheckpoint(ctx context.Context, taskID string) (*TaskCheckpoint, error) {
	return dbutil.WithTxVal[*TaskCheckpoint](ctx, s.db, func(tx *dbutil.Tx) (*TaskCheckpoint, error) {
		query := s.lockingRow(`SELECT ` + s.checkpointColumns() + ` FROM task_checkpoints WHERE task_id = ? AND final_message IS NULL`)
		cp, err := scanCheckpoint(tx.QueryRowContext(ctx, query, taskID))
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if err := rowsWritten(tx.ExecContext(ctx,
			`DELETE FROM task_checkpoints WHERE task_id = ? AND final_message IS NULL`, taskID)); err != nil {
			if errors.Is(err, ErrNotOwner) {
				return nil, nil
			}
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_pending_calls WHERE task_id = ?`, taskID); err != nil {
			return nil, err
		}
		return cp, nil
	})
}

func (s *SQLStore) DeleteCheckpointIfOwned(ctx context.Context, taskID, ownerID string) (bool, error) {
	return dbutil.WithTxVal[bool](ctx, s.db, func(tx *dbutil.Tx) (bool, error) {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM task_checkpoints
			  WHERE task_id = ? AND (owner_id = ? OR owner_id IS NULL OR owner_id = '')`,
			taskID, ownerID)
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected == 0 {
			return false, nil
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_pending_calls WHERE task_id = ?`, taskID); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *SQLStore) PurgeAbandonedCheckpoints(ctx context.Context, agentName string, lapsedBefore time.Time, limit int) (int, error) {
	cutoff := formatDBTime(lapsedBefore)
	agentClause, filterArgs := "", []any{cutoff}
	if agentName != "" {
		agentClause = " AND agent_name = ?"
		filterArgs = append(filterArgs, agentName)
	}
	lapsed := `(lease_expires_at IS NULL OR lease_expires_at <= ?)` + agentClause
	checkpointArgs := append(append(append([]any{}, filterArgs...), filterArgs...), limit)
	orphanArgs := append(append([]any{}, filterArgs...), limit)
	return dbutil.WithTxVal[int](ctx, s.db, func(tx *dbutil.Tx) (int, error) {
		rows, err := tx.QueryContext(ctx,
			`DELETE FROM task_checkpoints
			  WHERE `+lapsed+`
			    AND task_id IN (SELECT task_id FROM task_checkpoints WHERE `+lapsed+` LIMIT ?)
			  RETURNING task_id`,
			checkpointArgs...)
		if err != nil {
			return 0, err
		}
		var taskIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return 0, err
			}
			taskIDs = append(taskIDs, id)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		for chunk := range slices.Chunk(taskIDs, dbutil.InClauseCap) {
			clause, inArgs, err := dbutil.InClause(chunk)
			if err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM task_pending_calls WHERE task_id IN (`+clause+`)`, inArgs...); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_pending_calls WHERE corr_id IN (
			   SELECT corr_id FROM task_pending_calls
			    WHERE expires_at IS NOT NULL AND expires_at <= ?`+agentClause+`
			      AND NOT EXISTS (SELECT 1 FROM task_checkpoints c WHERE CAST(c.task_id AS TEXT) = task_pending_calls.task_id)
			    LIMIT ?)`,
			orphanArgs...); err != nil {
			return 0, err
		}
		return len(taskIDs), nil
	})
}

func (s *SQLStore) DeleteAbandonedCheckpoint(ctx context.Context, taskID string, lapsedBefore time.Time) (bool, error) {
	return dbutil.WithTxVal[bool](ctx, s.db, func(tx *dbutil.Tx) (bool, error) {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM task_checkpoints
			  WHERE task_id = ?
			    AND (owner_id IS NULL OR owner_id = ''
			         OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			taskID, formatDBTime(lapsedBefore))
		if err != nil {
			return false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if affected == 0 {
			var live bool
			err := tx.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM task_checkpoints WHERE task_id = ?)`, taskID).Scan(&live)
			if err != nil {
				return false, err
			}
			if live {
				return false, nil
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_pending_calls WHERE task_id = ?`, taskID); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *SQLStore) DeleteCheckpoint(ctx context.Context, taskID string) error {
	return s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_checkpoints WHERE task_id = ?`, taskID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM task_pending_calls WHERE task_id = ?`, taskID)
		return err
	})
}

func (s *SQLStore) RegisterPendingCalls(ctx context.Context, taskID, ownerID string, calls []PendingCall) error {
	return s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		held := s.lockingRow(`SELECT 1 FROM task_checkpoints WHERE task_id = ? AND ` + heldBySQL)
		var one int
		if err := tx.QueryRowContext(ctx, held, taskID, ownerID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotOwner
			}
			return err
		}
		return replacePendingCalls(ctx, tx, taskID, calls)
	})
}

func replacePendingCalls(ctx context.Context, tx *dbutil.Tx, taskID string, calls []PendingCall) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_pending_calls WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	for i := range calls {
		if err := insertPendingCall(ctx, tx, &calls[i]); err != nil {
			return err
		}
	}
	return nil
}

func insertPendingCall(ctx context.Context, tx *dbutil.Tx, c *PendingCall) error {
	var expires any
	if c.ExpiresAt != nil {
		expires = formatDBTime(*c.ExpiresAt)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO task_pending_calls (corr_id, task_id, agent_name, tool_call_id, tool_name, trace_id, kind, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (corr_id) DO UPDATE SET
				task_id = EXCLUDED.task_id,
				agent_name = EXCLUDED.agent_name,
				tool_call_id = EXCLUDED.tool_call_id,
				tool_name = EXCLUDED.tool_name,
				trace_id = EXCLUDED.trace_id,
				kind = EXCLUDED.kind,
				expires_at = EXCLUDED.expires_at
			WHERE task_pending_calls.task_id = EXCLUDED.task_id`,
		c.CorrID, c.TaskID, c.AgentName, c.ToolCallID, c.ToolName, c.TraceID, c.Kind, formatDBTime(time.Now()), expires,
	)
	if err != nil {
		return err
	}
	written, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if written == 0 {
		return &CorrelationTakenError{CorrID: c.CorrID}
	}
	return nil
}

func (s *SQLStore) Dialect() string {
	return s.db.Dialect()
}

func (s *SQLStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*PendingCall, error) {
	var pc PendingCall
	var createdStr string
	var expiresStr sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT corr_id, task_id, agent_name, tool_call_id, tool_name, trace_id, kind, created_at, expires_at
		   FROM task_pending_calls WHERE corr_id = ? AND agent_name = ?`,
		corrID, agentName,
	).Scan(&pc.CorrID, &pc.TaskID, &pc.AgentName, &pc.ToolCallID, &pc.ToolName, &pc.TraceID, &pc.Kind, &createdStr, &expiresStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pc.CreatedAt = parsePendingTime(createdStr, "created_at")
	if expiresStr.Valid {
		t := parsePendingTime(expiresStr.String, "expires_at")
		pc.ExpiresAt = &t
	}
	return &pc, nil
}

func (s *SQLStore) PendingCallTask(ctx context.Context, corrID string) (string, bool, error) {
	var taskID string
	err := s.db.QueryRowContext(ctx, `SELECT task_id FROM task_pending_calls WHERE corr_id = ?`, corrID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return taskID, true, nil
}

func (s *SQLStore) ExtendPendingCall(ctx context.Context, agentName, corrID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_pending_calls SET expires_at = ?
		  WHERE corr_id = ? AND agent_name = ? AND expires_at IS NOT NULL AND expires_at < ?`,
		formatDBTime(until), corrID, agentName, formatDBTime(until))
	return err
}

func (s *SQLStore) DeletePendingCall(ctx context.Context, corrID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM task_pending_calls WHERE corr_id = ?`, corrID)
	return err
}

func (s *SQLStore) ListExpiredPendingCalls(ctx context.Context, agentName string, now time.Time, limit int) ([]PendingCall, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT corr_id, task_id, agent_name, tool_call_id, tool_name, trace_id, kind, created_at, expires_at
		 FROM task_pending_calls
		 WHERE agent_name = ? AND expires_at IS NOT NULL AND expires_at <= ?
		 ORDER BY expires_at ASC
		 LIMIT ?`,
		agentName, formatDBTime(now), limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []PendingCall
	for rows.Next() {
		var pc PendingCall
		var createdStr string
		var expiresStr sql.NullString
		if err := rows.Scan(&pc.CorrID, &pc.TaskID, &pc.AgentName, &pc.ToolCallID, &pc.ToolName, &pc.TraceID, &pc.Kind, &createdStr, &expiresStr); err != nil {
			return nil, err
		}
		pc.CreatedAt = parsePendingTime(createdStr, "created_at")
		if expiresStr.Valid {
			t := parsePendingTime(expiresStr.String, "expires_at")
			pc.ExpiresAt = &t
		}
		out = append(out, pc)
	}
	return out, rows.Err()
}

func (s *SQLStore) ListResumableCheckpoints(ctx context.Context, agentName string, now time.Time, silentFor time.Duration, limit int, releasedOnly bool) ([]TaskCheckpoint, error) {
	gone, goneArgs := s.ownerGone(silentFor, now, releasedOnly)
	args := append(append([]any{agentName}, goneArgs...), limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+s.checkpointColumns()+`
		 FROM task_checkpoints
		 WHERE agent_name = ?
		   AND pending_count = 0
		   AND `+gone+`
		 ORDER BY lease_expires_at ASC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []TaskCheckpoint
	for rows.Next() {
		cp, scanErr := scanCheckpoint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *cp)
	}
	return out, rows.Err()
}

const dbTimeLayout = "2006-01-02T15:04:05.000000000Z"

func formatDBTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout)
}

func parsePendingTime(value, field string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		nlog().Warn("checkpoint: failed to parse timestamp", "field", field, "value", value, "error", err)
		return time.Time{}
	}
	return t
}

func (s *SQLStore) lockingRow(query string) string {
	if s.db.Dialect() == "postgres" {
		return query + " FOR UPDATE"
	}
	return query
}
