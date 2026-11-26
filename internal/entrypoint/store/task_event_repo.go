// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/tracededup"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const taskEventColumns = "id, task_id, user_id, created_time, topic, direction, payload, is_delta, signal_type, agent_name, tool_name, event_seq, publisher_id, config_prompt_blob, config_tools_blob, forwarded_from, trace_id"

const taskEventInsertColumns = taskEventColumns + ", commit_xid"

func taskEventValuesTuple(dialect string) string {
	if dialect == "postgres" {
		return "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, pg_current_xact_id())"
	}
	return "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)"
}

const taskEventParamsPerRow = 17

type TaskEventRepo struct {
	db              *DB
	blobs           *TaskEventBlobRepo
	unresolvedReads atomic.Uint64
}

func NewTaskEventRepo(db *DB) *TaskEventRepo {
	return &TaskEventRepo{db: db, blobs: NewTaskEventBlobRepo(db)}
}

func (r *TaskEventRepo) Blobs() *TaskEventBlobRepo {
	return r.blobs
}

func taskEventArgs(e *TaskEvent) []any {
	return []any{e.ID, e.TaskID, e.UserID, e.CreatedTime, e.Topic, e.Direction, e.Payload, dbutil.BoolToInt(e.IsDelta),
		e.SignalType, e.AgentName, e.ToolName, e.EventSeq, e.PublisherID, e.ConfigPromptBlob, e.ConfigToolsBlob, e.ForwardedFrom, e.TraceID}
}

func (r *TaskEventRepo) Create(ctx context.Context, e *TaskEvent) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO task_events (`+taskEventInsertColumns+`) VALUES `+taskEventValuesTuple(r.db.Dialect()),
		taskEventArgs(e)...,
	)
	if err != nil {
		return fmt.Errorf("TaskEventRepo.Create(%s): %w", e.ID, err)
	}
	return nil
}

func (r *TaskEventRepo) CreateBatch(ctx context.Context, events []*TaskEvent) error {
	if len(events) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO task_events (" + taskEventInsertColumns + ") VALUES ")
	tuple := taskEventValuesTuple(r.db.Dialect())
	args := make([]any, 0, len(events)*taskEventParamsPerRow)
	for i, e := range events {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(tuple)
		args = append(args, taskEventArgs(e)...)
	}
	if _, err := r.db.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("TaskEventRepo.CreateBatch(%d): %w", len(events), err)
	}
	return nil
}

func (r *TaskEventRepo) GetByTaskID(ctx context.Context, taskID string) ([]*TaskEvent, error) {
	events, err := dbutil.QueryRowPtrs[TaskEvent](ctx, r.db,
		`SELECT `+taskEventColumns+`
		 FROM task_events WHERE task_id = ? ORDER BY created_time ASC, id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	if err := r.rehydrate(ctx, events); err != nil {
		return nil, fmt.Errorf("TaskEventRepo.GetByTaskID(%s): %w", taskID, err)
	}
	return events, nil
}

func (r *TaskEventRepo) GetByTaskIDs(ctx context.Context, taskIDs []string) ([]*TaskEvent, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}

	ph, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("TaskEventRepo.GetByTaskIDs: %w", err)
	}
	events, err := dbutil.QueryRowPtrs[TaskEvent](ctx, r.db,
		`SELECT `+taskEventColumns+`
		 FROM task_events WHERE task_id IN (`+ph+`) ORDER BY created_time ASC, id ASC`, args...)
	if err != nil {
		return nil, err
	}
	if err := r.rehydrate(ctx, events); err != nil {
		return nil, fmt.Errorf("TaskEventRepo.GetByTaskIDs: %w", err)
	}
	return events, nil
}

func (r *TaskEventRepo) rehydrate(ctx context.Context, events []*TaskEvent) error {
	var hashes []string
	seen := map[string]struct{}{}
	for _, e := range events {
		for _, h := range tracededup.Refs([]byte(e.Payload)) {
			if _, dup := seen[h]; !dup {
				seen[h] = struct{}{}
				hashes = append(hashes, h)
			}
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	contents, err := r.blobs.GetContentByHashes(ctx, hashes)
	if err != nil {
		return err
	}
	lookup := func(h string) (string, bool) { c, ok := contents[h]; return c, ok }
	for _, e := range events {
		out, err := tracededup.Rehydrate([]byte(e.Payload), lookup)
		if err != nil {
			return fmt.Errorf("rehydrate event %s: %w", e.ID, err)
		}
		e.Payload = string(out)
	}
	if unresolved := tasksWithUnresolvedRefs(events); len(unresolved) > 0 {
		runtime.MetricProvider().Counter("gateway.task_events.rehydrate_unresolved_tasks", int64(len(unresolved)))
		n := r.unresolvedReads.Add(1)
		if logger := r.db.Logger(); logger != nil && (n == 1 || n%100 == 0) {
			logger.Warn("task event blob references unresolved on rehydrate",
				"tasks", len(unresolved), "taskIDs", unresolved[:min(len(unresolved), loggedTaskIDsCap)],
				"missingBlobs", len(hashes)-len(contents), "totalReads", n)
		}
	}
	return nil
}

const loggedTaskIDsCap = 20

func (r *TaskEventRepo) GetTreeEventsByTaskIDs(ctx context.Context, taskIDs []string, perTaskCap int) ([]*TaskEvent, map[string]int, error) {
	if len(taskIDs) == 0 {
		return nil, map[string]int{}, nil
	}
	if perTaskCap < 1 {
		perTaskCap = 30
	}
	ph, idArgs, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("TaskEventRepo.GetTreeEventsByTaskIDs: %w", err)
	}

	countRows, err := dbutil.QueryRowPtrs[taskEventCount](ctx, r.db,
		`SELECT task_id, COUNT(*) AS count FROM task_events
		 WHERE task_id IN (`+ph+`) AND is_delta = 0 GROUP BY task_id`, idArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("TaskEventRepo.GetTreeEventsByTaskIDs counts: %w", err)
	}
	counts := make(map[string]int, len(countRows))
	for _, row := range countRows {
		counts[row.TaskID] = row.Count
	}

	args := append(append([]any{}, idArgs...), perTaskCap, perTaskCap, 2*perTaskCap*len(taskIDs))
	events, err := dbutil.QueryRowPtrs[TaskEvent](ctx, r.db,
		`SELECT task_id, created_time, direction, payload FROM (
			SELECT id, task_id, created_time, direction, payload,
				ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY created_time ASC, id ASC) AS rn_head,
				ROW_NUMBER() OVER (PARTITION BY task_id ORDER BY created_time DESC, id DESC) AS rn_tail
			FROM task_events
			WHERE task_id IN (`+ph+`) AND is_delta = 0
		) ranked
		WHERE rn_head <= ? OR rn_tail <= ?
		ORDER BY task_id, created_time ASC, id ASC
		LIMIT ?`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("TaskEventRepo.GetTreeEventsByTaskIDs: %w", err)
	}
	return events, counts, nil
}

type taskEventCount struct {
	TaskID string `db:"task_id"`
	Count  int    `db:"count"`
}

func (r *TaskEventRepo) CountByTaskIDs(ctx context.Context, taskIDs []string) (map[string]int, error) {
	if len(taskIDs) == 0 {
		return map[string]int{}, nil
	}

	ph, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("TaskEventRepo.CountByTaskIDs: %w", err)
	}
	rows, err := dbutil.QueryRowPtrs[taskEventCount](ctx, r.db,
		`SELECT task_id, COUNT(*) AS count
		 FROM task_events WHERE task_id IN (`+ph+`) GROUP BY task_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("TaskEventRepo.CountByTaskIDs: %w", err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.TaskID] = row.Count
	}
	return counts, nil
}

func (r *TaskEventRepo) DeleteDeltasOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	beforeMs := before.UnixMilli()
	var total int64
	query := `DELETE FROM task_events WHERE id IN (
				SELECT id FROM task_events WHERE is_delta = 1 AND created_time < ? LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("TaskEventRepo.DeleteDeltasOlderThan: %w", err)
		}
		n, _ := result.RowsAffected()
		total += n
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

func tasksWithUnresolvedRefs(events []*TaskEvent) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, 1)
	for _, e := range events {
		if _, dup := seen[e.TaskID]; dup || !tracededup.HasRefs([]byte(e.Payload)) {
			continue
		}
		seen[e.TaskID] = struct{}{}
		ids = append(ids, e.TaskID)
	}
	return ids
}

const forwardedFromPeerKey = "forwarded_from_peer"

func (r *TaskEventRepo) ForwardedPeerNames(ctx context.Context, taskIDs []string) ([]string, error) {
	byTask, err := r.ForwardedPeersByTask(ctx, taskIDs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, names := range byTask {
		for _, name := range names {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out, nil
}

type forwardedPeerRow struct {
	TaskID  string `db:"task_id"`
	Payload string `db:"payload"`
}

func (r *TaskEventRepo) ForwardedPeersByTask(ctx context.Context, taskIDs []string) (map[string][]string, error) {
	seen := make(map[string]map[string]bool)
	for chunk := range slices.Chunk(taskIDs, dbutil.InClauseCap) {
		ph, args, err := dbutil.InClause(chunk)
		if err != nil {
			return nil, fmt.Errorf("TaskEventRepo.ForwardedPeersByTask: %w", err)
		}
		args = append(args, "%"+forwardedFromPeerKey+"%")
		rows, err := dbutil.QueryRowPtrs[forwardedPeerRow](ctx, r.db,
			`SELECT task_id, payload FROM task_events WHERE task_id IN (`+ph+`) AND payload LIKE ?`, args...)
		if err != nil {
			return nil, fmt.Errorf("TaskEventRepo.ForwardedPeersByTask: %w", err)
		}
		for _, row := range rows {
			if seen[row.TaskID] == nil {
				seen[row.TaskID] = make(map[string]bool)
			}
			var decoded any
			if err := json.Unmarshal([]byte(row.Payload), &decoded); err != nil {
				seen[row.TaskID][""] = true
				continue
			}
			collectForwardedPeers(decoded, seen[row.TaskID])
		}
	}
	out := make(map[string][]string, len(seen))
	for id, names := range seen {
		for name := range names {
			out[id] = append(out[id], name)
		}
	}
	return out, nil
}

func collectForwardedPeers(node any, seen map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == forwardedFromPeerKey {
				name, ok := child.(string)
				if !ok {
					seen[""] = true
					continue
				}
				seen[name] = true
				continue
			}
			collectForwardedPeers(child, seen)
		}
	case []any:
		for _, child := range v {
			collectForwardedPeers(child, seen)
		}
	}
}
