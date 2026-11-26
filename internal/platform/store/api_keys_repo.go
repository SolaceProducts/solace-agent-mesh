// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var ErrAPIKeyNotFound = errors.New("api key: not found")

var ErrAPIKeyCapReached = errors.New("api key: owner already holds the maximum number of active keys")

const (
	APIKeyStatusActive  = "active"
	APIKeyStatusExpired = "expired"
)

const neverExpiresSortSentinel = int64(1) << 62

const apiKeysSelectColumns = `id, user_id, label, key_hash, prefix, expires_time, ` +
	`last_used_time, created_by, updated_by, created_time, updated_time`

var apiKeysSortExpressions = map[string]string{
	"label":          "LOWER(label)",
	"prefix":         "prefix",
	"created_time":   "created_time",
	"updated_time":   "updated_time",
	"expires_time":   fmt.Sprintf("COALESCE(expires_time, %d)", neverExpiresSortSentinel),
	"last_used_time": "COALESCE(last_used_time, 0)",
}

type APIKeysRepo struct {
	db *DB
}

func NewAPIKeysRepo(db *DB) *APIKeysRepo {
	return &APIKeysRepo{db: db}
}

type APIKeyListOptions struct {
	PageNumber int
	PageSize   int
	SortColumn string
	SortOrder  string
	UserID     string
	Status     string
	Now        int64
}

func (r *APIKeysRepo) Create(ctx context.Context, key APIKey, scopes []string, maxActive int, now int64) (*APIKey, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("APIKeysRepo.Create: scopes must not be empty")
	}
	if key.UserID == "" || key.KeyHash == "" {
		return nil, fmt.Errorf("APIKeysRepo.Create: user_id and key_hash are required")
	}
	if maxActive <= 0 {
		return nil, fmt.Errorf("APIKeysRepo.Create: maxActive must be positive, got %d", maxActive)
	}
	key.ID = idgen.New()
	key.CreatedTime = now
	key.UpdatedTime = now

	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := lockOwnerTx(ctx, tx, key.UserID); err != nil {
			return err
		}
		active, err := dbutil.QueryVal[int](ctx, tx,
			`SELECT COUNT(*) FROM api_keys
			 WHERE user_id = ? AND (expires_time IS NULL OR expires_time > ?)`,
			key.UserID, now)
		if err != nil {
			return fmt.Errorf("count active keys: %w", err)
		}
		if active >= maxActive {
			return ErrAPIKeyCapReached
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO api_keys (
				id, user_id, label, key_hash, prefix, expires_time,
				last_used_time, created_by, updated_by, created_time, updated_time
			 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			key.ID, key.UserID, key.Label, key.KeyHash, key.Prefix, key.ExpiresTime,
			key.LastUsedTime, key.CreatedBy, key.UpdatedBy, key.CreatedTime, key.UpdatedTime,
		); err != nil {
			return fmt.Errorf("insert api key: %w", err)
		}
		for _, scope := range scopes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO api_key_scopes (api_key_id, scope) VALUES (?, ?)`,
				key.ID, scope); err != nil {
				return fmt.Errorf("insert api key scope: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func lockOwnerTx(ctx context.Context, tx *dbutil.Tx, userID string) error {
	if tx.Dialect() != "postgres" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))`,
		userID, "api_keys"); err != nil {
		return fmt.Errorf("lock api-key owner: %w", err)
	}
	return nil
}

func (r *APIKeysRepo) GetByID(ctx context.Context, id string) (*APIKey, error) {
	key, err := dbutil.QueryRow[APIKey](ctx, r.db,
		`SELECT `+apiKeysSelectColumns+` FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("APIKeysRepo.GetByID: %w", err)
	}
	if key == nil {
		return nil, ErrAPIKeyNotFound
	}
	return key, nil
}

func (r *APIKeysRepo) GetByKeyHash(ctx context.Context, keyHash string) (*APIKey, error) {
	if keyHash == "" {
		return nil, ErrAPIKeyNotFound
	}
	key, err := dbutil.QueryRow[APIKey](ctx, r.db,
		`SELECT `+apiKeysSelectColumns+` FROM api_keys WHERE key_hash = ?`, keyHash)
	if err != nil {
		return nil, fmt.Errorf("APIKeysRepo.GetByKeyHash: %w", err)
	}
	if key == nil {
		return nil, ErrAPIKeyNotFound
	}
	return key, nil
}

func (r *APIKeysRepo) ListPaginated(ctx context.Context, opts APIKeyListOptions) ([]*APIKey, int, error) {
	var clauses []string
	var args []any
	if opts.UserID != "" {
		clauses = append(clauses, "user_id = ?")
		args = append(args, opts.UserID)
	}
	if opts.Status != "" && opts.Now == 0 {
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated: status %q needs Now; the zero instant reports every dated key as expired", opts.Status)
	}
	switch opts.Status {
	case "":
	case APIKeyStatusActive:
		clauses = append(clauses, "(expires_time IS NULL OR expires_time > ?)")
		args = append(args, opts.Now)
	case APIKeyStatusExpired:
		clauses = append(clauses, "(expires_time IS NOT NULL AND expires_time <= ?)")
		args = append(args, opts.Now)
	default:
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated: unsupported status %q", opts.Status)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM api_keys`+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated count: %w", err)
	}

	sortColumn := opts.SortColumn
	if sortColumn == "" {
		sortColumn = "created_time"
	}
	sortExpr, ok := apiKeysSortExpressions[sortColumn]
	if !ok {
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated: unsupported sort column %q", opts.SortColumn)
	}
	order := "DESC"
	switch strings.ToUpper(opts.SortOrder) {
	case "", "DESC":
	case "ASC":
		order = "ASC"
	default:
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated: unsupported sort order %q", opts.SortOrder)
	}

	if opts.PageSize > 0 {
		if lastPage := (total + opts.PageSize - 1) / opts.PageSize; opts.PageNumber > lastPage {
			return []*APIKey{}, total, nil
		}
	}
	offset := (opts.PageNumber - 1) * opts.PageSize
	pageArgs := append(append([]any{}, args...), opts.PageSize, offset)
	keys, err := dbutil.QueryRowPtrs[APIKey](ctx, r.db,
		`SELECT `+apiKeysSelectColumns+` FROM api_keys`+where+
			` ORDER BY `+sortExpr+` `+order+`, id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("APIKeysRepo.ListPaginated query: %w", err)
	}
	return keys, total, nil
}

type apiKeyScopeRow struct {
	APIKeyID string `db:"api_key_id"`
	Scope    string `db:"scope"`
}

func (r *APIKeysRepo) ScopesForKeys(ctx context.Context, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}
	placeholders, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("APIKeysRepo.ScopesForKeys: id filter: %w", err)
	}
	rows, err := dbutil.QueryRows[apiKeyScopeRow](ctx, r.db,
		`SELECT api_key_id, scope FROM api_key_scopes
		 WHERE api_key_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("APIKeysRepo.ScopesForKeys: %w", err)
	}
	out := make(map[string][]string, len(ids))
	for _, row := range rows {
		out[row.APIKeyID] = append(out[row.APIKeyID], row.Scope)
	}
	for _, scopes := range out {
		slices.Sort(scopes)
	}
	return out, nil
}

func (r *APIKeysRepo) UpdateLabel(ctx context.Context, id, label, updatedBy string) (*APIKey, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET label = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
		label, updatedBy, time.Now().UnixMilli(), id)
	if err != nil {
		return nil, fmt.Errorf("APIKeysRepo.UpdateLabel(%s): %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, ErrAPIKeyNotFound
	}
	return r.GetByID(ctx, id)
}

func (r *APIKeysRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("APIKeysRepo.Delete(%s): %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

func (r *APIKeysRepo) TouchLastUsed(ctx context.Context, id string, at int64) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_time = ?
		 WHERE id = ? AND (last_used_time IS NULL OR last_used_time < ?)`, at, id, at); err != nil {
		return fmt.Errorf("APIKeysRepo.TouchLastUsed(%s): %w", id, err)
	}
	return nil
}
