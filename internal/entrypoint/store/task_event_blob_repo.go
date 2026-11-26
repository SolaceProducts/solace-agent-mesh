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

type TaskEventBlob struct {
	Hash      string `db:"hash"`
	Kind      string `db:"kind"`
	Content   string `db:"content"`
	ByteLen   int    `db:"byte_len"`
	FirstSeen int64  `db:"first_seen"`
	LastSeen  int64  `db:"last_seen"`
}

type TaskEventBlobRepo struct {
	db *DB
}

func NewTaskEventBlobRepo(db *DB) *TaskEventBlobRepo {
	return &TaskEventBlobRepo{db: db}
}

const blobUpsertBatch = 200

func (r *TaskEventBlobRepo) UpsertBatch(ctx context.Context, blobs []*TaskEventBlob) error {
	for start := 0; start < len(blobs); start += blobUpsertBatch {
		end := min(start+blobUpsertBatch, len(blobs))
		if err := r.upsertChunk(ctx, blobs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *TaskEventBlobRepo) upsertChunk(ctx context.Context, blobs []*TaskEventBlob) error {
	if len(blobs) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO task_event_blobs (hash, kind, content, byte_len, first_seen, last_seen) VALUES ")
	args := make([]any, 0, len(blobs)*6)
	for i, b := range blobs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?, ?, ?, ?)")
		args = append(args, b.Hash, b.Kind, b.Content, b.ByteLen, b.FirstSeen, b.LastSeen)
	}
	query := dbutil.UpsertSQL(r.db.Dialect(), sb.String(), "hash", []string{"last_seen"})
	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("TaskEventBlobRepo.UpsertBatch(%d): %w", len(blobs), err)
	}
	return nil
}

var blobReferenceIndexes = []string{"idx_task_events_config_prompt_blob", "idx_task_events_config_tools_blob"}

func (r *TaskEventBlobRepo) ReferenceIndexesReady(ctx context.Context) (bool, error) {
	if r.db.Dialect() != "postgres" {
		return true, nil
	}
	for _, name := range blobReferenceIndexes {
		exists, valid, err := dbutil.PostgresIndexState(ctx, r.db.RawDB(), name)
		if err != nil || !exists || !valid {
			return false, err
		}
	}
	return true, nil
}

func (r *TaskEventBlobRepo) DeleteUnreferencedOlderThan(ctx context.Context, lastSeenBefore time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := lastSeenBefore.UnixMilli()
	var total int64
	query := `DELETE FROM task_event_blobs WHERE last_seen < ? AND hash IN (
				SELECT b.hash FROM task_event_blobs b
				WHERE b.last_seen < ?
				  AND NOT EXISTS (SELECT 1 FROM task_events e WHERE e.config_prompt_blob = b.hash)
				  AND NOT EXISTS (SELECT 1 FROM task_events e WHERE e.config_tools_blob = b.hash)
				LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query, beforeMs, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("TaskEventBlobRepo.DeleteUnreferencedOlderThan: %w", err)
		}
		n, _ := result.RowsAffected()
		total += n
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

func (r *TaskEventBlobRepo) GetContentByHashes(ctx context.Context, hashes []string) (map[string]string, error) {
	out := make(map[string]string, len(hashes))
	for start := 0; start < len(hashes); start += dbutil.InClauseCap {
		end := min(start+dbutil.InClauseCap, len(hashes))
		ph, args, err := dbutil.InClause(hashes[start:end])
		if err != nil {
			return nil, fmt.Errorf("TaskEventBlobRepo.GetContentByHashes: %w", err)
		}
		rows, err := dbutil.QueryRowPtrs[TaskEventBlob](ctx, r.db,
			`SELECT hash, kind, content, byte_len, first_seen, last_seen
			 FROM task_event_blobs WHERE hash IN (`+ph+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("TaskEventBlobRepo.GetContentByHashes: %w", err)
		}
		for _, b := range rows {
			out[b.Hash] = b.Content
		}
	}
	return out, nil
}

func (r *TaskEventBlobRepo) Count(ctx context.Context) (int64, int64, error) {
	type agg struct {
		N     int64 `db:"n"`
		Bytes int64 `db:"bytes"`
	}
	row, err := dbutil.QueryRow[agg](ctx, r.db,
		`SELECT COUNT(*) AS n, COALESCE(SUM(byte_len), 0) AS bytes FROM task_event_blobs`)
	if err != nil {
		return 0, 0, fmt.Errorf("TaskEventBlobRepo.Count: %w", err)
	}
	return row.N, row.Bytes, nil
}
