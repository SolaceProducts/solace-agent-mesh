// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const defaultMigrationLockTimeout = 15 * time.Second

const defaultMigrationProgressInterval = 10 * time.Second

type MigrateConfig struct {
	DB               *sql.DB
	Dialect          string
	TableName        string
	SQLiteFS         fs.FS
	PostgresFS       fs.FS
	Logger           *slog.Logger
	LockTimeout      time.Duration
	ProgressInterval time.Duration
	AsyncIndexQueue  AsyncIndexConfig
}

type lockTimeoutLocker struct {
	lock.SessionLocker
	timeout time.Duration
	session *migrationSession
}

func (l lockTimeoutLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "SELECT set_config('lock_timeout', $1, false)", strconv.FormatInt(l.timeout.Milliseconds(), 10)); err != nil {
		return fmt.Errorf("set migration lock_timeout: %w", err)
	}
	if l.session != nil {
		var pid int
		if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return fmt.Errorf("read migration backend pid: %w", err)
		}
		l.session.pid.Store(int64(pid))
	}
	if err := l.SessionLocker.SessionLock(ctx, conn); err != nil {
		_, resetErr := conn.ExecContext(context.WithoutCancel(ctx), "RESET lock_timeout")
		return errors.Join(err, resetErr)
	}
	return nil
}

func (l lockTimeoutLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	unlockErr := l.SessionLocker.SessionUnlock(ctx, conn)
	_, resetErr := conn.ExecContext(ctx, "RESET lock_timeout")
	return errors.Join(unlockErr, resetErr)
}

func gooseDialect(dialect string) (goose.Dialect, error) {
	switch dialect {
	case "sqlite":
		return goose.DialectSQLite3, nil
	case "postgres":
		return goose.DialectPostgres, nil
	default:
		return "", fmt.Errorf("unsupported migration dialect: %q (supported: sqlite, postgres)", dialect)
	}
}

func selectFS(cfg MigrateConfig) (fs.FS, error) {
	switch cfg.Dialect {
	case "sqlite":
		return cfg.SQLiteFS, nil
	case "postgres":
		return cfg.PostgresFS, nil
	default:
		return nil, fmt.Errorf("no migration FS for dialect: %q", cfg.Dialect)
	}
}

func newProvider(cfg MigrateConfig, lockTimeout time.Duration, session *migrationSession) (*goose.Provider, error) {
	dialect, err := gooseDialect(cfg.Dialect)
	if err != nil {
		return nil, err
	}
	migrations, err := selectFS(cfg)
	if err != nil {
		return nil, err
	}

	opts := []goose.ProviderOption{
		goose.WithTableName(cfg.TableName),
	}

	if cfg.Dialect == "postgres" {
		locker, lockerErr := lock.NewPostgresSessionLocker()
		if lockerErr != nil {
			if cfg.Logger != nil {
				cfg.Logger.Warn("session locker unavailable, proceeding without advisory lock", "error", lockerErr)
			}
		} else {
			opts = append(opts, goose.WithSessionLocker(lockTimeoutLocker{SessionLocker: locker, timeout: lockTimeout, session: session}))
		}
	}

	provider, err := goose.NewProvider(dialect, cfg.DB, migrations, opts...)
	if err != nil {
		return nil, fmt.Errorf("create goose provider (table=%s, dialect=%s): %w", cfg.TableName, cfg.Dialect, err)
	}
	return provider, nil
}

type MigrationSet struct {
	FS           fs.FS
	VersionTable string
	SQLiteDir    string
	PostgresDir  string
}

func MigrateConfigFor(db *DB, set MigrationSet) (MigrateConfig, error) {
	sqliteFS, err := fs.Sub(set.FS, set.SQLiteDir)
	if err != nil {
		return MigrateConfig{}, fmt.Errorf("sqlite migrations %q: %w", set.SQLiteDir, err)
	}
	postgresFS, err := fs.Sub(set.FS, set.PostgresDir)
	if err != nil {
		return MigrateConfig{}, fmt.Errorf("postgres migrations %q: %w", set.PostgresDir, err)
	}
	return MigrateConfig{
		DB:         db.RawDB(),
		Dialect:    db.Dialect(),
		TableName:  set.VersionTable,
		SQLiteFS:   sqliteFS,
		PostgresFS: postgresFS,
		Logger:     db.Logger(),
	}, nil
}

func MigrateSet(ctx context.Context, db *DB, set MigrationSet) error {
	cfg, err := MigrateConfigFor(db, set)
	if err != nil {
		return err
	}
	return Migrate(ctx, cfg)
}

func Migrate(ctx context.Context, cfg MigrateConfig) error {
	lister, err := newProvider(cfg, 0, nil)
	if err != nil {
		return err
	}
	pending, current, err := pendingMigrations(ctx, lister)
	if err != nil {
		return fmt.Errorf("list pending migrations (table=%s): %w", cfg.TableName, err)
	}
	if cfg.Logger != nil {
		if len(pending) > 0 {
			cfg.Logger.Info("applying pending migrations", "table", cfg.TableName, "from", current, "versions", versionsOf(pending))
		} else {
			cfg.Logger.Info("migrations up to date", "table", cfg.TableName, "version", current)
		}
	}
	start := time.Now()

	segments, err := lockTimeoutSegments(cfg, pending)
	if err != nil {
		return err
	}
	for _, seg := range segments {
		blockers, err := applyMigrationSegment(ctx, cfg, seg)
		if IsDeadlock(err) {
			if cfg.Logger != nil {
				cfg.Logger.Warn("migration aborted by a deadlock; retrying it once", "table", cfg.TableName, "versions", seg.versions, "error", err)
			}
			blockers, err = applyMigrationSegment(ctx, cfg, seg)
		}
		if err == nil {
			continue
		}
		if !isLockNotAvailable(err) {
			return err
		}
		cancelled, cancelErr := cancelRunningAsyncIndexBuilds(ctx, cfg, blockers)
		if cancelErr != nil || cancelled == 0 {
			return errors.Join(err, cancelErr)
		}
		if _, err := applyMigrationSegment(ctx, cfg, seg); err != nil {
			return err
		}
	}
	if len(pending) > 0 && cfg.Logger != nil {
		cfg.Logger.Info("migrations applied", "table", cfg.TableName, "count", len(pending), "duration", time.Since(start))
	}
	return nil
}

func applyMigrationSegment(ctx context.Context, cfg MigrateConfig, seg migrationSegment) ([]int, error) {
	session := &migrationSession{}
	provider, err := newProvider(cfg, seg.lockTimeout, session)
	if err != nil {
		return nil, err
	}
	stopWatch := watchMigrationProgress(ctx, cfg, seg, session, time.Now())
	results, migrateErr := provider.UpTo(ctx, seg.upTo)
	failedAt := time.Now()
	stopWatch()
	if cfg.Logger != nil {
		for _, r := range results {
			if r.Error != nil {
				cfg.Logger.Warn("migration failed", "version", r.Source.Version, "error", r.Error)
			} else {
				cfg.Logger.Info("applied migration", "table", cfg.TableName, "version", r.Source.Version, "duration", r.Duration)
			}
		}
	}
	if migrateErr != nil {
		return session.blockingPIDsSince(failedAt.Add(-seg.lockTimeout)), fmt.Errorf("goose up (table=%s, versions=%v): %w", cfg.TableName, seg.versions, migrateErr)
	}
	return nil, nil
}

type migrationSession struct {
	pid atomic.Int64

	mu       sync.Mutex
	blockers map[int]time.Time
}

func (s *migrationSession) addBlockers(pids []int, sampledAt time.Time) {
	if len(pids) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockers == nil {
		s.blockers = make(map[int]time.Time, len(pids))
	}
	for _, pid := range pids {
		s.blockers[pid] = sampledAt
	}
}

func (s *migrationSession) blockingPIDsSince(since time.Time) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	pids := make([]int, 0, len(s.blockers))
	for pid, sampledAt := range s.blockers {
		if sampledAt.After(since) {
			pids = append(pids, pid)
		}
	}
	return pids
}

const maxBlockerSampleInterval = time.Second

func blockerSampleInterval(lockTimeout time.Duration) time.Duration {
	return min(maxBlockerSampleInterval, max(lockTimeout/4, 10*time.Millisecond))
}

func watchMigrationProgress(ctx context.Context, cfg MigrateConfig, seg migrationSegment, session *migrationSession, start time.Time) func() {
	watchCtx, stop := context.WithCancel(ctx)
	sampling := cfg.Dialect == "postgres" && seg.lockTimeout > 0 && cfg.AsyncIndexQueue.QueueTable != ""
	if cfg.Logger == nil && !sampling {
		return stop
	}
	interval := cfg.ProgressInterval
	if interval <= 0 {
		interval = defaultMigrationProgressInterval
	}
	var tickers []*time.Ticker
	var sampleC, progressC <-chan time.Time
	if sampling {
		sampler := time.NewTicker(blockerSampleInterval(seg.lockTimeout))
		tickers = append(tickers, sampler)
		sampleC = sampler.C
	}
	if cfg.Logger != nil {
		progress := time.NewTicker(interval)
		tickers = append(tickers, progress)
		progressC = progress.C
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			for _, t := range tickers {
				t.Stop()
			}
		}()
		sampleFailed := false
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-sampleC:
				pid := int(session.pid.Load())
				if pid == 0 {
					continue
				}
				sampledAt := time.Now()
				pids, err := blockingPIDs(watchCtx, cfg.DB, pid)
				if err != nil {
					if watchCtx.Err() == nil && !sampleFailed && cfg.Logger != nil {
						cfg.Logger.Warn("sample the sessions blocking a migration; a lock timeout will cancel no async index build", "table", cfg.TableName, "error", err)
					}
					sampleFailed = true
					continue
				}
				session.addBlockers(pids, sampledAt)
			case <-progressC:
				attrs := []any{"table", cfg.TableName, "versions", seg.versions, "elapsed", time.Since(start).Round(time.Second)}
				if cfg.Dialect == "postgres" {
					attrs = append(attrs, migrationActivityAttrs(watchCtx, cfg.DB, int(session.pid.Load()))...)
				}
				cfg.Logger.Warn("migration still running", attrs...)
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}

func blockingPIDs(ctx context.Context, db *sql.DB, pid int) ([]int, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return QueryVals[int](lookupCtx, db, `SELECT unnest(pg_blocking_pids($1))`, pid)
}

const gooseLockMatch = `l.locktype = 'advisory' AND l.granted AND l.objsubid = 1
	  AND l.classid::bigint = ($2::bigint >> 32) & 4294967295 AND l.objid::bigint = $2::bigint & 4294967295`

const migrationSessionSQL = `SELECT left(coalesce(a.query, ''), 200), coalesce(a.state, ''), coalesce(a.wait_event_type, ''), coalesce(a.wait_event, ''),
	coalesce(extract(epoch FROM now() - a.query_start), 0)::float8,
	EXISTS (SELECT 1 FROM pg_locks l WHERE l.pid = a.pid AND ` + gooseLockMatch + `)
	FROM pg_stat_activity a WHERE a.pid = $1`

const migrationLockHolderSQL = `SELECT a.pid, coalesce(a.application_name, ''), coalesce(a.state, ''), left(coalesce(a.query, ''), 200)
	FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
	WHERE a.pid <> $1 AND a.datname = current_database() AND ` + gooseLockMatch

type migrationLockHolder struct {
	PID             int
	ApplicationName string
	State           string
	Statement       string
}

func migrationActivityAttrs(ctx context.Context, db *sql.DB, pid int) []any {
	if pid == 0 {
		return []any{"activity", "opening the migration connection"}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var statement, state, waitEventType, waitEvent string
	var statementSeconds float64
	var holdsLock bool
	err := db.QueryRowContext(lookupCtx, migrationSessionSQL, pid, lock.DefaultLockID).
		Scan(&statement, &state, &waitEventType, &waitEvent, &statementSeconds, &holdsLock)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return []any{"pid", pid, "activity", "migration session not found"}
	case err != nil:
		return []any{"pid", pid, "activityError", err}
	case !holdsLock:
		return append([]any{"pid", pid}, migrationLockHolderAttrs(lookupCtx, db, pid)...)
	}
	attrs := []any{
		"pid", pid,
		"statement", statement,
		"state", state,
		"waitEventType", waitEventType,
		"waitEvent", waitEvent,
		"statementElapsed", time.Duration(statementSeconds * float64(time.Second)).Round(time.Second),
	}
	if waitEventType == "Lock" {
		sessions, err := blockingSessions(lookupCtx, db, pid)
		if err != nil {
			return append(attrs, "blockedByError", err)
		}
		attrs = append(attrs, "blockedBy", sessions)
	}
	return attrs
}

func migrationLockHolderAttrs(ctx context.Context, db *sql.DB, pid int) []any {
	var h migrationLockHolder
	err := db.QueryRowContext(ctx, migrationLockHolderSQL, pid, lock.DefaultLockID).
		Scan(&h.PID, &h.ApplicationName, &h.State, &h.Statement)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return []any{"activity", "migration lock is free; goose is retrying it"}
	case err != nil:
		return []any{"activityError", err}
	}
	return []any{"activity", "waiting for the migration lock", "lockHolder", h}
}

const blockingSessionsSQL = `SELECT b.pid, coalesce(b.usename::text, ''), coalesce(b.application_name, ''), coalesce(b.state, ''),
	coalesce(extract(epoch FROM now() - b.xact_start), 0)::float8
	FROM pg_stat_activity b WHERE b.pid = ANY(pg_blocking_pids($1))`

type blockingSession struct {
	PID             int
	User            string
	ApplicationName string
	State           string
	TransactionAge  time.Duration
}

func blockingSessions(ctx context.Context, db *sql.DB, pid int) ([]blockingSession, error) {
	rows, err := db.QueryContext(ctx, blockingSessionsSQL, pid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var sessions []blockingSession
	for rows.Next() {
		var s blockingSession
		var xactSeconds float64
		if err := rows.Scan(&s.PID, &s.User, &s.ApplicationName, &s.State, &xactSeconds); err != nil {
			return sessions, err
		}
		s.TransactionAge = time.Duration(xactSeconds * float64(time.Second)).Round(time.Second)
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func isLockNotAvailable(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "55P03"
}

func cancelRunningAsyncIndexBuilds(ctx context.Context, cfg MigrateConfig, blockers []int) (int, error) {
	queue := cfg.AsyncIndexQueue
	if cfg.Dialect != "postgres" || queue.QueueTable == "" {
		return 0, nil
	}
	if !identifierPattern.MatchString(queue.QueueTable) || (queue.Schema != "" && !identifierPattern.MatchString(queue.Schema)) {
		return 0, fmt.Errorf("async index queue %q is not a plain identifier", queue.qualified(queue.QueueTable))
	}
	var exists bool
	if err := cfg.DB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, queue.qualified(queue.QueueTable)).Scan(&exists); err != nil {
		return 0, fmt.Errorf("look up async index queue: %w", err)
	}
	if !exists {
		return 0, nil
	}
	builds, err := runningAsyncIndexBuilds(ctx, cfg.DB, queue)
	if err != nil {
		return 0, err
	}
	cancelled := 0
	for _, b := range builds {
		if !slices.Contains(blockers, b.pid) {
			continue
		}
		var ok bool
		if err := cfg.DB.QueryRowContext(ctx, `SELECT pg_cancel_backend($1)`, b.pid).Scan(&ok); err != nil {
			return cancelled, fmt.Errorf("cancel async index build %s on backend %d: %w", b.name, b.pid, err)
		}
		if !ok {
			continue
		}
		cancelled++
		if cfg.Logger != nil {
			cfg.Logger.Warn("cancelled a running async index build so a migration can take its lock", "queue", queue.QueueTable, "index", b.name, "pid", b.pid)
		}
	}
	return cancelled, nil
}

type runningAsyncIndexBuild struct {
	name string
	pid  int
}

func runningAsyncIndexBuilds(ctx context.Context, db *sql.DB, queue AsyncIndexConfig) ([]runningAsyncIndexBuild, error) {
	rows, err := db.QueryContext(ctx, queue.runningBuildsSQL())
	if err != nil {
		return nil, fmt.Errorf("list running async index builds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var builds []runningAsyncIndexBuild
	for rows.Next() {
		var b runningAsyncIndexBuild
		if err := rows.Scan(&b.name, &b.pid); err != nil {
			return nil, fmt.Errorf("scan running async index build: %w", err)
		}
		builds = append(builds, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list running async index builds: %w", err)
	}
	return builds, nil
}

type migrationSegment struct {
	lockTimeout time.Duration
	upTo        int64
	versions    []int64
}

func lockTimeoutSegments(cfg MigrateConfig, pending []*goose.Source) ([]migrationSegment, error) {
	configured := cfg.LockTimeout
	if configured <= 0 {
		configured = defaultMigrationLockTimeout
	}
	if cfg.Dialect != "postgres" {
		return []migrationSegment{{upTo: goose.MaxVersion}}, nil
	}
	migrations, err := selectFS(cfg)
	if err != nil {
		return nil, err
	}
	var segments []migrationSegment
	for _, src := range pending {
		body, readErr := fs.ReadFile(migrations, src.Path)
		if readErr != nil {
			return nil, fmt.Errorf("read migration %d: %w", src.Version, readErr)
		}
		timeout := configured
		if runsConcurrentIndexOperation(body) {
			timeout = 0
		}
		if n := len(segments); n > 0 && segments[n-1].lockTimeout == timeout {
			segments[n-1].upTo = src.Version
			segments[n-1].versions = append(segments[n-1].versions, src.Version)
			continue
		}
		segments = append(segments, migrationSegment{lockTimeout: timeout, upTo: src.Version, versions: []int64{src.Version}})
	}
	if len(segments) == 0 {
		return []migrationSegment{{lockTimeout: configured, upTo: goose.MaxVersion}}, nil
	}
	segments[len(segments)-1].upTo = goose.MaxVersion
	return segments, nil
}

func pendingMigrations(ctx context.Context, provider *goose.Provider) ([]*goose.Source, int64, error) {
	current, _, err := provider.GetVersions(ctx)
	if err != nil {
		return nil, 0, err
	}
	var pending []*goose.Source
	for _, src := range provider.ListSources() {
		if src.Version > current {
			pending = append(pending, src)
		}
	}
	return pending, current, nil
}

func versionsOf(sources []*goose.Source) []int64 {
	versions := make([]int64, 0, len(sources))
	for _, src := range sources {
		versions = append(versions, src.Version)
	}
	return versions
}

func runsConcurrentIndexOperation(body []byte) bool {
	noTransaction, inUp, concurrent := false, false, false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			if strings.Contains(trimmed, "+goose") {
				switch strings.ToLower(strings.TrimSpace(strings.Replace(strings.ReplaceAll(trimmed, "--", ""), "+goose", "", 1))) {
				case "no transaction":
					noTransaction = true
				case "up":
					inUp = true
				case "down":
					inUp = false
				}
			}
			continue
		}
		if inUp && strings.Contains(strings.ToLower(trimmed), "concurrently") {
			concurrent = true
		}
	}
	return noTransaction && concurrent
}

func TruncateTables(ctx context.Context, db *DB, tables []string) error {
	if len(tables) == 0 {
		return nil
	}
	switch db.Dialect() {
	case "postgres":
		query := "TRUNCATE " + strings.Join(tables, ", ") + " CASCADE"
		_, err := db.ExecContext(ctx, query)
		return err
	default:
		for _, t := range tables {
			if _, err := db.ExecContext(ctx, "DELETE FROM "+t); err != nil {
				return fmt.Errorf("delete from %s: %w", t, err)
			}
		}
		return nil
	}
}

func MigrateUpTo(ctx context.Context, cfg MigrateConfig, version int64) error {
	provider, err := newProvider(cfg, 0, nil)
	if err != nil {
		return err
	}

	results, upErr := provider.UpTo(ctx, version)
	if cfg.Logger != nil {
		for _, r := range results {
			if r.Error != nil {
				cfg.Logger.Warn("migration failed", "version", r.Source.Version, "error", r.Error)
			}
		}
	}
	if upErr != nil {
		return fmt.Errorf("goose up-to %d (table=%s): %w", version, cfg.TableName, upErr)
	}
	return nil
}

func MigrateDownTo(ctx context.Context, cfg MigrateConfig, version int64) error {
	provider, err := newProvider(cfg, 0, nil)
	if err != nil {
		return err
	}

	results, downErr := provider.DownTo(ctx, version)
	if cfg.Logger != nil {
		for _, r := range results {
			if r.Error != nil {
				cfg.Logger.Warn("rollback failed", "version", r.Source.Version, "error", r.Error)
			}
		}
	}
	if downErr != nil {
		return fmt.Errorf("goose down-to %d (table=%s): %w", version, cfg.TableName, downErr)
	}
	return nil
}

func MigrateDown(ctx context.Context, cfg MigrateConfig) error {
	provider, err := newProvider(cfg, 0, nil)
	if err != nil {
		return err
	}

	results, downErr := provider.DownTo(ctx, 0)
	if cfg.Logger != nil {
		for _, r := range results {
			if r.Error != nil {
				cfg.Logger.Warn("rollback failed", "version", r.Source.Version, "error", r.Error)
			}
		}
	}

	if downErr != nil {
		return fmt.Errorf("goose down-to-0 (table=%s): %w", cfg.TableName, downErr)
	}
	return nil
}
