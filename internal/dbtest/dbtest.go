// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbtest

import (
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Template struct {
	dialect string
	counter atomic.Int64

	baseURL      string
	templateName string
	adminDB      *sql.DB

	sqliteDir      string
	sqliteTemplate string
	sqliteAttached []string
}

func NewTemplateFromURL(dbURL, name string, migrateFn func(dbURL string) error) (*Template, error) {
	if dbURL == "" || strings.HasPrefix(dbURL, "sqlite:") {
		return newSQLiteTemplate(name, migrateFn)
	}
	if strings.HasPrefix(dbURL, "postgres://") || strings.HasPrefix(dbURL, "postgresql://") {
		base, err := BaseURL(dbURL)
		if err != nil {
			return nil, err
		}
		return NewTemplate(base, name, migrateFn)
	}
	return nil, fmt.Errorf("dbtest: unsupported database URL scheme: %q", dbURL)
}

func NewTemplate(baseURL string, name string, migrateFn func(dbURL string) error) (*Template, error) {
	adminDB, err := sql.Open("pgx", baseURL+"/postgres?sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("connect to admin db: %w", err)
	}

	tmplName := "tmpl_" + name

	adminDB.Exec("DROP DATABASE IF EXISTS " + tmplName + " WITH (FORCE)") //nolint:errcheck

	if _, err := adminDB.Exec("CREATE DATABASE " + tmplName); err != nil {
		_ = adminDB.Close()
		return nil, fmt.Errorf("create template db %s: %w", tmplName, err)
	}

	if err := migrateFn(baseURL + "/" + tmplName + "?sslmode=disable"); err != nil {
		adminDB.Exec("DROP DATABASE " + tmplName + " WITH (FORCE)") //nolint:errcheck
		_ = adminDB.Close()
		return nil, fmt.Errorf("migrate template %s: %w", tmplName, err)
	}

	return &Template{
		dialect:      "postgres",
		baseURL:      baseURL,
		templateName: tmplName,
		adminDB:      adminDB,
	}, nil
}

func newSQLiteTemplate(name string, migrateFn func(dbURL string) error) (*Template, error) {
	dir, err := os.MkdirTemp("", "dbtest-"+name+"-")
	if err != nil {
		return nil, fmt.Errorf("create sqlite template tempdir: %w", err)
	}

	templatePath := filepath.Join(dir, "template.db")
	if err := migrateFn(sqliteURL(templatePath)); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("migrate sqlite template: %w", err)
	}

	attached, companionsErr := sqliteCompanions(templatePath)
	if companionsErr != nil {
		_ = os.RemoveAll(dir)
		return nil, companionsErr
	}
	for _, path := range append([]string{templatePath}, companionPaths(templatePath, attached)...) {
		if err := consolidateSQLite(path); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
	}

	return &Template{
		dialect:        "sqlite",
		sqliteDir:      dir,
		sqliteTemplate: templatePath,
		sqliteAttached: attached,
	}, nil
}

func sqliteCompanions(templatePath string) ([]string, error) {
	ext := filepath.Ext(templatePath)
	stem := strings.TrimSuffix(templatePath, ext)
	matches, err := filepath.Glob(stem + ".*" + ext)
	if err != nil {
		return nil, fmt.Errorf("find sqlite template companions: %w", err)
	}
	var names []string
	for _, m := range matches {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(m, stem+"."), ext))
	}
	return names, nil
}

func companionPaths(mainPath string, names []string) []string {
	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, dbutil.SQLiteAttachmentPath(mainPath, n))
	}
	return paths
}

func consolidateSQLite(path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return fmt.Errorf("open sqlite template: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint sqlite template: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = DELETE"); err != nil {
		return fmt.Errorf("switch sqlite template to delete journal: %w", err)
	}
	return nil
}

func (tmpl *Template) Clone(t *testing.T) string {
	t.Helper()
	cloneURL, drop, err := tmpl.CloneDB()
	if err != nil {
		t.Fatalf("clone template db: %v", err)
	}
	t.Cleanup(drop)
	return cloneURL
}

func (tmpl *Template) CloneDB() (cloneURL string, drop func(), err error) {
	n := tmpl.counter.Add(1)
	switch tmpl.dialect {
	case "postgres":
		return tmpl.clonePostgres(n)
	case "sqlite":
		return tmpl.cloneSQLite(n)
	default:
		return "", nil, fmt.Errorf("dbtest: unknown template dialect %q", tmpl.dialect)
	}
}

func (tmpl *Template) clonePostgres(n int64) (string, func(), error) {
	cloneName := fmt.Sprintf("%s_%d", tmpl.templateName, n)

	if _, err := tmpl.adminDB.Exec(
		fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", cloneName, tmpl.templateName),
	); err != nil {
		return "", nil, fmt.Errorf("create clone %s: %w", cloneName, err)
	}

	cloneURL := tmpl.baseURL + "/" + cloneName + "?sslmode=disable"
	drop := func() {
		tmpl.adminDB.Exec(fmt.Sprintf( //nolint:errcheck
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s'",
			cloneName,
		))
		tmpl.adminDB.Exec("DROP DATABASE IF EXISTS " + cloneName + " WITH (FORCE)") //nolint:errcheck
	}
	return cloneURL, drop, nil
}

func (tmpl *Template) cloneSQLite(n int64) (string, func(), error) {
	clonePath := filepath.Join(tmpl.sqliteDir, fmt.Sprintf("clone_%d.db", n))
	sources := append([]string{tmpl.sqliteTemplate}, companionPaths(tmpl.sqliteTemplate, tmpl.sqliteAttached)...)
	targets := append([]string{clonePath}, companionPaths(clonePath, tmpl.sqliteAttached)...)
	for i := range sources {
		if err := copyFile(sources[i], targets[i]); err != nil {
			return "", nil, fmt.Errorf("copy sqlite template: %w", err)
		}
	}
	drop := func() {
		for _, path := range targets {
			_ = os.Remove(path)
			_ = os.Remove(path + "-wal")
			_ = os.Remove(path + "-shm")
		}
	}
	return sqliteURL(clonePath), drop, nil
}

func (tmpl *Template) Close() {
	switch tmpl.dialect {
	case "postgres":
		tmpl.adminDB.Exec("DROP DATABASE IF EXISTS " + tmpl.templateName + " WITH (FORCE)") //nolint:errcheck
		_ = tmpl.adminDB.Close()
	case "sqlite":
		_ = os.RemoveAll(tmpl.sqliteDir)
	}
}

func BaseURL(fullURL string) (string, error) {
	u, err := url.Parse(fullURL)
	if err != nil {
		return "", fmt.Errorf("parse database URL: %w", err)
	}
	u.Path = ""
	u.RawQuery = ""
	return u.String(), nil
}

func sqliteURL(absPath string) string {
	return "sqlite:///" + absPath
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
