// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type SpendLedgerEntry struct {
	ID                  string
	CompletedTime       int64
	UserID              *string
	UserSub             *string
	AgentID             *string
	AgentName           *string
	WorkflowID          *string
	WorkflowName        *string
	RootKind            *string
	RootID              *string
	RootName            *string
	RootNodeID          *string
	EntrypointID        *string
	EntrypointName      *string
	Channel             *string
	TriggerKind         *string
	TriggerID           *string
	Surface             string
	ModelConfigID       *string
	ModelName           *string
	TaskID              *string
	TraceID             *string
	UncachedInputTokens int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	Cost                *spend.Cost
	AvoidedInputTokens  *int64
	AvoidedInputCost    *int64
}

const spendLedgerColumns = `id, completed_time, user_id, user_sub, agent_id, agent_name,
	workflow_id, workflow_name, root_kind, root_id, root_name, root_node_id, entrypoint_id, entrypoint_name,
	channel, trigger_kind, trigger_id, surface, model_config_id, model_name, task_id, trace_id,
	uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	uncached_input_cost, output_cost, cache_read_cost, cache_write_cost,
	avoided_input_tokens, avoided_input_cost`

const spendLedgerColumnCount = 32

type SpendLedgerDetail struct {
	ID                     string
	ModelKey               *string
	SentModelName          *string
	ToolName               *string
	WorkflowNodeID         *string
	ParentTaskID           *string
	SessionID              *string
	ResolvedModelName      *string
	ResolvedModelProvider  *string
	ResolvedAgentName      *string
	ResolvedWorkflowName   *string
	ResolvedRootName       *string
	ResolvedEntrypointName *string
	AttemptCount           *int
	CacheWritesReported    *bool
}

const spendLedgerDetailColumns = `id, model_key, sent_model_name, tool_name, workflow_node_id,
	parent_task_id, session_id, resolved_model_name, resolved_model_provider,
	resolved_agent_name, resolved_workflow_name, resolved_root_name, resolved_entrypoint_name, attempt_count,
	cache_writes_reported`

const spendLedgerDetailColumnCount = 15

const MaxSpendLedgerBatch = 500

type SpendLedgerRepo struct {
	db *DB
}

func NewSpendLedgerRepo(db *DB) *SpendLedgerRepo {
	return &SpendLedgerRepo{db: db}
}

func (r *SpendLedgerRepo) Insert(ctx context.Context, entries []SpendLedgerEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	if len(entries) > MaxSpendLedgerBatch {
		return 0, fmt.Errorf("SpendLedgerRepo.Insert: %d entries exceeds the batch limit of %d", len(entries), MaxSpendLedgerBatch)
	}
	args, err := ledgerInsertArgs(entries)
	if err != nil {
		return 0, err
	}
	var filed int64
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		ids, insertErr := insertLedgerRows(ctx, tx, len(entries), args)
		if insertErr != nil {
			return insertErr
		}
		filed = int64(len(ids))
		added := make([]*SpendLedgerEntry, 0, len(ids))
		for i := range entries {
			if ids[entries[i].ID] {
				added = append(added, &entries[i])
				delete(ids, entries[i].ID)
			}
		}
		return addToBudgetPeriods(ctx, tx, added)
	})
	if err != nil {
		return 0, fmt.Errorf("SpendLedgerRepo.Insert: %w", err)
	}
	return filed, nil
}

func ledgerInsertArgs(entries []SpendLedgerEntry) ([]any, error) {
	args := make([]any, 0, len(entries)*spendLedgerColumnCount)
	for i := range entries {
		e := &entries[i]
		if (e.UserID == nil) == (e.UserSub == nil) {
			return nil, fmt.Errorf("SpendLedgerRepo.Insert: entry %s does not have exactly one of user_id and user_sub", e.ID)
		}
		if (e.ModelConfigID == nil) == (e.ModelName == nil) {
			return nil, fmt.Errorf("SpendLedgerRepo.Insert: entry %s does not have exactly one of model_config_id and model_name", e.ID)
		}
		args = append(args,
			e.ID, e.CompletedTime, e.UserID, e.UserSub, e.AgentID, e.AgentName,
			e.WorkflowID, e.WorkflowName, e.RootKind, e.RootID, e.RootName, e.RootNodeID, e.EntrypointID, e.EntrypointName,
			e.Channel, e.TriggerKind, e.TriggerID, e.Surface, e.ModelConfigID, e.ModelName, e.TaskID, e.TraceID,
			e.UncachedInputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens,
		)
		args = appendCostArgs(args, e.Cost)
		args = append(args, e.AvoidedInputTokens, e.AvoidedInputCost)
	}
	return args, nil
}

func insertLedgerRows(ctx context.Context, tx *dbutil.Tx, rowCount int, args []any) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx,
		`INSERT INTO token_spend_ledger (`+spendLedgerColumns+`) VALUES `+
			valuesRows(spendLedgerColumnCount, rowCount)+` ON CONFLICT (id) DO NOTHING RETURNING CAST(id AS TEXT)`,
		args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make(map[string]bool, rowCount)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan filed id: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func valuesRows(columnCount, rowCount int) string {
	row := "(" + strings.TrimSuffix(strings.Repeat("?, ", columnCount), ", ") + ")"
	return strings.TrimSuffix(strings.Repeat(row+", ", rowCount), ", ")
}

func (r *SpendLedgerRepo) InsertDetails(ctx context.Context, details []SpendLedgerDetail) (int64, error) {
	if len(details) == 0 {
		return 0, nil
	}
	if len(details) > MaxSpendLedgerBatch {
		return 0, fmt.Errorf("SpendLedgerRepo.InsertDetails: %d details exceeds the batch limit of %d", len(details), MaxSpendLedgerBatch)
	}
	args := make([]any, 0, len(details)*spendLedgerDetailColumnCount)
	for i := range details {
		d := &details[i]
		args = append(args,
			d.ID, d.ModelKey, d.SentModelName, d.ToolName, d.WorkflowNodeID,
			d.ParentTaskID, d.SessionID, d.ResolvedModelName, d.ResolvedModelProvider,
			d.ResolvedAgentName, d.ResolvedWorkflowName, d.ResolvedRootName, d.ResolvedEntrypointName, d.AttemptCount,
			d.CacheWritesReported,
		)
	}
	return r.insertDetailRows(ctx, len(details), args)
}

func (r *SpendLedgerRepo) insertDetailRows(ctx context.Context, rowCount int, args []any) (int64, error) {
	const op = "SpendLedgerRepo.InsertDetails"
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO token_spend_ledger_details (`+spendLedgerDetailColumns+`) VALUES `+
			valuesRows(spendLedgerDetailColumnCount, rowCount)+` ON CONFLICT (id) DO NOTHING`,
		args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", op, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s: rows affected: %w", op, err)
	}
	return n, nil
}

type ComponentTable string

const (
	ComponentAgents    ComponentTable = "agents"
	ComponentWorkflows ComponentTable = "workflows"
	ComponentGateways  ComponentTable = "gateways"
)

func (r *SpendLedgerRepo) ResolveNames(ctx context.Context, table ComponentTable, ids []string) (map[string]string, error) {
	switch table {
	case ComponentAgents, ComponentWorkflows, ComponentGateways:
	default:
		return nil, fmt.Errorf("SpendLedgerRepo.ResolveNames: unknown table %q", table)
	}
	names := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += MaxSpendLedgerBatch {
		chunk := ids[start:min(start+MaxSpendLedgerBatch, len(ids))]
		if err := r.resolveNameChunk(ctx, table, chunk, names); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func (r *SpendLedgerRepo) resolveNameChunk(ctx context.Context, table ComponentTable, ids []string, names map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	rows, err := r.db.QueryContext(ctx,
		`SELECT CAST(id AS TEXT), name FROM `+string(table)+` WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("SpendLedgerRepo.ResolveNames: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("SpendLedgerRepo.ResolveNames: scan: %w", err)
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("SpendLedgerRepo.ResolveNames: %w", err)
	}
	return nil
}

func appendCostArgs(args []any, c *spend.Cost) []any {
	if c == nil {
		return append(args, nil, nil, nil, nil)
	}
	return append(args, c.UncachedInput, c.Output, c.CacheRead, c.CacheWrite)
}

func (r *SpendLedgerRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, fmt.Errorf("SpendLedgerRepo.DeleteOlderThan: batch size %d is not positive", batchSize)
	}
	query := `DELETE FROM token_spend_ledger WHERE id IN (SELECT id FROM token_spend_ledger WHERE completed_time < ? LIMIT ?)`
	var total int64
	for {
		result, err := r.db.ExecContext(ctx, query, before.UnixMilli(), batchSize)
		if err != nil {
			return total, fmt.Errorf("SpendLedgerRepo.DeleteOlderThan: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("SpendLedgerRepo.DeleteOlderThan: rows affected: %w", err)
		}
		total += n
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

type TraceCost struct {
	TraceID  string `db:"trace_id"`
	Cost     int64  `db:"cost"`
	Unpriced int64  `db:"unpriced"`
	Tokens   int64  `db:"tokens"`
}

func (r *SpendLedgerRepo) CostByTrace(ctx context.Context, traceIDs []string, exclude []spend.Surface) (map[string]TraceCost, error) {
	unique := slices.Compact(slices.Sorted(slices.Values(traceIDs)))
	out := make(map[string]TraceCost, len(unique))
	for batch := range slices.Chunk(unique, dbutil.InClauseCap) {
		if err := r.costByTraceBatch(ctx, batch, exclude, out); err != nil {
			return nil, fmt.Errorf("SpendLedgerRepo.CostByTrace: %w", err)
		}
	}
	return out, nil
}

func (r *SpendLedgerRepo) costByTraceBatch(ctx context.Context, traceIDs []string, exclude []spend.Surface, out map[string]TraceCost) error {
	tracePH, args, err := dbutil.InClause(traceIDs)
	if err != nil {
		return err
	}
	where := "trace_id IN (" + tracePH + ")"
	if len(exclude) > 0 {
		surfaces := make([]string, len(exclude))
		for i, s := range exclude {
			surfaces[i] = string(s)
		}
		surfacePH, surfaceArgs, inErr := dbutil.InClause(surfaces)
		if inErr != nil {
			return inErr
		}
		where += " AND surface NOT IN (" + surfacePH + ")"
		args = append(args, surfaceArgs...)
	}
	rows, err := dbutil.QueryRows[TraceCost](ctx, r.db,
		`SELECT trace_id,
		        CAST(COALESCE(SUM(uncached_input_cost + output_cost + cache_read_cost + cache_write_cost), 0) AS BIGINT) AS cost,
		        CAST(SUM(CASE WHEN uncached_input_cost IS NULL THEN 1 ELSE 0 END) AS BIGINT) AS unpriced,
		        CAST(SUM(uncached_input_tokens + output_tokens + cache_read_tokens + cache_write_tokens) AS BIGINT) AS tokens
		 FROM token_spend_ledger WHERE `+where+` GROUP BY trace_id`, args...)
	if err != nil {
		return err
	}
	for _, row := range rows {
		out[row.TraceID] = row
	}
	return nil
}
