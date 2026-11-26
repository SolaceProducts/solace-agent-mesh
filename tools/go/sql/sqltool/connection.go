// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
	DialectSQLite   Dialect = "sqlite"
	DialectMSSQL    Dialect = "mssql"
	DialectOracle   Dialect = "oracle"
)

const jdbcWrapperScheme = "jdbc"

type connInfo struct {
	dialect    Dialect
	driverName string
	dsn        string
}

func parseConnectionString(connStr string) (connInfo, error) {
	normalized := connStr

	if schemeEnd := strings.Index(normalized, "://"); schemeEnd != -1 {
		scheme := normalized[:schemeEnd]
		if plusIdx := strings.Index(scheme, "+"); plusIdx != -1 {
			normalized = scheme[:plusIdx] + normalized[schemeEnd:]
		}
	}

	switch {
	case strings.HasPrefix(normalized, "postgres://") || strings.HasPrefix(normalized, "postgresql://"):
		return connInfo{
			dialect:    DialectPostgres,
			driverName: "pgx",
			dsn:        normalized,
		}, nil

	case strings.HasPrefix(normalized, "mysql://"):
		dsn, err := mysqlURLToDSN(normalized)
		if err != nil {
			return connInfo{}, fmt.Errorf("parse mysql URL: %w", err)
		}
		return connInfo{
			dialect:    DialectMySQL,
			driverName: "mysql",
			dsn:        dsn,
		}, nil

	case strings.HasPrefix(normalized, "sqlite:///"):
		path := strings.TrimPrefix(normalized, "sqlite:///")
		if path == "" {
			return connInfo{}, errors.New("sqlite URL has no path")
		}
		if path == ":memory:" {
			return connInfo{
				dialect:    DialectSQLite,
				driverName: "sqlite",
				dsn:        "file::memory:?cache=shared",
			}, nil
		}
		return connInfo{
			dialect:    DialectSQLite,
			driverName: "sqlite",
			dsn:        "file:" + path,
		}, nil

	case strings.HasPrefix(normalized, "sqlserver://") || strings.HasPrefix(normalized, "mssql://"):
		dsn := normalized
		if strings.HasPrefix(dsn, "mssql://") {
			dsn = "sqlserver://" + strings.TrimPrefix(dsn, "mssql://")
		}
		return connInfo{
			dialect:    DialectMSSQL,
			driverName: "sqlserver",
			dsn:        dsn,
		}, nil

	case strings.HasPrefix(normalized, "oracle://"):
		dsn, err := normalizeOracleURL(normalized)
		if err != nil {
			return connInfo{}, fmt.Errorf("parse oracle URL: %w", err)
		}
		return connInfo{
			dialect:    DialectOracle,
			driverName: "oracle",
			dsn:        dsn,
		}, nil

	default:
		return connInfo{}, fmt.Errorf("unsupported database URL scheme %q", urlScheme(connStr))
	}
}

func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

const connectionStringOmitted = "<connection string omitted>"

func stripConnectionString(err error, dsns ...string) error {
	err = unwrapURLError(err)
	msg := err.Error()
	stripped := msg
	for _, dsn := range dsns {
		if dsn != "" {
			stripped = strings.ReplaceAll(stripped, dsn, connectionStringOmitted)
		}
	}
	stripped = urlShapedToken.ReplaceAllString(stripped, connectionStringOmitted)
	if stripped == msg {
		return err
	}
	return errors.New(stripped)
}

var urlShapedToken = regexp.MustCompile("[a-zA-Z][a-zA-Z0-9+.-]*://[^\\s`\"']+")

func urlScheme(connStr string) string {
	i := strings.Index(connStr, "://")
	if i <= 0 {
		return ""
	}
	scheme := connStr[:i]
	parts := strings.Split(scheme, ":")
	if len(parts) > 2 || (len(parts) == 2 && !strings.EqualFold(parts[0], jdbcWrapperScheme)) {
		return ""
	}
	for _, part := range parts {
		if !isSchemeToken(part) {
			return ""
		}
	}
	return scheme
}

func isSchemeToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func mysqlURLToDSN(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", unwrapURLError(err)
	}

	var userInfo string
	if u.User != nil {
		userInfo = u.User.Username()
		if p, ok := u.User.Password(); ok {
			userInfo += ":" + p
		}
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "3306"
	}

	dbName := strings.TrimPrefix(u.Path, "/")

	dsn := fmt.Sprintf("%s@tcp(%s:%s)/%s", userInfo, host, port, dbName)

	params := u.Query()
	if _, ok := params["parseTime"]; !ok {
		params.Set("parseTime", "true")
	}
	if encoded := params.Encode(); encoded != "" {
		dsn += "?" + encoded
	}

	return dsn, nil
}

func normalizeOracleURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", unwrapURLError(err)
	}

	if serviceName := u.Query().Get("service_name"); serviceName != "" && (u.Path == "" || u.Path == "/") {
		u.Path = "/" + serviceName
		q := u.Query()
		q.Del("service_name")
		u.RawQuery = q.Encode()
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "1521"
		u.Host = host + ":" + port
	}

	return u.String(), nil
}

func openDB(connStr string) (*sql.DB, Dialect, error) {
	info, err := parseConnectionString(connStr)
	if err != nil {
		return nil, "", err
	}

	db, err := sql.Open(info.driverName, info.dsn)
	if err != nil {
		return nil, "", fmt.Errorf("open database: %w", stripConnectionString(err, connStr, info.dsn))
	}

	switch info.dialect {
	case DialectSQLite:
		db.SetMaxOpenConns(1)
		for _, pragma := range []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA foreign_keys=ON",
			"PRAGMA busy_timeout=5000",
		} {
			if _, err := db.Exec(pragma); err != nil {
				_ = db.Close()
				return nil, "", fmt.Errorf("sqlite pragma %q: %w", pragma, err)
			}
		}
	case DialectPostgres:
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(30 * time.Minute)
	case DialectMySQL, DialectMSSQL, DialectOracle:
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(30 * time.Minute)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("ping database: %w", stripConnectionString(err, connStr, info.dsn))
	}

	return db, info.dialect, nil
}
