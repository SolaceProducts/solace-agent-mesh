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
)

const (
	IntentKindAgent      = "agent"
	IntentKindWorkflow   = "workflow"
	IntentKindEntrypoint = "entrypoint"
)

type DeploymentIntent struct {
	ID          string `db:"id"`
	Kind        string `db:"kind"`
	ObjectID    string `db:"object_id"`
	Payload     string `db:"payload"`
	CreatedTime int64  `db:"created_time"`
}

type DeploymentIntentRepo struct {
	db *DB
}

func NewDeploymentIntentRepo(db *DB) *DeploymentIntentRepo {
	return &DeploymentIntentRepo{db: db}
}

func (r *DeploymentIntentRepo) Put(ctx context.Context, intent *DeploymentIntent) error {
	if intent.CreatedTime == 0 {
		intent.CreatedTime = time.Now().UnixMilli()
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO deployment_intents (id, kind, object_id, payload, created_time) VALUES (?, ?, ?, ?, ?)`,
		intent.ID, intent.Kind, intent.ObjectID, intent.Payload, intent.CreatedTime,
	); err != nil {
		return fmt.Errorf("DeploymentIntentRepo.Put(%s): %w", intent.ID, err)
	}
	return nil
}

func (r *DeploymentIntentRepo) PutReplacingOthers(ctx context.Context, intent *DeploymentIntent) error {
	if intent.CreatedTime == 0 {
		intent.CreatedTime = time.Now().UnixMilli()
	}
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if tx.Dialect() == "postgres" {
			if _, err := tx.ExecContext(ctx, `SELECT id FROM deployment_intents WHERE kind = ? AND object_id = ? FOR UPDATE`, intent.Kind, intent.ObjectID); err != nil {
				return fmt.Errorf("lock the object's deployments in flight: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO deployment_intents (id, kind, object_id, payload, created_time) VALUES (?, ?, ?, ?, ?)`,
			intent.ID, intent.Kind, intent.ObjectID, intent.Payload, intent.CreatedTime,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM deployment_intents WHERE kind = ? AND object_id = ? AND id <> ?`, intent.Kind, intent.ObjectID, intent.ID,
		); err != nil {
			return fmt.Errorf("replace older deployments in flight: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("DeploymentIntentRepo.PutReplacingOthers(%s): %w", intent.ID, err)
	}
	return nil
}

func (r *DeploymentIntentRepo) WhileInFlight(ctx context.Context, id string, fn func() error) (bool, error) {
	ran := false
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		lock := `SELECT id FROM deployment_intents WHERE id = ?`
		if tx.Dialect() == "postgres" {
			lock += ` FOR UPDATE`
		}
		var locked string
		if err := tx.QueryRowContext(ctx, lock, id).Scan(&locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock the deployment in flight: %w", err)
		}
		ran = true
		return fn()
	})
	if err != nil {
		return ran, fmt.Errorf("DeploymentIntentRepo.WhileInFlight(%s): %w", id, err)
	}
	return ran, nil
}

func (r *DeploymentIntentRepo) Recorded(ctx context.Context, kind, id string) (bool, error) {
	var query string
	switch kind {
	case IntentKindAgent:
		query = `SELECT COUNT(*) FROM agent_deployments WHERE id = ?`
	case IntentKindWorkflow:
		query = `SELECT COUNT(*) FROM workflow_deployments WHERE id = ?`
	case IntentKindEntrypoint:
		query = `SELECT COUNT(*) FROM gateway_deployments WHERE id = ?`
	default:
		return false, fmt.Errorf("DeploymentIntentRepo.Recorded: unknown kind %q", kind)
	}
	var n int
	if err := r.db.QueryRowContext(ctx, query, id).Scan(&n); err != nil {
		return false, fmt.Errorf("DeploymentIntentRepo.Recorded(%s, %s): %w", kind, id, err)
	}
	return n > 0, nil
}

func (r *DeploymentIntentRepo) InFlight(ctx context.Context, id string) (bool, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployment_intents WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("DeploymentIntentRepo.InFlight(%s): %w", id, err)
	}
	return n > 0, nil
}

func (r *DeploymentIntentRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM deployment_intents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("DeploymentIntentRepo.Delete(%s): %w", id, err)
	}
	return nil
}

func (r *DeploymentIntentRepo) List(ctx context.Context, kind string) ([]*DeploymentIntent, error) {
	intents, err := dbutil.QueryRowPtrs[DeploymentIntent](ctx, r.db,
		`SELECT id, kind, object_id, payload, created_time FROM deployment_intents WHERE kind = ? ORDER BY created_time, id`, kind)
	if err != nil {
		return nil, fmt.Errorf("DeploymentIntentRepo.List(%s): %w", kind, err)
	}
	return intents, nil
}

func (r *DeploymentIntentRepo) Newest(ctx context.Context, kind string) (map[string]*DeploymentIntent, error) {
	intents, err := r.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	newest := make(map[string]*DeploymentIntent, len(intents))
	for _, intent := range intents {
		newest[intent.ObjectID] = intent
	}
	return newest, nil
}

func (r *DeploymentIntentRepo) For(ctx context.Context, kind, objectID string) (*DeploymentIntent, error) {
	intent, err := dbutil.QueryRow[DeploymentIntent](ctx, r.db,
		`SELECT id, kind, object_id, payload, created_time FROM deployment_intents
		 WHERE kind = ? AND object_id = ? ORDER BY created_time DESC, id DESC LIMIT 1`, kind, objectID)
	if err != nil {
		return nil, fmt.Errorf("DeploymentIntentRepo.For(%s, %s): %w", kind, objectID, err)
	}
	return intent, nil
}
