// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	defaultPostgresPoolSize    = 25
	defaultPostgresMaxOverflow = 40
)

type DB struct {
	*sql.DB
	dialect     string
	logger      *slog.Logger
	postgresSQL bool
}

func Open(databaseURL string, logger *slog.Logger, opts ...OpenOption) (*DB, error) {
	dialect, dsn, err := parseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	return openDSN(dialect, dsn, logger, opts...)
}

var sqliteURIPathEscaper = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

func OpenSQLiteFile(path string, logger *slog.Logger, opts ...OpenOption) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path %q: %w", path, err)
	}
	return openDSN("sqlite", "file:"+sqliteURIPathEscaper.Replace(abs), logger, opts...)
}

func openDSN(dialect, dsn string, logger *slog.Logger, opts ...OpenOption) (*DB, error) {
	var err error
	var o openOptions
	for _, opt := range opts {
		opt(&o)
	}
	if dialect == "sqlite" {
		dsn = sqliteDSN(dsn, o)
		registerSQLiteAttachments(dsn, o.sqliteAttachments)
	}

	var db *sql.DB
	if dialect == "pgx" {
		db, err = openPostgres(dsn, o.poolName)
		if err != nil {
			return nil, err
		}
		dialect = "postgres"
	} else {
		db, err = sql.Open(dialect, dsn)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
	}

	switch dialect {
	case "sqlite":
		db.SetMaxOpenConns(1)
		if err := enableSQLiteWAL(db); err != nil {
			_ = db.Close()
			return nil, err
		}
		for _, p := range []struct{ sql, desc string }{
			{"PRAGMA synchronous=NORMAL", "set synchronous"},
			{"PRAGMA cache_size=-64000", "set cache_size"},
			{"PRAGMA mmap_size=268435456", "set mmap_size"},
			{"PRAGMA wal_autocheckpoint=1000", "set wal_autocheckpoint"},
		} {
			if _, err := db.Exec(p.sql); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("%s: %w", p.desc, err)
			}
		}
	case "postgres":
		poolSize := envIntOrDefault("PLATFORM_DB_POOL_SIZE", defaultPostgresPoolSize, logger)
		maxOverflow := envIntOrDefault("PLATFORM_DB_MAX_OVERFLOW", defaultPostgresMaxOverflow, logger)
		sizePostgresPool(db, poolSize, poolSize+maxOverflow)
	}

	if err := pingOrClose(context.Background(), db); err != nil {
		return nil, err
	}
	if dialect == "postgres" {
		trackPool(db, o.poolName)
	}
	return &DB{DB: db, dialect: dialect, logger: logger, postgresSQL: dialect == "sqlite" && o.postgresSQL}, nil
}

const openPingTimeout = 5 * time.Second

func sqliteDSN(dsn string, o openOptions) string {
	params := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if o.sqliteImmediateWrites {
		params += "&_txlock=immediate"
	}
	if o.sqliteQueryOnly {
		params += "&_pragma=query_only(1)"
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&" + params
	}
	return dsn + "?" + params
}

func sizePostgresPool(db *sql.DB, idle, open int) {
	db.SetMaxIdleConns(idle)
	db.SetMaxOpenConns(open)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
}

func pingOrClose(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, openPingTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

const walSwitchAttempts = 5

func enableSQLiteWAL(db *sql.DB) error {
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal_mode: %w", err)
	}
	if strings.EqualFold(mode, "wal") {
		return nil
	}
	var err error
	for attempt := range walSwitchAttempts {
		if _, err = db.Exec("PRAGMA journal_mode=WAL"); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		if scanErr := db.QueryRow("PRAGMA journal_mode").Scan(&mode); scanErr == nil && strings.EqualFold(mode, "wal") {
			return nil
		}
	}
	return fmt.Errorf("enable WAL: %w", err)
}

func envIntOrDefault(name string, def int, logger *slog.Logger) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n <= 0 {
		if logger != nil {
			logger.Warn("dbutil: ignoring invalid env override; using default",
				"env", name, "value", raw, "default", def)
		}
		return def
	}
	return n
}

func (db *DB) Dialect() string {
	return db.dialect
}

func (db *DB) Logger() *slog.Logger {
	return db.logger
}

func (db *DB) RawDB() *sql.DB {
	return db.DB
}

func (db *DB) Rebind(query string) string {
	if db.postgresSQL {
		query = postgresToSQLite(query)
	}
	return rebind(db.dialect, query)
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, db.Rebind(query), args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, db.Rebind(query), args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, db.Rebind(query), args...)
}

func (db *DB) BeginTx(ctx context.Context) (*Tx, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return &Tx{Tx: tx, dialect: db.dialect, postgresSQL: db.postgresSQL}, nil
}

func (db *DB) WithReadOnlyTx(ctx context.Context, fn func(tx *Tx) error) error {
	return db.withReadOnly(ctx, sql.LevelDefault, fn)
}

func (db *DB) WithReadOnlySnapshot(ctx context.Context, fn func(tx *Tx) error) error {
	return db.withReadOnly(ctx, sql.LevelRepeatableRead, fn)
}

func (db *DB) WithBoundedReadSnapshot(ctx context.Context, timeout time.Duration, fn func(tx *Tx) error) error {
	if db.dialect == "sqlite" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, max(timeout, time.Millisecond))
		defer cancel()
	}
	return db.WithReadOnlySnapshot(ctx, func(tx *Tx) error {
		if db.dialect == "postgres" {
			if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = `+strconv.FormatInt(max(timeout.Milliseconds(), 1), 10)); err != nil {
				return fmt.Errorf("set statement timeout: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `SET LOCAL plan_cache_mode = force_custom_plan`); err != nil {
				return fmt.Errorf("set plan cache mode: %w", err)
			}
		}
		return fn(tx)
	})
}

func (db *DB) withReadOnly(ctx context.Context, isolation sql.IsolationLevel, fn func(tx *Tx) error) error {
	tx, err := db.DB.BeginTx(ctx, &sql.TxOptions{Isolation: isolation, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read-only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(&Tx{Tx: tx, dialect: db.dialect, postgresSQL: db.postgresSQL})
}

func (db *DB) WithBoundedTx(ctx context.Context, timeout time.Duration, fn func(tx *Tx) error) error {
	return db.WithTx(ctx, func(tx *Tx) error {
		if db.dialect == "postgres" {
			if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = `+strconv.FormatInt(max(timeout.Milliseconds(), 1), 10)); err != nil {
				return fmt.Errorf("set statement timeout: %w", err)
			}
		}
		return fn(tx)
	})
}

func (db *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	dtx := &Tx{Tx: tx, dialect: db.dialect, postgresSQL: db.postgresSQL}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		}
	}()
	if err := fn(dtx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && db.logger != nil {
			db.logger.Error("rollback failed", "error", rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func RedactDSN(databaseURL string) string {
	if databaseURL == "" {
		return ""
	}
	if strings.HasPrefix(databaseURL, "sqlite:") {
		return redactSQLiteDSN(databaseURL)
	}
	scheme, _, hasSeparator := strings.Cut(databaseURL, "://")
	if !hasSeparator || !looksLikeURLScheme(scheme) {
		return "<unparseable database URL>"
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return scheme + "://<unparseable>"
	}
	u.Fragment, u.RawFragment = "", ""
	redactSecretQueryParams(u)
	return u.Redacted()
}

func redactSQLiteDSN(databaseURL string) string {
	withoutFragment, _, _ := strings.Cut(databaseURL, "#")
	path, rawQuery, hasQuery := strings.Cut(withoutFragment, "?")
	path = maskSQLiteUserinfo(path)
	if !hasQuery {
		return path
	}
	u := &url.URL{RawQuery: rawQuery}
	redactSecretQueryParams(u)
	return path + "?" + u.RawQuery
}

func maskSQLiteUserinfo(path string) string {
	rest, hasAuthority := strings.CutPrefix(path, "sqlite://")
	if !hasAuthority {
		return path
	}
	authority, _, _ := strings.Cut(rest, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return path
	}
	return strings.Replace(path, authority, "xxxxx@"+authority[at+1:], 1)
}

func looksLikeURLScheme(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		isTrailer := r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'
		if valid := isLetter || i > 0 && isTrailer; !valid {
			return false
		}
	}
	return true
}

var publishableQueryParams = map[string]bool{
	"application_name":          true,
	"cache":                     true,
	"channel_binding":           true,
	"client_encoding":           true,
	"connect_timeout":           true,
	"dbname":                    true,
	"default_query_exec_mode":   true,
	"fallback_application_name": true,
	"host":                      true,
	"hostaddr":                  true,
	"immutable":                 true,
	"mode":                      true,
	"pool_max_conn_idle_time":   true,
	"pool_max_conn_lifetime":    true,
	"pool_max_conns":            true,
	"pool_min_conns":            true,
	"port":                      true,
	"search_path":               true,
	"sslcert":                   true,
	"sslmode":                   true,
	"sslnegotiation":            true,
	"sslrootcert":               true,
	"target_session_attrs":      true,
	"user":                      true,
}

func redactSecretQueryParams(u *url.URL) {
	if u.RawQuery == "" {
		return
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		u.RawQuery = "<redacted>"
		return
	}
	masked := false
	for key := range q {
		if publishableQueryParams[strings.ToLower(key)] {
			continue
		}
		q.Set(key, "xxxxx")
		masked = true
	}
	if masked {
		u.RawQuery = q.Encode()
	}
}

func postgresKeepAliveConfig() net.KeepAliveConfig {
	return net.KeepAliveConfig{
		Enable:   true,
		Idle:     10 * time.Second,
		Interval: 5 * time.Second,
		Count:    3,
	}
}

func openPostgres(dsn, poolName string) (*sql.DB, error) {
	cfg, err := buildPostgresConfig(dsn)
	if err != nil {
		return nil, err
	}
	setApplicationName(cfg, poolName)
	connStr := stdlib.RegisterConnConfig(cfg)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

func buildPostgresConfig(dsn string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		safeMsg := strings.ReplaceAll(err.Error(), dsn, RedactDSN(dsn))
		return nil, fmt.Errorf("parse postgres config: %s", safeMsg)
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{KeepAliveConfig: postgresKeepAliveConfig()}
		return d.DialContext(ctx, network, addr)
	}
	return cfg, nil
}

func parseURL(url string) (dialect, dsn string, err error) {
	switch {
	case strings.HasPrefix(url, "sqlite:///"):
		path := strings.TrimPrefix(url, "sqlite:///")
		if path == "" {
			return "", "", fmt.Errorf("empty sqlite path in URL")
		}
		if path == ":memory:" || strings.HasPrefix(path, ":memory:") {
			name := strings.TrimPrefix(path, ":memory:")
			if name == "" {
				return "sqlite", "file::memory:?cache=shared", nil
			}
			return "sqlite", "file:" + name + "?mode=memory&cache=shared", nil
		}
		if strings.HasPrefix(path, "/") {
			path = "/" + strings.TrimLeft(path, "/")
		} else {
			abs, absErr := filepath.Abs(path)
			if absErr != nil {
				return "", "", fmt.Errorf("resolve sqlite path %q: %w", path, absErr)
			}
			path = abs
		}
		if dir := filepath.Dir(path); dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		return "sqlite", "file:" + path, nil
	case strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://"):
		return "pgx", url, nil
	default:
		scheme, _, ok := strings.Cut(url, "://")
		if !ok {
			return "", "", fmt.Errorf("unsupported database URL: missing scheme")
		}
		return "", "", fmt.Errorf("unsupported database URL scheme: %q", scheme)
	}
}
