// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type GatewaySessionKeyRepo struct {
	db *DB
}

func NewGatewaySessionKeyRepo(db *DB) *GatewaySessionKeyRepo {
	return &GatewaySessionKeyRepo{db: db}
}

type GetOrCreateByExternalKeyArgs struct {
	GatewayType string
	ExternalKey string
	UserID      string
	AgentID     *string
	Name        *string
	Source      *string

	RollOnOwnerChange bool
}

func (r *GatewaySessionKeyRepo) GetOrCreateByExternalKey(ctx context.Context, args GetOrCreateByExternalKeyArgs) (string, error) {
	var sessionID string
	txErr := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		existing, err := lookupMappingTx(ctx, tx, args.GatewayType, args.ExternalKey)
		if err != nil {
			return err
		}
		if existing != "" {
			alive, owner, aliveErr := sessionOwnerTx(ctx, tx, existing)
			if aliveErr != nil {
				return aliveErr
			}
			if alive && (!args.RollOnOwnerChange || owner == args.UserID) {
				if args.GatewayType != GatewayTypeScheduled {
					if _, stampErr := tx.ExecContext(ctx,
						`UPDATE sessions SET external_gateway = 1 WHERE id = ? AND external_gateway = 0`,
						existing,
					); stampErr != nil {
						return fmt.Errorf("stamp external_gateway on session %s: %w", existing, stampErr)
					}
				}
				sessionID = existing
				return nil
			}
			if _, delErr := tx.ExecContext(ctx,
				`DELETE FROM gateway_session_keys WHERE gateway_type = ? AND external_key = ?`,
				args.GatewayType, args.ExternalKey,
			); delErr != nil {
				return fmt.Errorf("delete stale mapping: %w", delErr)
			}
		}

		newID := idgen.New()
		now := time.Now().UnixMilli()
		if _, insErr := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, name, user_id, agent_id, project_id, created_time, updated_time, source, has_activity, external_gateway)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID, args.Name, args.UserID, args.AgentID, nil, now, now, args.Source,
			dbutil.BoolToInt(isSchedulerSource(args.Source)),
			dbutil.BoolToInt(args.GatewayType != GatewayTypeScheduled),
		); insErr != nil {
			return fmt.Errorf("insert session: %w", insErr)
		}

		if _, mapErr := tx.ExecContext(ctx,
			`INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (gateway_type, external_key) DO NOTHING`,
			args.GatewayType, args.ExternalKey, newID, now,
		); mapErr != nil {
			return fmt.Errorf("insert mapping: %w", mapErr)
		}

		winner, lookupErr := lookupMappingTx(ctx, tx, args.GatewayType, args.ExternalKey)
		if lookupErr != nil {
			return lookupErr
		}
		if winner == "" {
			return fmt.Errorf("mapping missing after insert: gateway=%s key=%s", args.GatewayType, args.ExternalKey)
		}
		if winner != newID {
			if _, delErr := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, newID); delErr != nil {
				return fmt.Errorf("delete orphan session: %w", delErr)
			}
		}
		sessionID = winner
		return nil
	})
	if txErr != nil {
		return "", fmt.Errorf("GatewaySessionKeyRepo.GetOrCreateByExternalKey(%s,%s): %w", args.GatewayType, args.ExternalKey, txErr)
	}
	return sessionID, nil
}

func (r *GatewaySessionKeyRepo) CreateMapping(ctx context.Context, gatewayType, externalKey, sessionID string) error {
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
			 VALUES (?, ?, ?, ?)`,
			gatewayType, externalKey, sessionID, now,
		); err != nil {
			return fmt.Errorf("GatewaySessionKeyRepo.CreateMapping(%s,%s): %w", gatewayType, externalKey, err)
		}
		if gatewayType != GatewayTypeScheduled {
			if _, err := tx.ExecContext(ctx,
				`UPDATE sessions SET external_gateway = 1 WHERE id = ?`,
				sessionID,
			); err != nil {
				return fmt.Errorf("GatewaySessionKeyRepo.CreateMapping(%s,%s): stamp external_gateway: %w", gatewayType, externalKey, err)
			}
		}
		return nil
	})
}

func (r *GatewaySessionKeyRepo) CreateSessionWithMapping(ctx context.Context, sess *Session, gatewayType, externalKey string) error {
	now := time.Now().UnixMilli()
	if sess.CreatedTime == 0 {
		sess.CreatedTime = now
	}
	if sess.UpdatedTime == 0 {
		sess.UpdatedTime = now
	}
	sess.HasActivity = sess.HasActivity || isSchedulerSource(sess.Source)
	sess.ExternalGateway = gatewayType != GatewayTypeScheduled
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, name, user_id, agent_id, project_id, created_time, updated_time, source, has_activity, external_gateway)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.ID, sess.Name, sess.UserID, sess.AgentID, sess.ProjectID,
			sess.CreatedTime, sess.UpdatedTime, sess.Source, dbutil.BoolToInt(sess.HasActivity),
			dbutil.BoolToInt(sess.ExternalGateway),
		); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
			 VALUES (?, ?, ?, ?)`,
			gatewayType, externalKey, sess.ID, now,
		); err != nil {
			return fmt.Errorf("insert mapping: %w", err)
		}
		return nil
	})
}

func (r *GatewaySessionKeyRepo) GetByGatewayAndSessionIDs(ctx context.Context, gatewayType string, sessionIDs []string) (map[string]string, error) {
	if len(sessionIDs) == 0 {
		return map[string]string{}, nil
	}
	placeholders, args, err := dbutil.InClause(sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndSessionIDs in clause: %w", err)
	}
	query := `SELECT session_id, external_key FROM gateway_session_keys
		 WHERE gateway_type = ? AND session_id IN (` + placeholders + `)`
	queryArgs := append([]any{gatewayType}, args...)

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndSessionIDs query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string, len(sessionIDs))
	for rows.Next() {
		var sid, ext string
		if err := rows.Scan(&sid, &ext); err != nil {
			return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndSessionIDs scan: %w", err)
		}
		out[sid] = ext
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndSessionIDs rows: %w", err)
	}
	return out, nil
}

func (r *GatewaySessionKeyRepo) GetByGatewayAndExternalKeys(ctx context.Context, gatewayType string, externalKeys []string) (map[string]string, error) {
	if len(externalKeys) == 0 {
		return map[string]string{}, nil
	}
	placeholders, args, err := dbutil.InClause(externalKeys)
	if err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndExternalKeys in clause: %w", err)
	}
	query := `SELECT external_key, session_id FROM gateway_session_keys
		 WHERE gateway_type = ? AND external_key IN (` + placeholders + `)`
	queryArgs := append([]any{gatewayType}, args...)

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndExternalKeys query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string, len(externalKeys))
	for rows.Next() {
		var ext, sid string
		if err := rows.Scan(&ext, &sid); err != nil {
			return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndExternalKeys scan: %w", err)
		}
		out[ext] = sid
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GatewaySessionKeyRepo.GetByGatewayAndExternalKeys rows: %w", err)
	}
	return out, nil
}

func lookupMappingTx(ctx context.Context, tx *dbutil.Tx, gatewayType, externalKey string) (string, error) {
	var sid string
	err := tx.QueryRowContext(ctx,
		`SELECT session_id FROM gateway_session_keys
		 WHERE gateway_type = ? AND external_key = ?`,
		gatewayType, externalKey,
	).Scan(&sid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup mapping: %w", err)
	}
	return sid, nil
}

func sessionOwnerTx(ctx context.Context, tx *dbutil.Tx, sessionID string) (alive bool, owner string, err error) {
	var userID sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE id = ? AND deleted_at IS NULL`,
		sessionID,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("session alive check: %w", err)
	}
	return true, userID.String, nil
}
