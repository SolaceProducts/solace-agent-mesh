// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const waitingDeployRequestsBatch = 100

type DeployRequest struct {
	ID          string  `db:"id"`
	Kind        string  `db:"kind"`
	ObjectID    string  `db:"object_id"`
	Op          string  `db:"op"`
	Args        string  `db:"args"`
	CreatedTime int64   `db:"created_time"`
	TakenCount  int     `db:"taken"`
	DoneTime    *int64  `db:"done_time"`
	Answer      *string `db:"answer"`
}

func (r *DeployRequest) Taken() bool { return r.TakenCount > 0 }

func (r *DeployRequest) Done() bool { return r.DoneTime != nil }

const deployRequestCols = `id, kind, object_id, op, args, created_time, taken, done_time, answer`

type DeployRequestRepo struct {
	db *DB
}

func NewDeployRequestRepo(db *DB) *DeployRequestRepo {
	return &DeployRequestRepo{db: db}
}

func (r *DeployRequestRepo) nowMillis() string {
	if r.db.Dialect() == "postgres" {
		return `(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::BIGINT`
	}
	return `CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`
}

func (r *DeployRequestRepo) Add(ctx context.Context, id, kind, objectID, op string, args []byte) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO deployment_requests (id, kind, object_id, op, args, created_time) VALUES (?, ?, ?, ?, ?, `+r.nowMillis()+`)`,
		id, kind, objectID, op, string(args),
	); err != nil {
		return fmt.Errorf("DeployRequestRepo.Add(%s): %w", id, err)
	}
	return nil
}

func (r *DeployRequestRepo) Get(ctx context.Context, id string) (*DeployRequest, error) {
	req, err := dbutil.QueryRow[DeployRequest](ctx, r.db,
		`SELECT `+deployRequestCols+` FROM deployment_requests WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("DeployRequestRepo.Get(%s): %w", id, err)
	}
	return req, nil
}

func (r *DeployRequestRepo) AnyWaiting(ctx context.Context, window time.Duration) (bool, error) {
	waiting, err := dbutil.QueryVal[bool](ctx, r.db,
		`SELECT EXISTS (SELECT 1 FROM deployment_requests WHERE done_time IS NULL AND created_time > `+r.nowMillis()+` - ?)`,
		window.Milliseconds())
	if err != nil {
		return false, fmt.Errorf("DeployRequestRepo.AnyWaiting: %w", err)
	}
	return waiting, nil
}

func (r *DeployRequestRepo) Waiting(ctx context.Context, kind string, window time.Duration) ([]*DeployRequest, error) {
	reqs, err := dbutil.QueryRowPtrs[DeployRequest](ctx, r.db,
		`SELECT `+deployRequestCols+` FROM deployment_requests
		 WHERE kind = ? AND done_time IS NULL AND created_time > `+r.nowMillis()+` - ?
		 ORDER BY created_time, id LIMIT ?`,
		kind, window.Milliseconds(), waitingDeployRequestsBatch)
	if err != nil {
		return nil, fmt.Errorf("DeployRequestRepo.Waiting(%s): %w", kind, err)
	}
	return reqs, nil
}

func (r *DeployRequestRepo) Take(ctx context.Context, id string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE deployment_requests SET taken = taken + 1 WHERE id = ? AND done_time IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("DeployRequestRepo.Take(%s): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("DeployRequestRepo.Take(%s): %w", id, err)
	}
	return n == 1, nil
}

func (r *DeployRequestRepo) Answer(ctx context.Context, req *DeployRequest, answer string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE deployment_requests SET answer = ?, done_time = `+r.nowMillis()+` WHERE id = ? AND done_time IS NULL`,
		answer, req.ID,
	); err != nil {
		return fmt.Errorf("DeployRequestRepo.Answer(%s): %w", req.ID, err)
	}
	return nil
}

func (r *DeployRequestRepo) Withdraw(ctx context.Context, id string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM deployment_requests WHERE id = ? AND taken = 0 AND done_time IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("DeployRequestRepo.Withdraw(%s): %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("DeployRequestRepo.Withdraw(%s): %w", id, err)
	}
	return n == 1, nil
}

func (r *DeployRequestRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM deployment_requests WHERE id = ?`, id); err != nil {
		return fmt.Errorf("DeployRequestRepo.Delete(%s): %w", id, err)
	}
	return nil
}

func (r *DeployRequestRepo) DeleteOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM deployment_requests WHERE created_time < `+r.nowMillis()+` - ?`, age.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("DeployRequestRepo.DeleteOlderThan: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("DeployRequestRepo.DeleteOlderThan: %w", err)
	}
	return n, nil
}
