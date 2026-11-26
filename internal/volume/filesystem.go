// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	singleHostListFile     = "volumes.db"
	removedDirectoryPrefix = ".removed-"
	setAsideDirectoryGrace = time.Hour

	sharedListIdleConns = 2
	sharedListMaxConns  = 10

	whileNoLiveWriteLock = " AND (lock_holder IS NULL OR lock_max_expiry IS NULL OR lock_max_expiry <= ?)"
	whileStillExpired    = " AND expires_at IS NOT NULL AND expires_at <= ?"
	whileNotCreatedBy    = " AND creator_type <> ?"
)

var columnsAbsentFromTheFirstListFiles = []string{"external_path", "external_path_in_container"}

type FilesystemStore struct {
	root              string
	view              RuntimeView
	db                *dbutil.DB
	listLocation      string
	logger            *slog.Logger
	perUserQuotaBytes int64
	userCreateLocks   sync.Map
}

type FilesystemConfig struct {
	Root        string
	DatabaseURL string
	View        RuntimeView
	Logger      *slog.Logger
}

func (s *FilesystemStore) SetPerUserQuotaBytes(limit int64) {
	if limit < 0 {
		limit = 0
	}
	s.perUserQuotaBytes = limit
}

func (s *FilesystemStore) PerUserQuotaBytes() int64 {
	return s.perUserQuotaBytes
}

func NewFilesystemStore(root string, logger *slog.Logger) (*FilesystemStore, error) {
	return NewFilesystemStoreInView(root, ViewHost, logger)
}

func NewFilesystemStoreInView(root string, view RuntimeView, logger *slog.Logger) (*FilesystemStore, error) {
	return OpenFilesystemStore(context.Background(), FilesystemConfig{Root: root, View: view, Logger: logger})
}

func OpenFilesystemStore(ctx context.Context, cfg FilesystemConfig) (*FilesystemStore, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create volume root %s: %w", cfg.Root, err)
	}

	db, location, err := openVolumeList(cfg, logger)
	if err != nil {
		return nil, err
	}
	if err := dbutil.MigrateSet(ctx, db, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate volume list database: %w", err)
	}
	if err := addColumnsAbsentFromTheFirstListFiles(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &FilesystemStore{root: cfg.Root, view: cfg.View, db: db, listLocation: location, logger: logger}, nil
}

func openVolumeList(cfg FilesystemConfig, logger *slog.Logger) (*dbutil.DB, string, error) {
	listFile, err := filepath.Abs(filepath.Join(cfg.Root, singleHostListFile))
	if err != nil {
		return nil, "", fmt.Errorf("resolve volume list file under %s: %w", cfg.Root, err)
	}
	if cfg.DatabaseURL == "" {
		db, openErr := dbutil.OpenSQLiteFile(listFile, logger)
		if openErr != nil {
			return nil, "", fmt.Errorf("open volume list file %s: %w", listFile, openErr)
		}
		return db, listFile, nil
	}
	if _, statErr := os.Stat(listFile); statErr == nil {
		logger.Warn("the volume list now lives in the configured database; the list file under the volume root is no longer read, so volumes recorded only in it are not listed (their directories are untouched)",
			"listFile", listFile)
	}
	db, err := dbutil.Open(cfg.DatabaseURL, logger, dbutil.WithPoolName("volumes"))
	if err != nil {
		return nil, "", fmt.Errorf("open volume list database %s: %w", dbutil.RedactDSN(cfg.DatabaseURL), err)
	}
	if db.Dialect() == "postgres" {
		db.SetMaxIdleConns(sharedListIdleConns)
		db.SetMaxOpenConns(sharedListMaxConns)
	}
	return db, hostAndDatabaseName(cfg.DatabaseURL), nil
}

func hostAndDatabaseName(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil || u.Host == "" {
		return dbutil.RedactDSN(databaseURL)
	}
	return u.Host + u.Path
}

func addColumnsAbsentFromTheFirstListFiles(ctx context.Context, db *dbutil.DB) error {
	if db.Dialect() != "sqlite" {
		return nil
	}
	for _, column := range columnsAbsentFromTheFirstListFiles {
		var present int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('volumes') WHERE name = ?", column,
		).Scan(&present); err != nil {
			return fmt.Errorf("inspect the volume list file for column %s: %w", column, err)
		}
		if present > 0 {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE volumes ADD COLUMN "+column+" TEXT"); err != nil {
			return fmt.Errorf("add column %s to the volume list file: %w", column, err)
		}
	}
	return nil
}

func (s *FilesystemStore) Root() string { return s.root }

func (s *FilesystemStore) ListLocation() string { return s.listLocation }

func (s *FilesystemStore) ListIsSingleHostFile() bool {
	return s.db.Dialect() == "sqlite"
}

func (s *FilesystemStore) Close() error {
	return s.db.Close()
}

func validateCreateOptions(opts CreateOptions) (linked, copying bool, err error) {
	if opts.Name == "" || opts.UserID == "" {
		return false, false, ErrInvalidOptions
	}
	if err := validateOptID(opts.ID); err != nil {
		return false, false, err
	}
	linked = opts.ExternalPath != ""
	copying = opts.CopyFromPath != ""
	if err := validateCallerChosenIDHasPlainStorage(opts); err != nil {
		return false, false, err
	}
	if linked && copying {
		return false, false, fmt.Errorf("%w: external_path and copy_from_path are mutually exclusive", ErrInvalidOptions)
	}
	if linked {
		if err := validateHostDirPath("external_path", opts.ExternalPath); err != nil {
			return false, false, err
		}
	}
	if copying {
		if err := validateHostDirPath("copy_from_path", opts.CopyFromPath); err != nil {
			return false, false, err
		}
	}
	return linked, copying, nil
}

func validateHostDirPath(label, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %s must be absolute, got %q", ErrInvalidOptions, label, path)
	}
	if info, err := os.Lstat(path); err != nil {
		return fmt.Errorf("%w: %s %q: %v", ErrInvalidOptions, label, path, err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s %q is a symlink; resolve it and use the canonical path (a symlinked %s could bind a directory the operator did not intend)", ErrInvalidOptions, label, path, label)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("%w: %s %q: resolve symlinks: %v", ErrInvalidOptions, label, path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return fmt.Errorf("%w: %s %q (canonical %q): %v", ErrInvalidOptions, label, path, canonical, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s %q is not a directory", ErrInvalidOptions, label, path)
	}
	return nil
}

func (s *FilesystemStore) checkQuotaForCreate(ctx context.Context, opts CreateOptions, copying bool) error {
	if !copying || s.perUserQuotaBytes <= 0 {
		return nil
	}
	delta, err := dirSizeBytes(opts.CopyFromPath)
	if err != nil {
		return fmt.Errorf("measure copy source: %w", err)
	}
	if delta == 0 {
		return nil
	}
	used, err := s.userUsageBytes(ctx, opts.UserID)
	if err != nil {
		return fmt.Errorf("compute user usage: %w", err)
	}
	if used+delta > s.perUserQuotaBytes {
		return &QuotaError{
			UserID:     opts.UserID,
			UsedBytes:  used,
			WouldAdd:   delta,
			LimitBytes: s.perUserQuotaBytes,
		}
	}
	return nil
}

func (s *FilesystemStore) setupVolumeStorage(id string, linked, copying bool, opts CreateOptions) error {
	if err := os.MkdirAll(s.volumePath(id), 0o700); err != nil {
		return fmt.Errorf("create volume directory: %w", err)
	}
	if linked {
		if err := writeLinkedSidecar(s.volumePath(id), id, opts.ExternalPath, opts.ExternalPathContainer); err != nil {
			_ = os.RemoveAll(s.volumePath(id))
			return err
		}
		return nil
	}
	if err := os.MkdirAll(s.dataPath(id), 0o755); err != nil {
		_ = os.RemoveAll(s.volumePath(id))
		return fmt.Errorf("create volume data directory: %w", err)
	}
	if copying {
		if err := copyHostDirInto(opts.CopyFromPath, s.dataPath(id)); err != nil {
			_ = os.RemoveAll(s.volumePath(id))
			return fmt.Errorf("seed volume from %s: %w", opts.CopyFromPath, err)
		}
	}
	return nil
}

func (s *FilesystemStore) lockForUser(userID string) *sync.Mutex {
	if existing, ok := s.userCreateLocks.Load(userID); ok {
		return existing.(*sync.Mutex)
	}
	created := &sync.Mutex{}
	actual, _ := s.userCreateLocks.LoadOrStore(userID, created)
	return actual.(*sync.Mutex)
}

type volumeRow struct {
	id        string
	opts      CreateOptions
	createdAt time.Time
	sizeBytes int64
}

func (s *FilesystemStore) insertVolume(ctx context.Context, db volumeListWriter, row volumeRow) error {
	var ttlSec, expiresAt *int64
	if row.opts.TTL != nil {
		sec := int64(row.opts.TTL.Seconds())
		ttlSec = &sec
		exp := row.createdAt.Add(*row.opts.TTL).Unix()
		expiresAt = &exp
	}
	var extPath, extPathContainer *string
	if row.opts.ExternalPath != "" {
		extPath = &row.opts.ExternalPath
		if row.opts.ExternalPathContainer != "" {
			extPathContainer = &row.opts.ExternalPathContainer
		}
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO volumes (id, name, description, user_id, creator_type, creator_name,
			created_at, last_access, ttl_seconds, expires_at, size_bytes,
			external_path, external_path_in_container)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.id, row.opts.Name, row.opts.Description, row.opts.UserID, string(row.opts.CreatorType),
		row.opts.CreatorName, row.createdAt.Unix(), row.createdAt.Unix(), ttlSec, expiresAt, row.sizeBytes,
		extPath, extPathContainer)
	return err
}

func (s *FilesystemStore) Create(ctx context.Context, opts CreateOptions) (string, error) {
	linked, copying, err := validateCreateOptions(opts)
	if err != nil {
		return "", err
	}
	userLock := s.lockForUser(opts.UserID)
	userLock.Lock()
	defer userLock.Unlock()

	if opts.ID != "" {
		return s.createUnderCallerChosenID(ctx, opts)
	}

	if quotaErr := s.checkQuotaForCreate(ctx, opts, copying); quotaErr != nil {
		return "", quotaErr
	}

	row := volumeRow{id: generateID(), opts: opts, createdAt: time.Now()}
	if setupErr := s.setupVolumeStorage(row.id, linked, copying, opts); setupErr != nil {
		return "", setupErr
	}
	if copying {
		sz, sizeErr := dirSizeBytes(s.dataPath(row.id))
		if sizeErr != nil {
			s.logger.Warn("compute volume size after copy", "id", row.id, "error", sizeErr)
		} else {
			row.sizeBytes = sz
		}
	}

	insert := func(ctx context.Context, db volumeListWriter) error {
		return s.insertVolume(ctx, db, row)
	}
	if s.perUserQuotaBytes > 0 && row.sizeBytes > 0 {
		err = s.insertIfWithinQuota(ctx, opts.UserID, row.sizeBytes, insert)
	} else {
		err = insert(ctx, s.db)
	}
	if err != nil {
		_ = os.RemoveAll(s.volumePath(row.id))
		if errors.Is(err, ErrQuotaExceeded) {
			return "", err
		}
		return "", fmt.Errorf("insert volume metadata: %w", err)
	}

	return row.id, nil
}

func (s *FilesystemStore) createUnderCallerChosenID(ctx context.Context, opts CreateOptions) (string, error) {
	if err := s.ensureStorageOfACallerChosenID(opts.ID); err != nil {
		return "", err
	}
	err := s.insertVolume(ctx, s.db, volumeRow{id: opts.ID, opts: opts, createdAt: time.Now()})
	if dbutil.IsUniqueViolation(err) {
		return "", ErrAlreadyExists
	}
	if err != nil {
		return "", fmt.Errorf("insert volume metadata: %w", err)
	}
	if err := s.ensureStorageOfACallerChosenID(opts.ID); err != nil {
		return "", err
	}
	return opts.ID, nil
}

func (s *FilesystemStore) ensureStorageOfACallerChosenID(id string) error {
	if err := os.MkdirAll(s.volumePath(id), 0o700); err != nil {
		return fmt.Errorf("create volume directory: %w", err)
	}
	if err := os.MkdirAll(s.dataPath(id), 0o755); err != nil {
		return fmt.Errorf("create volume data directory: %w", err)
	}
	return nil
}

type volumeListWriter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *FilesystemStore) insertIfWithinQuota(ctx context.Context, userID string, added int64, insert func(context.Context, volumeListWriter) error) error {
	return s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO volume_quota_guards (user_id, checked_at) VALUES (?, ?)
			ON CONFLICT (user_id) DO UPDATE SET checked_at = excluded.checked_at`,
			userID, time.Now().Unix()); err != nil {
			return fmt.Errorf("serialize quota checks for the user: %w", err)
		}
		var used int64
		if err := tx.QueryRowContext(ctx,
			`SELECT CAST(COALESCE(SUM(size_bytes), 0) AS BIGINT) FROM volumes
			 WHERE user_id = ? AND external_path IS NULL`,
			userID,
		).Scan(&used); err != nil {
			return fmt.Errorf("sum user usage: %w", err)
		}
		if used+added > s.perUserQuotaBytes {
			return &QuotaError{UserID: userID, UsedBytes: used, WouldAdd: added, LimitBytes: s.perUserQuotaBytes}
		}
		return insert(ctx, tx)
	})
}

func (s *FilesystemStore) removeVolumeIf(ctx context.Context, id, condition string, conditionArgs ...any) (bool, error) {
	var setAside string
	removed := false
	err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM volumes WHERE id = ?"+condition, append([]any{id}, conditionArgs...)...)
		if err != nil {
			return fmt.Errorf("delete volume metadata: %w", err)
		}
		matched, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete volume metadata RowsAffected: %w", err)
		}
		if matched == 0 {
			return nil
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM session_volumes WHERE volume_id = ?", id); err != nil {
			return fmt.Errorf("delete session associations: %w", err)
		}
		setAside, err = s.setVolumeDirectoryAside(id)
		if err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err != nil {
		if setAside != "" {
			if restoreErr := os.Rename(setAside, s.volumePath(id)); restoreErr != nil {
				s.logger.Warn("failed to restore the directory of a volume whose removal did not commit", "id", id, "error", restoreErr)
			}
		}
		return false, err
	}
	if setAside != "" {
		if err := os.RemoveAll(setAside); err != nil {
			s.logger.Warn("failed to remove the directory of a removed volume; the expiry sweep retries it", "id", id, "directory", setAside, "error", err)
		}
	}
	return removed, nil
}

func (s *FilesystemStore) setVolumeDirectoryAside(id string) (string, error) {
	aside := filepath.Join(s.root, setAsideDirectoryName(id, time.Now()))
	err := os.Rename(s.volumePath(id), aside)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("set aside the directory of volume %s: %w", id, err)
	}
	return aside, nil
}

func liveWriteLockError(v *Volume) error {
	if v.WriteLock == nil || v.WriteLock.IsExpired() {
		return nil
	}
	return &LockError{VolumeID: v.ID, Holder: v.WriteLock.Holder, Until: v.WriteLock.MaxExpiry}
}

func (s *FilesystemStore) RemoveIfExpired(ctx context.Context, id string) (bool, error) {
	now := time.Now().Unix()
	removed, err := s.removeVolumeIf(ctx, id, whileStillExpired+whileNoLiveWriteLock, now, now)
	if err != nil || removed {
		return removed, err
	}
	v, err := s.getByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if v.IsExpired() {
		return false, liveWriteLockError(v)
	}
	return false, nil
}

func (s *FilesystemStore) RefreshAllSizes(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM volumes WHERE external_path IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("list managed volumes for size refresh: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return 0, fmt.Errorf("scan volume id: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, rowsErr
	}
	refreshed := 0
	for _, id := range ids {
		if _, refErr := s.RefreshSize(ctx, id); refErr != nil {
			s.logger.Warn("refresh volume size", "id", id, "error", refErr)
			continue
		}
		refreshed++
	}
	return refreshed, nil
}

func (s *FilesystemStore) UserUsage(ctx context.Context, userID string) (int64, error) {
	return s.userUsageBytes(ctx, userID)
}

func (s *FilesystemStore) userUsageBytes(ctx context.Context, userID string) (int64, error) {
	var sum int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT CAST(COALESCE(SUM(size_bytes), 0) AS BIGINT) FROM volumes
		 WHERE user_id = ? AND external_path IS NULL`,
		userID,
	).Scan(&sum); err != nil {
		return 0, fmt.Errorf("sum user usage: %w", err)
	}
	return sum, nil
}

func (s *FilesystemStore) RefreshSize(ctx context.Context, id string) (int64, error) {
	v, err := s.getByID(ctx, id)
	if err != nil {
		return 0, err
	}
	if v.Linked {
		return 0, nil
	}
	sz, err := dirSizeBytes(s.dataPath(id))
	if err != nil {
		return 0, fmt.Errorf("measure volume %s: %w", id, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE volumes SET size_bytes = ? WHERE id = ?`, sz, id); err != nil {
		return 0, fmt.Errorf("update size for %s: %w", id, err)
	}
	return sz, nil
}

func (s *FilesystemStore) Get(ctx context.Context, id string) (*Volume, error) {
	return s.getByID(ctx, id)
}

func (s *FilesystemStore) List(ctx context.Context, userID string, filter ListFilter) ([]*Volume, error) {
	query := `SELECT id, name, description, user_id, creator_type, creator_name,
		created_at, last_access, ttl_seconds, expires_at, size_bytes,
		lock_holder, lock_acquired, lock_max_expiry,
		external_path, external_path_in_container
		FROM volumes WHERE user_id = ?`
	args := []any{userID}

	if filter.CreatorType != nil {
		query += " AND creator_type = ?"
		args = append(args, string(*filter.CreatorType))
	}
	if filter.SessionID != nil {
		query += " AND id IN (SELECT volume_id FROM session_volumes WHERE session_id = ?)"
		args = append(args, *filter.SessionID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query volumes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*Volume
	for rows.Next() {
		v, err := scanVolume(rows, s.root)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (s *FilesystemStore) Delete(ctx context.Context, id string, callerType CallerType) error {
	condition, conditionArgs := whileNoLiveWriteLock, []any{time.Now().Unix()}
	if callerType == CallerAgent {
		condition += whileNotCreatedBy
		conditionArgs = append(conditionArgs, string(CallerHuman))
	}
	removed, err := s.removeVolumeIf(ctx, id, condition, conditionArgs...)
	if err != nil || removed {
		return err
	}

	kept, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	if callerType == CallerAgent && kept.CreatorType == CallerHuman {
		return ErrPermission
	}
	if lockErr := liveWriteLockError(kept); lockErr != nil {
		return lockErr
	}
	return &LockError{VolumeID: id, Holder: "<unknown>", Until: time.Now()}
}

func (s *FilesystemStore) AcquireWriteLock(ctx context.Context, id string, holder string, maxDuration time.Duration) error {
	if _, err := s.getByID(ctx, id); err != nil {
		return err
	}

	now := time.Now()
	expiry := now.Add(maxDuration)
	res, err := s.db.ExecContext(ctx, `
		UPDATE volumes
		SET lock_holder = ?, lock_acquired = ?, lock_max_expiry = ?
		WHERE id = ?
		  AND (lock_holder IS NULL OR lock_max_expiry IS NULL OR lock_max_expiry <= ?)`,
		holder, now.Unix(), expiry.Unix(), id, now.Unix())
	if err != nil {
		return fmt.Errorf("acquire write lock: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("acquire write lock RowsAffected: %w", err)
	} else if n == 0 {
		v, getErr := s.getByID(ctx, id)
		if getErr != nil {
			return getErr
		}
		if v.WriteLock != nil && !v.WriteLock.IsExpired() {
			return &LockError{
				VolumeID: id,
				Holder:   v.WriteLock.Holder,
				Until:    v.WriteLock.MaxExpiry,
			}
		}
		return &LockError{VolumeID: id, Holder: "<unknown>", Until: time.Now()}
	}
	return nil
}

func (s *FilesystemStore) ReleaseWriteLock(ctx context.Context, id string, holder string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE volumes SET lock_holder = NULL, lock_acquired = NULL, lock_max_expiry = NULL
		WHERE id = ?
		  AND lock_holder IS NOT NULL
		  AND (lock_holder = ? OR lock_max_expiry <= ?)`,
		id, holder, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("release write lock: %w", err)
	}
	released, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("release write lock RowsAffected: %w", err)
	}
	if released > 0 {
		return nil
	}
	v, err := s.getByID(ctx, id)
	if err != nil {
		return err
	}
	if v.WriteLock == nil || v.WriteLock.Holder == holder || v.WriteLock.IsExpired() {
		return nil
	}
	return ErrNotLockHolder
}

func (s *FilesystemStore) RecordAccess(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE volumes SET last_access = ? WHERE id = ?",
		time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("record access: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *FilesystemStore) Resolve(ctx context.Context, id string) (string, error) {
	v, err := s.getByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			sc, scErr := readSidecar(s.volumePath(id))
			if scErr != nil {
				if s.logger != nil {
					s.logger.Warn("read volume sidecar during resolve fallback",
						"volumeID", id, "error", scErr)
				}
				return "", fmt.Errorf("read volume sidecar for %s: %w", id, scErr)
			}
			if sc != nil && sc.Linked {
				return s.sidecarLinkedPath(sc)
			}
			dp := s.dataPath(id)
			if _, statErr := os.Stat(dp); statErr == nil {
				return dp, nil
			}
		}
		return "", err
	}
	if v.IsExpired() {
		return "", ErrExpired
	}
	if v.Linked {
		return s.linkedPathForVolume(v)
	}
	return s.dataPath(id), nil
}

func (s *FilesystemStore) linkedPathForVolume(v *Volume) (string, error) {
	switch s.view {
	case ViewContainer:
		if v.ExternalPathContainer == "" {
			return "", fmt.Errorf("linked volume %s has no container-view path (container.host_mount_scope may be unset or path is outside the scope)", v.ID)
		}
		return v.ExternalPathContainer, nil
	default:
		return v.ExternalPath, nil
	}
}

func (s *FilesystemStore) sidecarLinkedPath(sc *sidecar) (string, error) {
	if s.view == ViewContainer {
		if sc.ExternalPathContainer == "" {
			return "", fmt.Errorf("linked volume %s has no container-view path (container.host_mount_scope may be unset or path is outside the scope)", sc.ID)
		}
		return sc.ExternalPathContainer, nil
	}
	return sc.ExternalPath, nil
}

func (s *FilesystemStore) CleanupExpired(ctx context.Context) (int, error) {
	s.removeDirectoriesSetAsideBefore(ctx, time.Now().Add(-setAsideDirectoryGrace))
	ids, err := s.expiredVolumeIDs(ctx)
	if err != nil {
		return 0, err
	}
	return s.removeThoseStillExpired(ctx, ids), nil
}

func (s *FilesystemStore) removeDirectoriesSetAsideBefore(ctx context.Context, cutoff time.Time) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		s.logger.Warn("failed to list the volume root for directories of removed volumes", "error", err)
		return
	}
	for _, entry := range entries {
		setAside, ok := parseSetAsideDirectoryName(entry.Name())
		if !ok || !setAside.at.Before(cutoff) {
			continue
		}
		directory := filepath.Join(s.root, entry.Name())
		if s.removalDidNotCommit(ctx, setAside, directory) {
			continue
		}
		if err := os.RemoveAll(directory); err != nil {
			s.logger.Warn("failed to remove the directory of a removed volume; the expiry sweep retries it", "directory", directory, "error", err)
		}
	}
}

func (s *FilesystemStore) removalDidNotCommit(ctx context.Context, setAside setAsideDirectory, directory string) bool {
	listed, err := s.getByID(ctx, setAside.volumeID)
	if errors.Is(err, ErrNotFound) {
		return false
	}
	if err != nil {
		s.logger.Warn("failed to check whether a set-aside directory's volume is still listed; leaving the directory in place", "id", setAside.volumeID, "directory", directory, "error", err)
		return true
	}
	if listed.CreatedAt.After(setAside.at) {
		return false
	}
	s.logger.Warn("a volume is still listed although its directory was set aside for removal; leaving the directory in place for an operator", "id", setAside.volumeID, "directory", directory)
	return true
}

type setAsideDirectory struct {
	volumeID string
	at       time.Time
}

func setAsideDirectoryName(id string, at time.Time) string {
	return removedDirectoryPrefix + id + "-" + strconv.FormatInt(at.UnixNano(), 36)
}

func parseSetAsideDirectoryName(directoryName string) (setAsideDirectory, bool) {
	idAndTime, ok := strings.CutPrefix(directoryName, removedDirectoryPrefix)
	if !ok {
		return setAsideDirectory{}, false
	}
	timeStarts := strings.LastIndexByte(idAndTime, '-')
	if timeStarts < 0 || !validIDPattern.MatchString(idAndTime[:timeStarts]) {
		return setAsideDirectory{}, false
	}
	nanos, err := strconv.ParseInt(idAndTime[timeStarts+1:], 36, 64)
	if err != nil {
		return setAsideDirectory{}, false
	}
	return setAsideDirectory{volumeID: idAndTime[:timeStarts], at: time.Unix(0, nanos)}, true
}

func (s *FilesystemStore) expiredVolumeIDs(ctx context.Context) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, s.db,
		"SELECT id FROM volumes WHERE expires_at IS NOT NULL AND expires_at <= ?", time.Now().Unix())
	if err != nil {
		return nil, fmt.Errorf("query expired volumes: %w", err)
	}
	return ids, nil
}

func (s *FilesystemStore) removeThoseStillExpired(ctx context.Context, ids []string) int {
	removed := 0
	for _, id := range ids {
		stillExpired, err := s.removeVolumeIf(ctx, id, whileStillExpired, time.Now().Unix())
		if err != nil {
			s.logger.Warn("failed to delete expired volume", "id", id, "error", err)
			continue
		}
		if stillExpired {
			removed++
		}
	}
	return removed
}

func (s *FilesystemStore) AssociateSession(ctx context.Context, volumeID string, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO session_volumes (session_id, volume_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING",
		sessionID, volumeID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("associate session: %w", err)
	}
	return nil
}

func (s *FilesystemStore) ListSessionVolumes(ctx context.Context, sessionID string) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, s.db,
		"SELECT volume_id FROM session_volumes WHERE session_id = ? ORDER BY created_at, volume_id", sessionID)
	if err != nil {
		return nil, fmt.Errorf("list session volumes: %w", err)
	}
	return ids, nil
}

func (s *FilesystemStore) volumePath(id string) string {
	return filepath.Join(s.root, id)
}

func (s *FilesystemStore) dataPath(id string) string {
	return filepath.Join(s.root, id, "data")
}

func (s *FilesystemStore) getByID(ctx context.Context, id string) (*Volume, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, user_id, creator_type, creator_name,
			created_at, last_access, ttl_seconds, expires_at, size_bytes,
			lock_holder, lock_acquired, lock_max_expiry,
			external_path, external_path_in_container
		FROM volumes WHERE id = ?`, id)

	v, err := scanVolumeRow(row, s.root)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get volume %s: %w", id, err)
	}
	return v, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanVolumeRow(row *sql.Row, root string) (*Volume, error) {
	return scanVolumeFromScanner(row, root)
}

func scanVolume(rows *sql.Rows, root string) (*Volume, error) {
	return scanVolumeFromScanner(rows, root)
}

func scanVolumeFromScanner(s scanner, root string) (*Volume, error) {
	var (
		v                Volume
		creatorType      string
		createdAt        int64
		lastAccess       int64
		ttlSec           *int64
		expiresAtUnix    *int64
		sizeBytes        int64
		lockHolder       *string
		lockAcquired     *int64
		lockExpiry       *int64
		extPath          *string
		extPathContainer *string
	)

	if err := s.Scan(
		&v.ID, &v.Name, &v.Description, &v.UserID, &creatorType, &v.CreatorName,
		&createdAt, &lastAccess, &ttlSec, &expiresAtUnix, &sizeBytes,
		&lockHolder, &lockAcquired, &lockExpiry,
		&extPath, &extPathContainer,
	); err != nil {
		return nil, err
	}

	v.CreatorType = CallerType(creatorType)
	v.CreatedAt = time.Unix(createdAt, 0)
	v.LastAccess = time.Unix(lastAccess, 0)
	v.SizeBytes = sizeBytes

	if extPath != nil && *extPath != "" {
		v.Linked = true
		v.ExternalPath = *extPath
		if extPathContainer != nil {
			v.ExternalPathContainer = *extPathContainer
		}
		v.Path = *extPath
	} else {
		v.Path = filepath.Join(root, v.ID, "data")
	}

	if ttlSec != nil {
		d := time.Duration(*ttlSec) * time.Second
		v.TTL = &d
	}
	if expiresAtUnix != nil {
		t := time.Unix(*expiresAtUnix, 0)
		v.ExpiresAt = &t
	}
	if lockHolder != nil {
		v.WriteLock = &WriteLock{
			Holder:     *lockHolder,
			AcquiredAt: time.Unix(*lockAcquired, 0),
			MaxExpiry:  time.Unix(*lockExpiry, 0),
		}
	}

	return &v, nil
}
