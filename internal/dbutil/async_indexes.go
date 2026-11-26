// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrAsyncIndexesBusy = errors.New("async index builds held by another process")

var errAsyncIndexRequeued = errors.New("async index row re-queued since the pass read it")

const (
	asyncIndexRetryInterval    = time.Minute
	maxAsyncIndexRetryInterval = time.Hour
	maxAsyncIndexErrorLen      = 1000
)

const MaxAsyncIndexAttempts = 10

type AsyncIndexConfig struct {
	DB               *sql.DB
	Schema           string
	QueueTable       string
	Logger           *slog.Logger
	ProgressInterval time.Duration
}

const defaultAsyncIndexProgressInterval = time.Minute

func (cfg AsyncIndexConfig) qualified(name string) string {
	if cfg.Schema == "" {
		return name
	}
	return cfg.Schema + "." + name
}

func (cfg AsyncIndexConfig) pendingSQL() string {
	return `SELECT name, definition, replaces, attempts, next_attempt_at, queued_at FROM ` + cfg.qualified(cfg.QueueTable) +
		` WHERE completed_at IS NULL AND attempts < $1 ORDER BY queued_at, name`
}

func (cfg AsyncIndexConfig) givenUpSQL() string {
	return `SELECT name FROM ` + cfg.qualified(cfg.QueueTable) +
		` WHERE completed_at IS NULL AND attempts >= $1 ORDER BY queued_at, name`
}

func (cfg AsyncIndexConfig) dropIndexSQL(name string) string {
	return `DROP INDEX CONCURRENTLY IF EXISTS ` + cfg.qualified(name)
}

func (cfg AsyncIndexConfig) markStartedSQL() string {
	return `UPDATE ` + cfg.qualified(cfg.QueueTable) + ` SET builder_pid = pg_backend_pid(), started_at = $1 WHERE name = $2 AND queued_at = $3`
}

func (cfg AsyncIndexConfig) markDoneSQL() string {
	return `UPDATE ` + cfg.qualified(cfg.QueueTable) + ` SET completed_at = $1, last_error = NULL, builder_pid = NULL WHERE name = $2 AND queued_at = $3`
}

func (cfg AsyncIndexConfig) recordFailureSQL() string {
	return `UPDATE ` + cfg.qualified(cfg.QueueTable) + ` SET attempts = $1, last_error = $2, next_attempt_at = $3, builder_pid = NULL WHERE name = $4 AND queued_at = $5`
}

func (cfg AsyncIndexConfig) progressSQL() string {
	return `SELECT p.phase, p.blocks_done, p.blocks_total, p.tuples_done, p.tuples_total, p.lockers_done, p.lockers_total
		FROM pg_stat_progress_create_index p JOIN ` + cfg.qualified(cfg.QueueTable) + ` q ON q.builder_pid = p.pid WHERE q.name = $1`
}

func (cfg AsyncIndexConfig) runningBuildsSQL() string {
	return `SELECT q.name, q.builder_pid FROM ` + cfg.qualified(cfg.QueueTable) + ` q
		JOIN pg_stat_activity a ON a.pid = q.builder_pid
		WHERE q.completed_at IS NULL AND a.usename = current_user AND a.state = 'active' AND a.query ILIKE '%concurrently%'
		  AND (a.query ILIKE '%' || q.name || '%' OR (q.replaces IS NOT NULL AND a.query ILIKE '%' || q.replaces || '%'))`
}

const asyncIndexLockNamespace = "sam-async-indexes:"

func AsyncIndexLockID(queueTable string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(asyncIndexLockNamespace + queueTable))
	return int64(h.Sum64())
}

func RunAsyncIndexes(ctx context.Context, cfg AsyncIndexConfig) {
	WarnGivenUpAsyncIndexes(ctx, cfg)
	failureBackoff := asyncIndexRetryInterval
	start := time.Now()
	completed := 0
	busyLogged := false
	for {
		pass, err := runAsyncIndexPass(ctx, cfg)
		completed += pass.completed
		if ctx.Err() != nil {
			return
		}
		busy := errors.Is(err, ErrAsyncIndexesBusy)
		if err == nil && pass.retryAt.IsZero() {
			if completed > 0 {
				logInfo(cfg.Logger, "async index queue drained", "queue", cfg.QueueTable, "completed", completed, "duration", time.Since(start).Round(time.Second))
			}
			return
		}
		wait := asyncIndexRetryInterval
		switch {
		case busy:
		case !pass.retryAt.IsZero():
			wait = max(time.Until(pass.retryAt), asyncIndexRetryInterval)
		default:
			wait = failureBackoff
			failureBackoff = min(2*failureBackoff, maxAsyncIndexRetryInterval)
		}
		wait = jitterAsyncIndexWait(wait)
		if cfg.Logger != nil {
			switch {
			case busy:
				level := slog.LevelInfo
				if busyLogged {
					level = slog.LevelDebug
				}
				cfg.Logger.Log(ctx, level, "async index builds held by another process", "queue", cfg.QueueTable, "retryIn", wait)
			case err != nil && pass.failedRows == 0:
				cfg.Logger.Warn("async index pass failed; retrying", "queue", cfg.QueueTable, "error", err, "retryIn", wait)
			default:
				cfg.Logger.Debug("async index builds waiting for a row's next attempt", "queue", cfg.QueueTable, "retryIn", wait)
			}
		}
		busyLogged = busy
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func WarnGivenUpAsyncIndexes(ctx context.Context, cfg AsyncIndexConfig) {
	if cfg.Logger == nil || !identifierPattern.MatchString(cfg.QueueTable) || (cfg.Schema != "" && !identifierPattern.MatchString(cfg.Schema)) {
		return
	}
	names, err := QueryVals[string](ctx, cfg.DB, cfg.givenUpSQL(), MaxAsyncIndexAttempts)
	switch {
	case err != nil:
		cfg.Logger.Warn("list async index builds that gave up", "queue", cfg.QueueTable, "error", err)
	case len(names) > 0:
		cfg.Logger.Warn("async index builds gave up and are skipped until attempts is reset", "queue", cfg.QueueTable, "indexes", names, "maxAttempts", MaxAsyncIndexAttempts)
	}
}

func jitterAsyncIndexWait(wait time.Duration) time.Duration {
	return wait + rand.N(wait/4)
}

func BuildAsyncIndexes(ctx context.Context, cfg AsyncIndexConfig) error {
	_, err := runAsyncIndexPass(ctx, cfg)
	return err
}

type asyncIndexPass struct {
	retryAt    time.Time
	completed  int
	failedRows int
}

func runAsyncIndexPass(ctx context.Context, cfg AsyncIndexConfig) (asyncIndexPass, error) {
	var pass asyncIndexPass
	if !identifierPattern.MatchString(cfg.QueueTable) {
		return pass, fmt.Errorf("async index queue table %q is not a plain identifier", cfg.QueueTable)
	}
	if cfg.Schema != "" && !identifierPattern.MatchString(cfg.Schema) {
		return pass, fmt.Errorf("async index schema %q is not a plain identifier", cfg.Schema)
	}
	queue := cfg.qualified(cfg.QueueTable)
	pending, err := pendingAsyncIndexes(ctx, cfg.DB, cfg)
	if err != nil {
		return pass, err
	}
	due, retryAt := dueAsyncIndexes(pending, time.Now())
	pass.retryAt = retryAt
	if len(due) == 0 {
		return pass, nil
	}

	conn, err := cfg.DB.Conn(ctx)
	if err != nil {
		return pass, fmt.Errorf("async index connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	lockID := AsyncIndexLockID(queue)
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&locked); err != nil {
		return pass, fmt.Errorf("acquire async index lock: %w", err)
	}
	if !locked {
		return pass, ErrAsyncIndexesBusy
	}
	defer func() {
		releaseErr := releaseAsyncIndexSession(context.WithoutCancel(ctx), conn, lockID)
		if releaseErr == nil {
			return
		}
		if cfg.Logger != nil {
			cfg.Logger.Warn("release the async index session; discarding its connection", "queue", cfg.QueueTable, "error", releaseErr)
		}
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}()

	if _, err = conn.ExecContext(ctx, `SET statement_timeout = 0`); err != nil {
		return pass, fmt.Errorf("disable statement_timeout for async index builds: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `SET lock_timeout = 0`); err != nil {
		return pass, fmt.Errorf("disable lock_timeout for async index builds: %w", err)
	}
	pending, err = pendingAsyncIndexes(ctx, conn, cfg)
	if err != nil {
		return pass, err
	}
	due, pass.retryAt = dueAsyncIndexes(pending, time.Now())
	if len(due) > 0 {
		names := make([]string, 0, len(due))
		for _, idx := range due {
			names = append(names, idx.name)
		}
		logInfo(cfg.Logger, "async index operations pending", "queue", cfg.QueueTable, "indexes", names)
	}
	var buildErrs []error
	for _, idx := range due {
		if ctx.Err() != nil {
			buildErrs = append(buildErrs, ctx.Err())
			break
		}
		if err := buildAsyncIndex(ctx, conn, cfg, idx); err != nil {
			if errors.Is(err, errAsyncIndexRequeued) {
				pass.retryAt = earliestTime(pass.retryAt, time.Now())
				continue
			}
			if ctx.Err() != nil {
				logInfo(cfg.Logger, "stopped during an async index operation; Postgres finishes a running build and the next pass records it", "queue", cfg.QueueTable, "index", idx.name)
				buildErrs = append(buildErrs, ctx.Err())
				break
			}
			pass.failedRows++
			if next := recordAsyncIndexFailure(ctx, conn, cfg, idx, err); !next.IsZero() {
				pass.retryAt = earliestTime(pass.retryAt, next)
			}
			buildErrs = append(buildErrs, fmt.Errorf("async index %s: %w", idx.name, err))
			if isAsyncIndexInterrupted(err) {
				break
			}
			continue
		}
		pass.completed++
	}
	return pass, errors.Join(buildErrs...)
}

func releaseAsyncIndexSession(ctx context.Context, conn *sql.Conn, lockID int64) error {
	var errs []error
	if _, err := conn.ExecContext(ctx, `RESET statement_timeout`); err != nil {
		errs = append(errs, fmt.Errorf("reset statement_timeout: %w", err))
	}
	if _, err := conn.ExecContext(ctx, `RESET lock_timeout`); err != nil {
		errs = append(errs, fmt.Errorf("reset lock_timeout: %w", err))
	}
	var unlocked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, lockID).Scan(&unlocked); err != nil {
		errs = append(errs, fmt.Errorf("release async index lock: %w", err))
	} else if !unlocked {
		errs = append(errs, errors.New("release async index lock: advisory lock was not held"))
	}
	return errors.Join(errs...)
}

type asyncIndex struct {
	name          string
	definition    string
	replaces      sql.NullString
	attempts      int
	nextAttemptAt sql.NullInt64
	queuedAt      int64
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func pendingAsyncIndexes(ctx context.Context, q queryer, cfg AsyncIndexConfig) ([]asyncIndex, error) {
	rows, err := q.QueryContext(ctx, cfg.pendingSQL(), MaxAsyncIndexAttempts)
	if err != nil {
		return nil, fmt.Errorf("list pending async indexes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var pending []asyncIndex
	for rows.Next() {
		var idx asyncIndex
		if err := rows.Scan(&idx.name, &idx.definition, &idx.replaces, &idx.attempts, &idx.nextAttemptAt, &idx.queuedAt); err != nil {
			return nil, fmt.Errorf("scan pending async index: %w", err)
		}
		pending = append(pending, idx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending async indexes: %w", err)
	}
	return pending, nil
}

var identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

var AsyncIndexCreatePattern = regexp.MustCompile(`(?is)^\s*CREATE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+([a-z_][a-z0-9_]*)\s+ON\s[^;]*;?\s*$`)

var asyncIndexTableSchemaPattern = regexp.MustCompile(`(?is)^\s*CREATE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+[a-z_][a-z0-9_]*\s+ON\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\.`)

func AsyncIndexTableSchema(definition string) string {
	if m := asyncIndexTableSchemaPattern.FindStringSubmatch(definition); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	return ""
}

var AsyncIndexDropPattern = regexp.MustCompile(`(?is)^\s*DROP\s+INDEX\s+CONCURRENTLY\s+IF\s+EXISTS\s+([a-z_][a-z0-9_]*)\s*;?\s*$`)

func buildAsyncIndex(ctx context.Context, conn *sql.Conn, cfg AsyncIndexConfig, idx asyncIndex) error {
	if idx.replaces.Valid && !identifierPattern.MatchString(idx.replaces.String) {
		return fmt.Errorf("replaced index %q is not a plain identifier", idx.replaces.String)
	}
	if m := AsyncIndexDropPattern.FindStringSubmatch(idx.definition); len(m) > 1 && m[1] == idx.name {
		return runAsyncIndexOperation(ctx, conn, cfg, idx, "dropping async index", "dropped async index", cfg.dropIndexSQL(idx.name))
	}
	m := AsyncIndexCreatePattern.FindStringSubmatch(idx.definition)
	if len(m) < 2 || m[1] != idx.name {
		return fmt.Errorf("definition must be CREATE INDEX CONCURRENTLY IF NOT EXISTS %[1]s ON ... or DROP INDEX CONCURRENTLY IF EXISTS %[1]s", idx.name)
	}
	if cfg.Schema != "" && AsyncIndexTableSchema(idx.definition) != cfg.Schema {
		return fmt.Errorf("definition must name its table as %s.<table>, the schema the queue checks for the index", cfg.Schema)
	}

	exists, valid, err := PostgresIndexState(ctx, conn, cfg.qualified(idx.name))
	if err != nil {
		return err
	}
	statements := []string{idx.definition}
	if exists && !valid {
		logInfo(cfg.Logger, "dropping index left invalid by an interrupted build", "index", idx.name)
		statements = []string{cfg.dropIndexSQL(idx.name), idx.definition}
	}
	return runAsyncIndexOperation(ctx, conn, cfg, idx, "building async index", "built async index", statements...)
}

func runAsyncIndexOperation(ctx context.Context, conn *sql.Conn, cfg AsyncIndexConfig, idx asyncIndex, startMsg, doneMsg string, statements ...string) error {
	start := time.Now()
	started, err := conn.ExecContext(ctx, cfg.markStartedSQL(), start.UnixMilli(), idx.name, idx.queuedAt)
	if err != nil {
		return fmt.Errorf("mark started: %w", err)
	}
	if n, rowsErr := started.RowsAffected(); rowsErr == nil && n == 0 {
		logInfo(cfg.Logger, "skipping an async index operation re-queued since the pass read it", "queue", cfg.QueueTable, "index", idx.name)
		return errAsyncIndexRequeued
	}
	logInfo(cfg.Logger, startMsg, "queue", cfg.QueueTable, "index", idx.name)
	stopWatch := watchAsyncIndexProgress(ctx, cfg, idx, start)
	var runErr error
	for _, statement := range statements {
		if _, runErr = conn.ExecContext(ctx, statement); runErr != nil {
			break
		}
	}
	stopWatch()
	if runErr != nil {
		return fmt.Errorf("run: %w", runErr)
	}
	if idx.replaces.Valid {
		if _, err = conn.ExecContext(ctx, cfg.dropIndexSQL(idx.replaces.String)); err != nil {
			return fmt.Errorf("drop replaced index %s: %w", idx.replaces.String, err)
		}
	}
	done, err := conn.ExecContext(ctx, cfg.markDoneSQL(), time.Now().UnixMilli(), idx.name, idx.queuedAt)
	if err != nil {
		return fmt.Errorf("mark done: %w", err)
	}
	if n, rowsErr := done.RowsAffected(); rowsErr == nil && n == 0 {
		logInfo(cfg.Logger, "async index row re-queued during its operation; leaving it for the next pass", "queue", cfg.QueueTable, "index", idx.name)
		return errAsyncIndexRequeued
	}
	logInfo(cfg.Logger, doneMsg, "queue", cfg.QueueTable, "index", idx.name, "duration", time.Since(start))
	return nil
}

func watchAsyncIndexProgress(ctx context.Context, cfg AsyncIndexConfig, idx asyncIndex, start time.Time) func() {
	watchCtx, stop := context.WithCancel(ctx)
	if cfg.Logger == nil {
		return stop
	}
	interval := cfg.ProgressInterval
	if interval <= 0 {
		interval = defaultAsyncIndexProgressInterval
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				attrs := []any{"queue", cfg.QueueTable, "index", idx.name, "elapsed", time.Since(start).Round(time.Second)}
				cfg.Logger.Info("async index operation in progress", append(attrs, asyncIndexProgressAttrs(watchCtx, cfg, idx.name)...)...)
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}

func asyncIndexProgressAttrs(ctx context.Context, cfg AsyncIndexConfig, name string) []any {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var phase string
	var blocksDone, blocksTotal, tuplesDone, tuplesTotal, lockersDone, lockersTotal int64
	err := cfg.DB.QueryRowContext(lookupCtx, cfg.progressSQL(), name).
		Scan(&phase, &blocksDone, &blocksTotal, &tuplesDone, &tuplesTotal, &lockersDone, &lockersTotal)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return []any{"progressError", err}
	}
	return []any{
		"phase", phase,
		"blocksDone", blocksDone,
		"blocksTotal", blocksTotal,
		"tuplesDone", tuplesDone,
		"tuplesTotal", tuplesTotal,
		"lockersDone", lockersDone,
		"lockersTotal", lockersTotal,
	}
}

func recordAsyncIndexFailure(ctx context.Context, conn *sql.Conn, cfg AsyncIndexConfig, idx asyncIndex, buildErr error) time.Time {
	attempts := idx.attempts + 1
	next := time.Now().Add(asyncIndexBackoff(attempts))
	if isAsyncIndexInterrupted(buildErr) {
		attempts = idx.attempts
		next = time.Now().Add(asyncIndexRetryInterval)
	}
	recorded, err := conn.ExecContext(context.WithoutCancel(ctx), cfg.recordFailureSQL(),
		attempts, truncateAsyncIndexError(buildErr.Error()), next.UnixMilli(), idx.name, idx.queuedAt)
	if err != nil {
		if cfg.Logger != nil {
			cfg.Logger.Warn("record async index failure", "queue", cfg.QueueTable, "index", idx.name, "error", err)
		}
	} else if n, rowsErr := recorded.RowsAffected(); rowsErr == nil && n == 0 {
		logInfo(cfg.Logger, "async index row re-queued during its failed operation; leaving it for the next pass", "queue", cfg.QueueTable, "index", idx.name)
		return time.Now()
	}
	switch {
	case isAsyncIndexInterrupted(buildErr):
		logInfo(cfg.Logger, "async index operation interrupted by a migration or an operator; retrying", "queue", cfg.QueueTable, "index", idx.name, "error", buildErr, "retryIn", time.Until(next).Round(time.Second))
	case attempts >= MaxAsyncIndexAttempts:
		if cfg.Logger != nil {
			cfg.Logger.Error("async index build gave up after repeated failures", "queue", cfg.QueueTable, "index", idx.name, "attempts", attempts, "error", buildErr)
		}
		return time.Time{}
	case cfg.Logger != nil:
		cfg.Logger.Warn("async index build failed; retrying", "queue", cfg.QueueTable, "index", idx.name, "attempts", attempts, "error", buildErr, "retryIn", time.Until(next).Round(time.Second))
	}
	return next
}

func isAsyncIndexInterrupted(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && (pgErr.Code == "57014" || pgErr.Code == "40P01")
}

func asyncIndexBackoff(attempts int) time.Duration {
	backoff := asyncIndexRetryInterval
	for i := 1; i < attempts && backoff < maxAsyncIndexRetryInterval; i++ {
		backoff *= 2
	}
	return min(backoff, maxAsyncIndexRetryInterval)
}

func dueAsyncIndexes(pending []asyncIndex, now time.Time) (due []asyncIndex, retryAt time.Time) {
	for _, idx := range pending {
		if idx.nextAttemptAt.Valid && idx.nextAttemptAt.Int64 > now.UnixMilli() {
			retryAt = earliestTime(retryAt, time.UnixMilli(idx.nextAttemptAt.Int64))
			continue
		}
		due = append(due, idx)
	}
	return due, retryAt
}

func earliestTime(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

func truncateAsyncIndexError(msg string) string {
	if len(msg) <= maxAsyncIndexErrorLen {
		return msg
	}
	cut := maxAsyncIndexErrorLen
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut]
}

func logInfo(logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.Info(msg, args...)
	}
}

type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func PostgresIndexState(ctx context.Context, q RowQuerier, name string) (exists, valid bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass($1)`, name).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("look up index %s: %w", name, err)
	}
	return true, valid, nil
}
