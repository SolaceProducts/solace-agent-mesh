// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

type OpenOption func(*openOptions)

type openOptions struct {
	sqliteAttachments     []string
	sqliteImmediateWrites bool
	sqliteQueryOnly       bool
	postgresSQL           bool
	poolName              string
}

func WithSQLiteAttachment(name string) OpenOption {
	return func(o *openOptions) { o.sqliteAttachments = append(o.sqliteAttachments, name) }
}

func WithSQLiteImmediateWrites() OpenOption {
	return func(o *openOptions) { o.sqliteImmediateWrites = true }
}

func WithSQLiteQueryOnly() OpenOption {
	return func(o *openOptions) { o.sqliteQueryOnly = true }
}

func SQLiteAttachmentPath(mainPath, name string) string {
	ext := filepath.Ext(mainPath)
	return strings.TrimSuffix(mainPath, ext) + "." + name + ext
}

var (
	sqliteAttachMu    sync.Mutex
	sqliteAttachByDSN = map[string][]string{}
)

func init() {
	sqlite.RegisterConnectionHook(attachOnConnect)
}

func registerSQLiteAttachments(dsn string, names []string) {
	if len(names) == 0 || sqliteInMemory(dsn) {
		return
	}
	sqliteAttachMu.Lock()
	defer sqliteAttachMu.Unlock()
	merged := sqliteAttachByDSN[dsn]
	for _, n := range names {
		if !slices.Contains(merged, n) {
			merged = append(merged, n)
		}
	}
	sqliteAttachByDSN[dsn] = merged
}

func sqliteInMemory(dsn string) bool {
	return strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
}

func attachOnConnect(conn sqlite.ExecQuerierContext, dsn string) error {
	sqliteAttachMu.Lock()
	names := sqliteAttachByDSN[dsn]
	sqliteAttachMu.Unlock()
	mainPath := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(mainPath, '?'); i >= 0 {
		mainPath = mainPath[:i]
	}
	for _, name := range names {
		if err := attachSQLite(context.Background(), conn, SQLiteAttachmentPath(mainPath, name), name); err != nil {
			return fmt.Errorf("attach %s: %w", name, err)
		}
	}
	return nil
}

func attachSQLite(ctx context.Context, conn sqlite.ExecQuerierContext, path, name string) error {
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS "+name, []driver.NamedValue{{Ordinal: 1, Value: path}}); err != nil {
		return err
	}
	if err := enableAttachedWAL(ctx, conn, name); err != nil {
		return err
	}
	for _, pragma := range []string{"synchronous=NORMAL", "optimize=0x10002"} {
		if _, err := attachedPragma(ctx, conn, name, pragma); err != nil {
			return err
		}
	}
	return nil
}

func enableAttachedWAL(ctx context.Context, conn sqlite.ExecQuerierContext, name string) error {
	var err error
	for attempt := range walSwitchAttempts {
		var mode string
		if mode, err = attachedPragma(ctx, conn, name, "journal_mode"); err == nil && strings.EqualFold(mode, "wal") {
			return nil
		}
		if _, err = attachedPragma(ctx, conn, name, "journal_mode=WAL"); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	return fmt.Errorf("enable WAL: %w", err)
}

func attachedPragma(ctx context.Context, conn sqlite.ExecQuerierContext, name, pragma string) (string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA "+name+"."+pragma, nil)
	if err != nil {
		return "", fmt.Errorf("pragma %s: %w", pragma, err)
	}
	value := ""
	if dest := make([]driver.Value, len(rows.Columns())); len(dest) > 0 && rows.Next(dest) == nil {
		value = fmt.Sprint(dest[0])
	}
	if err := rows.Close(); err != nil {
		return "", fmt.Errorf("pragma %s: %w", pragma, err)
	}
	return value, nil
}
