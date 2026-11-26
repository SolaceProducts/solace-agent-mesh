// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type spendLedgerRow struct {
	ID                  string  `db:"id"`
	CompletedTime       int64   `db:"completed_time"`
	UserID              *string `db:"user_id"`
	UserSub             *string `db:"user_sub"`
	AgentID             *string `db:"agent_id"`
	AgentName           *string `db:"agent_name"`
	WorkflowID          *string `db:"workflow_id"`
	WorkflowName        *string `db:"workflow_name"`
	RootKind            *string `db:"root_kind"`
	RootID              *string `db:"root_id"`
	RootName            *string `db:"root_name"`
	RootNodeID          *string `db:"root_node_id"`
	EntrypointID        *string `db:"entrypoint_id"`
	EntrypointName      *string `db:"entrypoint_name"`
	Channel             *string `db:"channel"`
	TriggerKind         *string `db:"trigger_kind"`
	TriggerID           *string `db:"trigger_id"`
	Surface             string  `db:"surface"`
	ModelConfigID       *string `db:"model_config_id"`
	ModelName           *string `db:"model_name"`
	TaskID              *string `db:"task_id"`
	TraceID             *string `db:"trace_id"`
	UncachedInputTokens int64   `db:"uncached_input_tokens"`
	OutputTokens        int64   `db:"output_tokens"`
	CacheReadTokens     int64   `db:"cache_read_tokens"`
	CacheWriteTokens    int64   `db:"cache_write_tokens"`
	UncachedInputCost   *int64  `db:"uncached_input_cost"`
	OutputCost          *int64  `db:"output_cost"`
	CacheReadCost       *int64  `db:"cache_read_cost"`
	CacheWriteCost      *int64  `db:"cache_write_cost"`
	TotalCost           *int64  `db:"total_cost"`
}

type spendLedgerDetailRow struct {
	ID                     string  `db:"id"`
	ModelKey               *string `db:"model_key"`
	SentModelName          *string `db:"sent_model_name"`
	ToolName               *string `db:"tool_name"`
	WorkflowNodeID         *string `db:"workflow_node_id"`
	ParentTaskID           *string `db:"parent_task_id"`
	SessionID              *string `db:"session_id"`
	ResolvedModelName      *string `db:"resolved_model_name"`
	ResolvedModelProvider  *string `db:"resolved_model_provider"`
	ResolvedAgentName      *string `db:"resolved_agent_name"`
	ResolvedWorkflowName   *string `db:"resolved_workflow_name"`
	ResolvedRootName       *string `db:"resolved_root_name"`
	ResolvedEntrypointName *string `db:"resolved_entrypoint_name"`
	AttemptCount           *int    `db:"attempt_count"`
	CacheWritesReported    *bool   `db:"cache_writes_reported"`
}

func pricedLedgerEntry() SpendLedgerEntry {
	agentID := idgen.New()
	return SpendLedgerEntry{
		ID:                  idgen.New(),
		CompletedTime:       1_760_000_000_000,
		UserID:              new(idgen.New()),
		AgentID:             new(agentID),
		RootKind:            new("agent"),
		RootID:              new(agentID),
		Channel:             new("cli"),
		TriggerKind:         new("chat"),
		Surface:             "agent",
		ModelConfigID:       new(idgen.New()),
		TaskID:              new("task-1"),
		TraceID:             new("trace-1"),
		UncachedInputTokens: 400,
		OutputTokens:        50,
		CacheReadTokens:     400,
		CacheWriteTokens:    200,
		Cost:                &spend.Cost{UncachedInput: 12_000_000, Output: 7_500_000, CacheRead: 1_200_000, CacheWrite: 7_500_000},
	}
}

func unpricedLedgerEntry() SpendLedgerEntry {
	return SpendLedgerEntry{
		ID:                  idgen.New(),
		CompletedTime:       1_760_000_000_001,
		UserSub:             new("user-2"),
		Surface:             "compaction",
		ModelName:           new("inline-model"),
		UncachedInputTokens: 10,
		OutputTokens:        5,
	}
}

func shippedLedgerColumns() string {
	return strings.TrimSuffix(strings.TrimSpace(spendLedgerColumns), ",\n\tavoided_input_tokens, avoided_input_cost")
}

func insertAtShippedLedgerSchema(t *testing.T, db *DB, entries ...SpendLedgerEntry) {
	t.Helper()
	columns := shippedLedgerColumns()
	placeholders := "(" + strings.Repeat("?, ", spendLedgerColumnCount-3) + "?)"
	for _, e := range entries {
		args := []any{
			e.ID, e.CompletedTime, e.UserID, e.UserSub, e.AgentID, e.AgentName,
			e.WorkflowID, e.WorkflowName, e.RootKind, e.RootID, e.RootName, e.RootNodeID, e.EntrypointID, e.EntrypointName,
			e.Channel, e.TriggerKind, e.TriggerID, e.Surface, e.ModelConfigID, e.ModelName, e.TaskID, e.TraceID,
			e.UncachedInputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens,
		}
		args = appendCostArgs(args, e.Cost)
		if _, err := db.ExecContext(t.Context(), `INSERT INTO token_spend_ledger (`+columns+`) VALUES `+placeholders, args...); err != nil {
			t.Fatalf("insert into the shipped ledger: %v", err)
		}
	}
}

func readSpendLedgerRow(t *testing.T, db *DB, id string) *spendLedgerRow {
	t.Helper()
	row, err := dbutil.QueryRow[spendLedgerRow](t.Context(), db,
		`SELECT `+shippedLedgerColumns()+`, total_cost FROM token_spend_ledger WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read ledger row %s: %v", id, err)
	}
	return row
}

func readSpendLedgerDetailRow(t *testing.T, db *DB, id string) *spendLedgerDetailRow {
	t.Helper()
	row, err := dbutil.QueryRow[spendLedgerDetailRow](t.Context(), db,
		`SELECT `+spendLedgerDetailColumns+` FROM token_spend_ledger_details WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read ledger details %s: %v", id, err)
	}
	return row
}

func countSpendLedgerDetailRows(t *testing.T, db *DB) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger_details`)
	if err != nil {
		t.Fatalf("count ledger details: %v", err)
	}
	return n
}

func countSpendLedgerRows(t *testing.T, db *DB) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM token_spend_ledger`)
	if err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return n
}

func TestSpendLedgerRepo_InsertStampsATotalOfTheFourCostsForAPricedCall(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := pricedLedgerEntry()

	inserted, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if inserted != 1 {
		t.Errorf("inserted = %d, want 1", inserted)
	}

	row := readSpendLedgerRow(t, db, entry.ID)
	if row == nil {
		t.Fatal("row was not stored")
	}
	wantTotal := entry.Cost.UncachedInput + entry.Cost.Output + entry.Cost.CacheRead + entry.Cost.CacheWrite
	if row.TotalCost == nil || *row.TotalCost != wantTotal {
		t.Errorf("total_cost = %v, want %d", row.TotalCost, wantTotal)
	}
	if row.UncachedInputTokens != 400 || row.CacheWriteTokens != 200 {
		t.Errorf("tokens = input %d cacheWrite %d, want 400 and 200", row.UncachedInputTokens, row.CacheWriteTokens)
	}
	if row.AgentID == nil || *row.AgentID != *entry.AgentID || row.ModelConfigID == nil || *row.ModelConfigID != *entry.ModelConfigID {
		t.Errorf("attribution not stored: agent %v, model config %v", row.AgentID, row.ModelConfigID)
	}
	if row.Channel == nil || *row.Channel != "cli" {
		t.Errorf("channel = %v, want cli", row.Channel)
	}
}

func TestSpendLedgerRepo_InsertStoresNullCostsForAnUnpricedCallSoItIsNotAFreeOne(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := unpricedLedgerEntry()

	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	row := readSpendLedgerRow(t, db, entry.ID)
	if row == nil {
		t.Fatal("row was not stored")
	}
	for name, cost := range map[string]*int64{
		"uncached_input_cost": row.UncachedInputCost, "output_cost": row.OutputCost,
		"cache_read_cost": row.CacheReadCost, "cache_write_cost": row.CacheWriteCost,
		"total_cost": row.TotalCost,
	} {
		if cost != nil {
			t.Errorf("%s = %d, want NULL for an unpriced call", name, *cost)
		}
	}
	if row.ModelName == nil || *row.ModelName != "inline-model" || row.ModelConfigID != nil {
		t.Errorf("model = config %v name %v, want the name alone an unpriced call carried", row.ModelConfigID, row.ModelName)
	}
}

func TestSpendLedgerRepo_InsertStoresAbsentOptionalColumnsAsNull(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := unpricedLedgerEntry()

	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	row := readSpendLedgerRow(t, db, entry.ID)
	if row == nil {
		t.Fatal("row was not stored")
	}
	for name, v := range map[string]*string{
		"user_id": row.UserID, "agent_id": row.AgentID, "agent_name": row.AgentName,
		"workflow_id": row.WorkflowID, "workflow_name": row.WorkflowName, "root_kind": row.RootKind,
		"root_id": row.RootID, "root_name": row.RootName, "root_node_id": row.RootNodeID, "entrypoint_id": row.EntrypointID,
		"entrypoint_name": row.EntrypointName, "trigger_id": row.TriggerID,
		"trigger_kind": row.TriggerKind, "task_id": row.TaskID,
		"trace_id": row.TraceID, "channel": row.Channel, "model_config_id": row.ModelConfigID,
	} {
		if v != nil {
			t.Errorf("%s = %q, want NULL", name, *v)
		}
	}
}

func TestSpendLedgerRepo_InsertAZeroCostIsNotNull(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := unpricedLedgerEntry()
	entry.Cost = &spend.Cost{}

	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	row := readSpendLedgerRow(t, db, entry.ID)
	if row == nil || row.TotalCost == nil || *row.TotalCost != 0 {
		t.Errorf("a free call must have total_cost 0, got row %+v", row)
	}
}

func TestSpendLedgerRepo_InsertFilesAnIdOnceAndLeavesTheFirstRowAlone(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	first := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{first}); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	redelivered := first
	redelivered.UserID = new(idgen.New())
	redelivered.Cost = &spend.Cost{UncachedInput: 1, Output: 1, CacheRead: 1, CacheWrite: 1}
	inserted, err := repo.Insert(t.Context(), []SpendLedgerEntry{redelivered})
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}

	if inserted != 0 {
		t.Errorf("second insert returned %d, want 0 new rows", inserted)
	}
	if n := countSpendLedgerRows(t, db); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	row := readSpendLedgerRow(t, db, first.ID)
	if *row.UserID != *first.UserID || *row.UncachedInputCost != first.Cost.UncachedInput {
		t.Errorf("the first row was altered by the duplicate: user %q uncached_input_cost %d", *row.UserID, *row.UncachedInputCost)
	}
}

func TestSpendLedgerRepo_InsertFilesAnIdRepeatedWithinOneBatchOnce(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := pricedLedgerEntry()

	inserted, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry, entry})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if inserted != 1 {
		t.Errorf("inserted = %d, want 1", inserted)
	}
	if n := countSpendLedgerRows(t, db); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestSpendLedgerRepo_InsertCountsOnlyTheNewRowsOfAMixedBatch(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	existing := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{existing}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	inserted, err := repo.Insert(t.Context(), []SpendLedgerEntry{existing, unpricedLedgerEntry(), unpricedLedgerEntry()})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if inserted != 2 {
		t.Errorf("inserted = %d, want 2", inserted)
	}
	if n := countSpendLedgerRows(t, db); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

func TestSpendLedgerRepo_InsertWritesAFullBatchInOneCall(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entries := make([]SpendLedgerEntry, MaxSpendLedgerBatch)
	for i := range entries {
		entries[i] = unpricedLedgerEntry()
	}

	inserted, err := repo.Insert(t.Context(), entries)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if inserted != MaxSpendLedgerBatch {
		t.Errorf("inserted = %d, want %d", inserted, MaxSpendLedgerBatch)
	}
}

func TestSpendLedgerRepo_InsertRefusesMoreThanTheBatchLimit(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entries := make([]SpendLedgerEntry, MaxSpendLedgerBatch+1)
	for i := range entries {
		entries[i] = unpricedLedgerEntry()
	}

	if _, err := repo.Insert(t.Context(), entries); err == nil {
		t.Fatal("insert of more than the batch limit succeeded, want an error")
	}
	if n := countSpendLedgerRows(t, db); n != 0 {
		t.Errorf("rows = %d, want none written by a refused batch", n)
	}
}

func TestSpendLedgerRepo_DeleteOlderThanClearsEveryOldRowAcrossBatches(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	cutoff := time.UnixMilli(1_000_000)
	var old, recent []string
	var entries []SpendLedgerEntry
	for i := range 5 {
		e := unpricedLedgerEntry()
		e.CompletedTime = cutoff.UnixMilli() - int64(i+1)
		old = append(old, e.ID)
		entries = append(entries, e)
	}
	for i := range 3 {
		e := unpricedLedgerEntry()
		e.CompletedTime = cutoff.UnixMilli() + int64(i)
		recent = append(recent, e.ID)
		entries = append(entries, e)
	}
	if _, err := repo.Insert(t.Context(), entries); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := repo.DeleteOlderThan(t.Context(), cutoff, 2)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if deleted != int64(len(old)) {
		t.Errorf("deleted = %d, want %d", deleted, len(old))
	}
	for _, id := range old {
		if readSpendLedgerRow(t, db, id) != nil {
			t.Errorf("row %s older than the cutoff survived", id)
		}
	}
	for _, id := range recent {
		if readSpendLedgerRow(t, db, id) == nil {
			t.Errorf("row %s at or after the cutoff was deleted", id)
		}
	}
}

func TestSpendLedgerRepo_DeleteOlderThanStopsExactlyWhenARunFillsABatch(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	var entries []SpendLedgerEntry
	for range 4 {
		e := unpricedLedgerEntry()
		e.CompletedTime = 10
		entries = append(entries, e)
	}
	if _, err := repo.Insert(t.Context(), entries); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := repo.DeleteOlderThan(t.Context(), time.UnixMilli(1000), 2)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if deleted != 4 || countSpendLedgerRows(t, db) != 0 {
		t.Errorf("deleted = %d, remaining = %d; want 4 and 0", deleted, countSpendLedgerRows(t, db))
	}
}

func TestSpendLedgerRepo_DeleteOlderThanRefusesABatchSizeThatCouldNeverFinish(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := unpricedLedgerEntry()
	entry.CompletedTime = 10
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, size := range []int{0, -1} {
		if _, err := repo.DeleteOlderThan(t.Context(), time.UnixMilli(1000), size); err == nil {
			t.Errorf("batch size %d: err = nil, want an error", size)
		}
	}
	if n := countSpendLedgerRows(t, db); n != 1 {
		t.Errorf("rows = %d, want the row left alone by a refused delete", n)
	}
}

func TestTokenSpendLedgerMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "token_spend_ledger", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if ledgerTableExists(t, db, "token_spend_ledger") || ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("the ledger tables exist before their migration")
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if !ledgerTableExists(t, db, "token_spend_ledger") || !ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("a ledger table is absent after migrate up")
		}
		assertLedgerAcceptsAPricedAndAnUnpricedRow(t, db)
		assertLedgerIndexes(t, db, true)

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		if ledgerTableExists(t, db, "token_spend_ledger") || ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("a ledger table survived the rollback")
		}
		assertLedgerIndexes(t, db, false)

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		if !ledgerTableExists(t, db, "token_spend_ledger") || !ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("a ledger table is absent after re-apply")
		}
		if n := countSpendLedgerRows(t, db); n != 0 {
			t.Errorf("re-applied ledger holds %d rows, want an empty table", n)
		}
		assertLedgerAcceptsAPricedAndAnUnpricedRow(t, db)
		assertLedgerRefusesPartiallyPricedRows(t, db)
	})
}

func assertLedgerAcceptsAPricedAndAnUnpricedRow(t *testing.T, db *DB) {
	t.Helper()
	priced, unpriced := pricedLedgerEntry(), unpricedLedgerEntry()
	insertAtShippedLedgerSchema(t, db, priced, unpriced)
	if row := readSpendLedgerRow(t, db, priced.ID); row == nil || row.TotalCost == nil {
		t.Errorf("a priced row has no generated total_cost: %+v", row)
	}
	if row := readSpendLedgerRow(t, db, unpriced.ID); row == nil || row.TotalCost != nil {
		t.Errorf("an unpriced row has a total_cost: %+v", row)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger`); err != nil {
		t.Fatalf("clear ledger: %v", err)
	}
}

func assertDetailsAcceptAnAttemptCountAndCascade(t *testing.T, db *DB) {
	t.Helper()
	entry := pricedLedgerEntry()
	insertAtShippedLedgerSchema(t, db, entry)
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO token_spend_ledger_details (id, attempt_count) VALUES (?, 2)`, entry.ID); err != nil {
		t.Fatalf("insert into the migrated details table: %v", err)
	}
	if got, err := dbutil.QueryVal[int](t.Context(), db,
		`SELECT attempt_count FROM token_spend_ledger_details WHERE id = ?`, entry.ID); err != nil || got != 2 {
		t.Errorf("attempt_count = %d, %v; want 2", got, err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger`); err != nil {
		t.Fatalf("clear ledger: %v", err)
	}
	if n := countSpendLedgerDetailRows(t, db); n != 0 {
		t.Errorf("details rows = %d after the ledger was cleared, want the cascade to remove them", n)
	}
}

func TestSpendLedgerAttemptCountMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "spend_ledger_details_attempt_count_and_sent_model", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertDetailsAcceptAnAttemptCountAndCascade(t, db)

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		if !ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("the details table did not survive the rollback of its added column")
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertDetailsAcceptAnAttemptCountAndCascade(t, db)
	})
}

func TestSpendLedgerCacheWritesReportedMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "spend_ledger_details_cache_writes_reported", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		assertDetailsKeepWhetherCacheWritesWereReported(t, db)

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		if !ledgerTableExists(t, db, "token_spend_ledger_details") {
			t.Fatal("the details table did not survive the rollback of its added column")
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		assertDetailsKeepWhetherCacheWritesWereReported(t, db)
	})
}

func assertDetailsKeepWhetherCacheWritesWereReported(t *testing.T, db *DB) {
	t.Helper()
	for _, want := range []*bool{new(true), new(false), nil} {
		entry := pricedLedgerEntry()
		insertAtShippedLedgerSchema(t, db, entry)
		if _, err := db.ExecContext(t.Context(),
			`INSERT INTO token_spend_ledger_details (id, cache_writes_reported) VALUES (?, ?)`, entry.ID, want); err != nil {
			t.Fatalf("insert into the migrated details table: %v", err)
		}
		got, err := dbutil.QueryRow[struct {
			CacheWritesReported *bool `db:"cache_writes_reported"`
		}](t.Context(), db, `SELECT cache_writes_reported FROM token_spend_ledger_details WHERE id = ?`, entry.ID)
		if err != nil || got == nil || (got.CacheWritesReported == nil) != (want == nil) ||
			(want != nil && *got.CacheWritesReported != *want) {
			t.Errorf("%s: cache_writes_reported stored as %+v, %v; want %v", db.Dialect(), got, err, want)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger`); err != nil {
		t.Fatalf("clear ledger: %v", err)
	}
}

func TestSpendLedgerSentModelMigration_KeepsAShippedRowsModel(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "spend_ledger_details_attempt_count_and_sent_model", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to the shipped schema: %v", err)
		}
		entry := pricedLedgerEntry()
		insertAtShippedLedgerSchema(t, db, entry)
		if _, err := db.ExecContext(ctx,
			`INSERT INTO token_spend_ledger_details (id, requested_model_name) VALUES (?, 'openai/gpt-shipped')`, entry.ID); err != nil {
			t.Fatalf("insert into the shipped details table: %v", err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		if got, err := dbutil.QueryVal[string](ctx, db,
			`SELECT sent_model_name FROM token_spend_ledger_details WHERE id = ?`, entry.ID); err != nil || got != "openai/gpt-shipped" {
			t.Errorf("%s: sent_model_name after the rename = %q, %v; want the shipped row's model", db.Dialect(), got, err)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		model, err := dbutil.QueryVal[string](ctx, db,
			`SELECT requested_model_name FROM token_spend_ledger_details WHERE id = ?`, entry.ID)
		if err != nil || model != "openai/gpt-shipped" {
			t.Errorf("%s: requested_model_name after rollback = %q, %v; want the row's model back", db.Dialect(), model, err)
		}
	})
}

func insertLedgerOnly(t *testing.T, db *DB, entries ...SpendLedgerEntry) {
	t.Helper()
	args, err := ledgerInsertArgs(entries)
	if err != nil {
		t.Fatalf("ledger insert args: %v", err)
	}
	if err := db.WithTx(t.Context(), func(tx *dbutil.Tx) error {
		_, err := insertLedgerRows(t.Context(), tx, len(entries), args)
		return err
	}); err != nil {
		t.Fatalf("insert into the migrated ledger: %v", err)
	}
}

func assertLedgerRefusesPartiallyPricedRows(t *testing.T, db *DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO token_spend_ledger (id, completed_time, user_sub, surface, model_name,
			uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, uncached_input_cost)
		 VALUES (?, 1, 'u', 'agent', 'm', 1, 1, 0, 0, 1)`, idgen.New())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "check constraint") {
		t.Errorf("%s: a row with only some costs: err = %v, want a check-constraint violation", db.Dialect(), err)
	}
}

var tokenSpendLedgerIndexes = []string{
	"idx_token_spend_ledger_completed", "idx_token_spend_ledger_user", "idx_token_spend_ledger_user_sub",
	"idx_token_spend_ledger_agent", "idx_token_spend_ledger_agent_name", "idx_token_spend_ledger_workflow",
	"idx_token_spend_ledger_workflow_name", "idx_token_spend_ledger_root", "idx_token_spend_ledger_root_name",
	"idx_token_spend_ledger_entrypoint", "idx_token_spend_ledger_entrypoint_name",
	"idx_token_spend_ledger_model", "idx_token_spend_ledger_trace",
}

func assertLedgerIndexes(t *testing.T, db *DB, wantPresent bool) {
	t.Helper()
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`
	if db.Dialect() == "postgres" {
		query = `SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?`
	}
	for _, name := range tokenSpendLedgerIndexes {
		count, err := dbutil.QueryVal[int](t.Context(), db, query, name)
		if err != nil {
			t.Fatalf("look up index %s: %v", name, err)
		}
		if (count > 0) != wantPresent {
			t.Errorf("%s: index %s present = %v, want %v", db.Dialect(), name, count > 0, wantPresent)
		}
	}
}

func ledgerTableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	if db.Dialect() == "postgres" {
		query = `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = ?`
	}
	count, err := dbutil.QueryVal[int](t.Context(), db, query, name)
	if err != nil {
		t.Fatalf("look up %s: %v", name, err)
	}
	return count > 0
}

func assertRowMatches(t *testing.T, got, want any) {
	t.Helper()
	gotV, wantV := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := range gotV.NumField() {
		g, w := gotV.Field(i), wantV.Field(i)
		if g.Kind() == reflect.Pointer && !g.IsNil() && !w.IsNil() {
			g, w = g.Elem(), w.Elem()
		}
		if !reflect.DeepEqual(g.Interface(), w.Interface()) {
			t.Errorf("%s = %v, want %v", gotV.Type().Field(i).Tag.Get("db"), g.Interface(), w.Interface())
		}
	}
}

func TestSpendLedgerRepo_InsertFilesEveryFieldOfEachSideOfEachPairInItsOwnColumn(t *testing.T) {
	db := TestDB(t)
	byID := SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: 1_760_000_000_123, UserID: new(idgen.New()),
		AgentID: new(idgen.New()), WorkflowID: new(idgen.New()),
		RootKind: new("root-kind-x"), RootID: new(idgen.New()), RootNodeID: new("root-node-x"), EntrypointID: new(idgen.New()),
		Channel: new("channel-x"), TriggerKind: new("trigger-kind-x"), TriggerID: new("trigger-x"),
		Surface: "surface-x", ModelConfigID: new(idgen.New()),
		TaskID: new("task-x"), TraceID: new("trace-x"),
		UncachedInputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44,
		Cost: &spend.Cost{UncachedInput: 101, Output: 202, CacheRead: 303, CacheWrite: 404},
	}
	byName := SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: 1_760_000_000_456, UserSub: new("sub-x"),
		AgentName: new("agent-x"), WorkflowName: new("workflow-x"),
		RootKind: new("root-kind-y"), RootName: new("root-x"), RootNodeID: new("root-node-y"), EntrypointName: new("entrypoint-x"),
		Surface: "surface-y", ModelName: new("model-name-x"),
		UncachedInputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
	}

	if _, err := NewSpendLedgerRepo(db).Insert(t.Context(), []SpendLedgerEntry{byID, byName}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	assertRowMatches(t, *readSpendLedgerRow(t, db, byID.ID), spendLedgerRow{
		ID: byID.ID, CompletedTime: byID.CompletedTime, UserID: byID.UserID,
		AgentID: byID.AgentID, WorkflowID: byID.WorkflowID,
		RootKind: byID.RootKind, RootID: byID.RootID, RootNodeID: byID.RootNodeID, EntrypointID: byID.EntrypointID,
		Channel: byID.Channel, TriggerKind: byID.TriggerKind, TriggerID: byID.TriggerID,
		Surface: byID.Surface, ModelConfigID: byID.ModelConfigID, TaskID: byID.TaskID, TraceID: byID.TraceID,
		UncachedInputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44,
		UncachedInputCost: new(int64(101)), OutputCost: new(int64(202)),
		CacheReadCost: new(int64(303)), CacheWriteCost: new(int64(404)), TotalCost: new(int64(1010)),
	})
	assertRowMatches(t, *readSpendLedgerRow(t, db, byName.ID), spendLedgerRow{
		ID: byName.ID, CompletedTime: byName.CompletedTime, UserSub: byName.UserSub,
		AgentName: byName.AgentName, WorkflowName: byName.WorkflowName,
		RootKind: byName.RootKind, RootName: byName.RootName, RootNodeID: byName.RootNodeID, EntrypointName: byName.EntrypointName,
		Surface: byName.Surface, ModelName: byName.ModelName,
		UncachedInputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
	})
}

func fullLedgerDetail(id string) SpendLedgerDetail {
	return SpendLedgerDetail{
		ID: id, ModelKey: new("key-x"), SentModelName: new("sent-x"), ToolName: new("tool-x"),
		WorkflowNodeID: new("wf-node-x"), ParentTaskID: new("parent-x"),
		SessionID: new("session-x"), ResolvedModelName: new("resolved-model-x"),
		ResolvedModelProvider: new("provider-x"), ResolvedAgentName: new("agent-x"),
		ResolvedWorkflowName: new("workflow-x"), ResolvedRootName: new("root-x"),
		ResolvedEntrypointName: new("entrypoint-x"), AttemptCount: new(2),
		CacheWritesReported: new(false),
	}
}

func TestSpendLedgerRepo_InsertDetailsFilesEveryFieldInItsOwnColumn(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	detail := fullLedgerDetail(entry.ID)

	inserted, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{detail})
	if err != nil {
		t.Fatalf("insert details: %v", err)
	}

	if inserted != 1 {
		t.Errorf("inserted = %d, want 1", inserted)
	}
	got := readSpendLedgerDetailRow(t, db, entry.ID)
	if got == nil {
		t.Fatal("details were not stored")
	}
	assertRowMatches(t, *got, spendLedgerDetailRow(detail))
}

func TestSpendLedgerRepo_InsertDetailsStoresAbsentColumnsAsNull(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{{ID: entry.ID}}); err != nil {
		t.Fatalf("insert details: %v", err)
	}

	assertRowMatches(t, *readSpendLedgerDetailRow(t, db, entry.ID), spendLedgerDetailRow{ID: entry.ID})
}

func TestSpendLedgerRepo_InsertDetailsSkipsAnIdThatAlreadyHasDetails(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	entry := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	first := fullLedgerDetail(entry.ID)
	if _, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{first}); err != nil {
		t.Fatalf("first details: %v", err)
	}

	redelivered := fullLedgerDetail(entry.ID)
	redelivered.ToolName = new("other-tool")
	inserted, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{redelivered})
	if err != nil {
		t.Fatalf("second details: %v", err)
	}

	if inserted != 0 {
		t.Errorf("inserted = %d, want 0 new rows", inserted)
	}
	if got := readSpendLedgerDetailRow(t, db, entry.ID); *got.ToolName != "tool-x" {
		t.Errorf("tool_name = %q, want the first details left alone", *got.ToolName)
	}
}

func TestSpendLedgerRepo_InsertDetailsRefusesADetailRowForACallTheLedgerDoesNotHold(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	held := pricedLedgerEntry()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{held}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{fullLedgerDetail(held.ID), fullLedgerDetail(idgen.New())})

	if err == nil {
		t.Fatal("InsertDetails accepted a row whose id is not in the ledger")
	}
	if n := countSpendLedgerDetailRows(t, db); n != 0 {
		t.Errorf("details rows = %d, want none written by a refused batch", n)
	}
}

func TestSpendLedgerRepo_InsertDetailsRefusesMoreThanTheBatchLimit(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	details := make([]SpendLedgerDetail, MaxSpendLedgerBatch+1)
	for i := range details {
		details[i] = SpendLedgerDetail{ID: idgen.New()}
	}

	if _, err := repo.InsertDetails(t.Context(), details); err == nil {
		t.Fatal("InsertDetails of more than the batch limit succeeded, want an error")
	}
}

func TestSpendLedgerRepo_InsertDetailsOfNothingWritesNothing(t *testing.T) {
	db := TestDB(t)

	inserted, err := NewSpendLedgerRepo(db).InsertDetails(t.Context(), nil)

	if err != nil || inserted != 0 {
		t.Errorf("InsertDetails(nil) = %d, %v, want 0 and no error", inserted, err)
	}
}

func TestSpendLedgerRepo_InsertRefusesAnEntryWithoutExactlyOneUserIdentity(t *testing.T) {
	cases := map[string]func(*SpendLedgerEntry){
		"both user_id and user_sub": func(e *SpendLedgerEntry) { e.UserID, e.UserSub = new(idgen.New()), new("sub") },
		"neither":                   func(e *SpendLedgerEntry) { e.UserID, e.UserSub = nil, nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			bad := unpricedLedgerEntry()
			mutate(&bad)

			_, err := NewSpendLedgerRepo(db).Insert(t.Context(), []SpendLedgerEntry{unpricedLedgerEntry(), bad})

			if err == nil || !strings.Contains(err.Error(), "exactly one of user_id and user_sub") {
				t.Fatalf("Insert err = %v, want the refusal before any SQL runs", err)
			}
			if n := countSpendLedgerRows(t, db); n != 0 {
				t.Errorf("ledger holds %d rows, want none written by a refused batch", n)
			}
		})
	}
}

func TestSpendLedgerRepo_InsertRefusesAnEntryWithoutExactlyOneModelIdentity(t *testing.T) {
	cases := map[string]func(*SpendLedgerEntry){
		"both model_config_id and model_name": func(e *SpendLedgerEntry) { e.ModelConfigID = new(idgen.New()) },
		"neither":                             func(e *SpendLedgerEntry) { e.ModelConfigID, e.ModelName = nil, nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			bad := unpricedLedgerEntry()
			mutate(&bad)

			_, err := NewSpendLedgerRepo(db).Insert(t.Context(), []SpendLedgerEntry{unpricedLedgerEntry(), bad})

			if err == nil || !strings.Contains(err.Error(), "exactly one of model_config_id and model_name") {
				t.Fatalf("Insert err = %v, want the refusal before any SQL runs", err)
			}
			if n := countSpendLedgerRows(t, db); n != 0 {
				t.Errorf("ledger holds %d rows, want none written by a refused batch", n)
			}
		})
	}
}

func allCosts(column string, value int) map[string]any {
	overrides := map[string]any{
		"uncached_input_cost": 1, "output_cost": 1, "cache_read_cost": 1, "cache_write_cost": 1,
	}
	overrides[column] = value
	return overrides
}

func rawLedgerInsert(db *DB, t *testing.T, overrides map[string]any) error {
	t.Helper()
	row := map[string]any{
		"id": idgen.New(), "completed_time": 1, "user_sub": "u", "surface": "agent", "model_name": "m",
		"uncached_input_tokens": 0, "output_tokens": 0, "cache_read_tokens": 0, "cache_write_tokens": 0,
	}
	for k, v := range overrides {
		row[k] = v
	}
	columns := make([]string, 0, len(row))
	args := make([]any, 0, len(row))
	for k, v := range row {
		columns = append(columns, k)
		args = append(args, v)
	}
	_, err := db.ExecContext(t.Context(),
		`INSERT INTO token_spend_ledger (`+strings.Join(columns, ", ")+`) VALUES (`+
			strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")+`)`, args...)
	return err
}

func TestTokenSpendLedgerChecks_RefuseRowsThatBreakAnIdentityRule(t *testing.T) {
	cases := []struct {
		constraint string
		overrides  map[string]any
	}{
		{"token_spend_ledger_one_user", map[string]any{"user_id": idgen.New(), "user_sub": "u"}},
		{"token_spend_ledger_one_user", map[string]any{"user_sub": nil}},
		{"token_spend_ledger_one_model", map[string]any{"model_config_id": idgen.New(), "model_name": "m"}},
		{"token_spend_ledger_one_model", map[string]any{"model_name": nil}},
		{"token_spend_ledger_agent_pair", map[string]any{"agent_id": idgen.New(), "agent_name": "a"}},
		{"token_spend_ledger_workflow_pair", map[string]any{"workflow_id": idgen.New(), "workflow_name": "w"}},
		{"token_spend_ledger_entrypoint_pair", map[string]any{"entrypoint_id": idgen.New(), "entrypoint_name": "e"}},
		{"token_spend_ledger_root", map[string]any{"root_kind": "agent", "root_id": idgen.New(), "root_name": "r"}},
		{"token_spend_ledger_root", map[string]any{"root_kind": "agent"}},
		{"token_spend_ledger_root", map[string]any{"root_id": idgen.New()}},
		{"token_spend_ledger_root", map[string]any{"root_name": "r"}},
		{"token_spend_ledger_root_node", map[string]any{"root_node_id": "n"}},
		{"token_spend_ledger_tokens_non_negative", map[string]any{"uncached_input_tokens": -1}},
		{"token_spend_ledger_tokens_non_negative", map[string]any{"output_tokens": -1}},
		{"token_spend_ledger_tokens_non_negative", map[string]any{"cache_read_tokens": -1}},
		{"token_spend_ledger_tokens_non_negative", map[string]any{"cache_write_tokens": -1}},
		{"token_spend_ledger_costs_non_negative", allCosts("uncached_input_cost", -1)},
		{"token_spend_ledger_costs_non_negative", allCosts("output_cost", -1)},
		{"token_spend_ledger_costs_non_negative", allCosts("cache_read_cost", -1)},
		{"token_spend_ledger_costs_non_negative", allCosts("cache_write_cost", -1)},
	}
	for _, tc := range cases {
		t.Run(tc.constraint, func(t *testing.T) {
			db := TestDB(t)

			err := rawLedgerInsert(db, t, tc.overrides)

			if err == nil || !strings.Contains(err.Error(), tc.constraint) {
				t.Errorf("%s: insert of %v: err = %v, want a violation of %s", db.Dialect(), tc.overrides, err, tc.constraint)
			}
			if n := countSpendLedgerRows(t, db); n != 0 {
				t.Errorf("ledger holds %d rows, want none", n)
			}
		})
	}
}

func TestTokenSpendLedgerChecks_AcceptTheValidShapesOfEachRule(t *testing.T) {
	cases := map[string]map[string]any{
		"user by id":                  {"user_id": idgen.New(), "user_sub": nil},
		"model by config":             {"model_config_id": idgen.New(), "model_name": nil},
		"agent by id":                 {"agent_id": idgen.New()},
		"agent by name":               {"agent_name": "a"},
		"root by id":                  {"root_kind": "agent", "root_id": idgen.New()},
		"root by name":                {"root_kind": "workflow", "root_name": "r"},
		"root node with root by id":   {"root_kind": "agent", "root_id": idgen.New(), "root_node_id": "n"},
		"root node with root by name": {"root_kind": "workflow", "root_name": "r", "root_node_id": "n"},
		"zero tokens and zero costs":  allCosts("output_cost", 0),
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			if err := rawLedgerInsert(db, t, overrides); err != nil {
				t.Errorf("insert of %v: %v", overrides, err)
			}
		})
	}
}

func TestSpendLedgerRepo_DeleteOlderThanRemovesTheDetailsOfPurgedRowsAndKeepsTheRest(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	cutoff := time.UnixMilli(1_000_000)
	old, kept := unpricedLedgerEntry(), unpricedLedgerEntry()
	old.CompletedTime, kept.CompletedTime = cutoff.UnixMilli()-1, cutoff.UnixMilli()
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{old, kept}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := repo.InsertDetails(t.Context(), []SpendLedgerDetail{fullLedgerDetail(old.ID), fullLedgerDetail(kept.ID)}); err != nil {
		t.Fatalf("seed details: %v", err)
	}

	if _, err := repo.DeleteOlderThan(t.Context(), cutoff, 10); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if readSpendLedgerDetailRow(t, db, old.ID) != nil {
		t.Error("details of a purged row survived")
	}
	if readSpendLedgerDetailRow(t, db, kept.ID) == nil {
		t.Error("details of a kept row were deleted")
	}
}

type componentFixture struct {
	table  ComponentTable
	create func(t *testing.T, db *DB, name string) string
}

var componentFixtures = []componentFixture{
	{ComponentAgents, func(t *testing.T, db *DB, name string) string {
		a := &Agent{Name: name, CreatedBy: "u", UpdatedBy: "u"}
		if err := NewAgentRepo(db).Create(t.Context(), a, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		return a.ID
	}},
	{ComponentWorkflows, func(t *testing.T, db *DB, name string) string {
		w := &Workflow{Name: name, AppConfig: `{}`, CreatedBy: "u", UpdatedBy: "u"}
		if err := NewWorkflowRepo(db).Create(t.Context(), w, WorkflowBindings{}); err != nil {
			t.Fatalf("create workflow: %v", err)
		}
		return w.ID
	}},
	{ComponentGateways, func(t *testing.T, db *DB, name string) string {
		g := &Gateway{Name: name, Slug: name, Type: "slack", CreatedBy: "u", UpdatedBy: "u"}
		if err := NewGatewayRepo(db).Create(t.Context(), g); err != nil {
			t.Fatalf("create gateway: %v", err)
		}
		return g.ID
	}},
}

func TestSpendLedgerRepo_ResolveNamesMapsEachExistingIdToItsRowsNameAndOmitsTheRest(t *testing.T) {
	for _, fx := range componentFixtures {
		t.Run(string(fx.table), func(t *testing.T) {
			db := TestDB(t)
			first, second := fx.create(t, db, "first-name"), fx.create(t, db, "second-name")
			absent := idgen.New()

			names, err := NewSpendLedgerRepo(db).ResolveNames(t.Context(), fx.table, []string{first, absent, second})

			if err != nil {
				t.Fatalf("ResolveNames: %v", err)
			}
			want := map[string]string{first: "first-name", second: "second-name"}
			if !reflect.DeepEqual(names, want) {
				t.Errorf("names = %v, want %v", names, want)
			}
		})
	}
}

func TestSpendLedgerRepo_ResolveNamesOfNoIdsIsEmpty(t *testing.T) {
	db := TestDB(t)

	names, err := NewSpendLedgerRepo(db).ResolveNames(t.Context(), ComponentAgents, nil)

	if err != nil || len(names) != 0 {
		t.Errorf("ResolveNames(nil) = %v, %v, want an empty map", names, err)
	}
}

func TestSpendLedgerRepo_ResolveNamesRefusesATableThatIsNotAComponentTable(t *testing.T) {
	db := TestDB(t)

	if _, err := NewSpendLedgerRepo(db).ResolveNames(t.Context(), "users", []string{idgen.New()}); err == nil {
		t.Error("ResolveNames accepted an unknown table")
	}
}

func TestSpendLedgerRepo_ResolveNamesResolvesMoreIdsThanOneBatchHolds(t *testing.T) {
	db := TestDB(t)
	agentID := ComponentAgents
	var ids []string
	for range MaxSpendLedgerBatch + 5 {
		ids = append(ids, idgen.New())
	}
	early := componentFixtures[0].create(t, db, "early")
	late := componentFixtures[0].create(t, db, "late")
	ids[0], ids[len(ids)-1] = early, late

	names, err := NewSpendLedgerRepo(db).ResolveNames(t.Context(), agentID, ids)

	if err != nil {
		t.Fatalf("ResolveNames: %v", err)
	}
	if names[early] != "early" || names[late] != "late" || len(names) != 2 {
		t.Errorf("names = %v, want exactly early and late resolved across chunks", names)
	}
}

func TestSpendLedgerRepo_TotalCostAtTheTokenLimitSpreadAcrossEveryBucketDoesNotOverflow(t *testing.T) {
	db := TestDB(t)
	quarter := pricing.MaxTokensBeforeCostOverflows / 4
	tokens := spend.Tokens{UncachedInput: quarter, Output: quarter, CacheRead: quarter, CacheWrite: quarter}
	cost := spend.CostOf(tokens, pricing.ModelPrice{Input: pricing.MaxAmount, Output: pricing.MaxAmount})
	entry := unpricedLedgerEntry()
	entry.UncachedInputTokens, entry.OutputTokens = tokens.UncachedInput, tokens.Output
	entry.CacheReadTokens, entry.CacheWriteTokens = tokens.CacheRead, tokens.CacheWrite
	entry.Cost = &cost

	if _, err := NewSpendLedgerRepo(db).Insert(t.Context(), []SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	want := new(big.Int).Mul(big.NewInt(int64(pricing.MaxAmount)), big.NewInt(4*quarter))
	row := readSpendLedgerRow(t, db, entry.ID)
	if row == nil || row.TotalCost == nil || !want.IsInt64() || *row.TotalCost != want.Int64() {
		t.Errorf("total_cost = %v, want exactly %s", row.TotalCost, want)
	}
}

func TestSpendLedgerRepo_ConcurrentPurgesOnPostgresDeleteEveryOldRowExactlyOnce(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("only Postgres runs purges from several replicas at once")
	}
	repo := NewSpendLedgerRepo(db)
	const rows = 2000
	for start := 0; start < rows; start += MaxSpendLedgerBatch {
		entries := make([]SpendLedgerEntry, MaxSpendLedgerBatch)
		for i := range entries {
			entries[i] = unpricedLedgerEntry()
			entries[i].CompletedTime = int64(start + i)
		}
		if _, err := repo.Insert(t.Context(), entries); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var wg sync.WaitGroup
	deleted := make([]int64, 4)
	errs := make([]error, len(deleted))
	for i := range deleted {
		wg.Go(func() { deleted[i], errs[i] = repo.DeleteOlderThan(t.Context(), time.UnixMilli(rows), 50) })
	}
	wg.Wait()

	var total int64
	for i, err := range errs {
		if err != nil {
			t.Errorf("purge %d: %v", i, err)
		}
		total += deleted[i]
	}
	if remaining := countSpendLedgerRows(t, db); remaining != 0 || total != rows {
		t.Errorf("deleted %d across the purges with %d left, want all %d deleted exactly once", total, remaining, rows)
	}
}
