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

const evalExampleArtifactColumns = `id, example_id, role, filename, mime_type, size_bytes,
	version, artifact_session_id, content_hash, created_time, updated_time`

type EvalExampleArtifactRepo struct {
	db *DB
}

func NewEvalExampleArtifactRepo(db *DB) *EvalExampleArtifactRepo {
	return &EvalExampleArtifactRepo{db: db}
}

func (r *EvalExampleArtifactRepo) Upsert(ctx context.Context, a *EvalExampleArtifact, files EvalFileDeleteEvent) error {
	if a.ID == "" {
		a.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if a.CreatedTime == 0 {
		a.CreatedTime = now
	}
	a.UpdatedTime = now
	wrap := func(err error) error {
		return fmt.Errorf("EvalExampleArtifactRepo.Upsert(%s, %s): %w", a.ExampleID, a.Role, err)
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE eval_examples SET updated_time = ? WHERE id = ?`, now, a.ExampleID)
		if err != nil {
			return wrap(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrEvalNotFound
		}
		if err = recordReplacedFile(ctx, tx, a, files); err != nil {
			return wrap(err)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO eval_example_artifacts (`+evalExampleArtifactColumns+`)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (example_id, role) DO UPDATE SET
			   filename = excluded.filename, mime_type = excluded.mime_type,
			   size_bytes = excluded.size_bytes, version = excluded.version,
			   artifact_session_id = excluded.artifact_session_id,
			   content_hash = excluded.content_hash, updated_time = excluded.updated_time`,
			a.ID, a.ExampleID, a.Role, a.Filename, a.MIMEType, a.SizeBytes,
			a.Version, a.SessionID, a.ContentHash, a.CreatedTime, a.UpdatedTime)
		if err != nil {
			return wrap(err)
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT id, created_time FROM eval_example_artifacts WHERE example_id = ? AND role = ?`,
			a.ExampleID, a.Role).Scan(&a.ID, &a.CreatedTime); err != nil {
			return wrap(err)
		}
		return nil
	})
}

func (r *EvalExampleArtifactRepo) Delete(ctx context.Context, exampleID, role string, files EvalFileDeleteEvent) error {
	wrap := func(err error) error {
		return fmt.Errorf("EvalExampleArtifactRepo.Delete(%s, %s): %w", exampleID, role, err)
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM eval_examples WHERE id = ?`, exampleID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEvalNotFound
			}
			return wrap(err)
		}
		removed, err := dbutil.QueryRows[EvalExampleFile](ctx, tx,
			`SELECT artifact_session_id, filename FROM eval_example_artifacts WHERE example_id = ? AND role = ?`, exampleID, role)
		if err != nil {
			return wrap(err)
		}
		if err = writeFileDeleteEvent(ctx, tx, EvalFileDeletes{ExampleFiles: removed}, files); err != nil {
			return wrap(err)
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM eval_example_artifacts WHERE example_id = ? AND role = ?`, exampleID, role)
		if err != nil {
			return wrap(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE eval_examples SET updated_time = ? WHERE id = ?`,
			time.Now().UnixMilli(), exampleID); err != nil {
			return wrap(err)
		}
		return nil
	})
}

func (r *EvalExampleArtifactRepo) SumBytesByDataset(ctx context.Context, datasetID string) (int64, error) {
	return dbutil.QueryVal[int64](ctx, r.db,
		`SELECT COALESCE(SUM(a.size_bytes), 0) FROM eval_example_artifacts a
		 JOIN eval_examples e ON e.id = a.example_id WHERE e.dataset_id = ?`, datasetID)
}

func recordReplacedFile(ctx context.Context, tx *dbutil.Tx, a *EvalExampleArtifact, files EvalFileDeleteEvent) error {
	if files == nil {
		return nil
	}
	current, err := dbutil.QueryRows[EvalExampleFile](ctx, tx,
		`SELECT artifact_session_id, filename FROM eval_example_artifacts WHERE example_id = ? AND role = ?`, a.ExampleID, a.Role)
	if err != nil {
		return err
	}
	var replaced []EvalExampleFile
	for _, f := range current {
		if f.SessionID != a.SessionID || f.Filename != a.Filename {
			replaced = append(replaced, f)
		}
	}
	return writeFileDeleteEvent(ctx, tx, EvalFileDeletes{ExampleFiles: replaced}, files)
}

func (r *EvalExampleArtifactRepo) SessionReferenced(ctx context.Context, sessionID string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_example_artifacts WHERE artifact_session_id = ?`, sessionID)
	if err != nil {
		return false, fmt.Errorf("EvalExampleArtifactRepo.SessionReferenced: %w", err)
	}
	return n > 0, nil
}

func (r *EvalExampleArtifactRepo) RecordFiles(ctx context.Context, locations []EvalExampleFile, files EvalFileDeleteEvent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return writeFileDeleteEvent(ctx, tx, EvalFileDeletes{ExampleFiles: locations}, files)
	})
}
