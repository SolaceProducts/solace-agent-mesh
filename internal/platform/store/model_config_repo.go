// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

type ModelConfigRepo struct {
	db *DB
}

func NewModelConfigRepo(db *DB) *ModelConfigRepo {
	return &ModelConfigRepo{db: db}
}

func (r *ModelConfigRepo) WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

const modelConfigSelectColumns = `id, alias, provider, model_name, api_base, model_auth_type,
	model_auth_config, model_params, description, max_input_tokens, model_type,
	price_input, price_output, price_cache_read, price_cache_write,
	created_by, updated_by, created_time, updated_time`

func (r *ModelConfigRepo) GetAll(ctx context.Context) ([]*ModelConfiguration, error) {
	return dbutil.QueryRowPtrs[ModelConfiguration](ctx, r.db,
		`SELECT `+modelConfigSelectColumns+` FROM model_configurations ORDER BY created_time DESC`,
	)
}

var modelConfigSortColumns = map[string]string{
	"alias":       "alias",
	"createdTime": "created_time",
}

func (r *ModelConfigRepo) GetPage(ctx context.Context, offset, limit int, sortField string, desc bool) ([]*ModelConfiguration, error) {
	column, ok := modelConfigSortColumns[sortField]
	if !ok {
		column, desc = "created_time", true
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	return dbutil.QueryRowPtrs[ModelConfiguration](ctx, r.db,
		`SELECT `+modelConfigSelectColumns+` FROM model_configurations ORDER BY `+column+` `+direction+`, id LIMIT ? OFFSET ?`,
		limit, offset,
	)
}

func (r *ModelConfigRepo) CountAll(ctx context.Context) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM model_configurations`)
}

func (r *ModelConfigRepo) GetByAlias(ctx context.Context, alias string) (*ModelConfiguration, error) {
	return dbutil.QueryRow[ModelConfiguration](ctx, r.db,
		`SELECT `+modelConfigSelectColumns+` FROM model_configurations WHERE alias = ?`, alias,
	)
}

func (r *ModelConfigRepo) GetByAliasOrID(ctx context.Context, aliasOrID string) (*ModelConfiguration, error) {
	mc, err := r.GetByAlias(ctx, aliasOrID)
	if err != nil {
		return nil, fmt.Errorf("ModelConfigRepo.GetByAliasOrID(%s): %w", aliasOrID, err)
	}
	if mc != nil {
		return mc, nil
	}
	id, parseErr := uuid.Parse(aliasOrID)
	if parseErr != nil {
		return nil, nil
	}
	return dbutil.QueryRow[ModelConfiguration](ctx, r.db,
		`SELECT `+modelConfigSelectColumns+` FROM model_configurations WHERE id = ?`, id.String(),
	)
}

func (r *ModelConfigRepo) ExistsByAlias(ctx context.Context, alias string) (bool, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM model_configurations WHERE alias = ?`, alias,
	)
	if err != nil {
		return false, fmt.Errorf("ModelConfigRepo.ExistsByAlias(%s): %w", alias, err)
	}
	return count > 0, nil
}

func (r *ModelConfigRepo) Create(ctx context.Context, mc *ModelConfiguration) error {
	return r.createOn(ctx, r.db, mc)
}

func (r *ModelConfigRepo) CreateInTx(ctx context.Context, tx *dbutil.Tx, mc *ModelConfiguration) error {
	return r.createOn(ctx, tx, mc)
}

func (r *ModelConfigRepo) createOn(ctx context.Context, q dbutil.Querier, mc *ModelConfiguration) error {
	if mc.ID == "" {
		mc.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if mc.CreatedTime == 0 {
		mc.CreatedTime = now
	}
	if mc.UpdatedTime == 0 {
		mc.UpdatedTime = now
	}
	if mc.ModelType == "" {
		mc.ModelType = ModelTypeChat
	}

	_, err := q.ExecContext(ctx,
		`INSERT INTO model_configurations
		 (id, alias, provider, model_name, api_base, model_auth_type,
		  model_auth_config, model_params, description, max_input_tokens, model_type,
		  price_input, price_output, price_cache_read, price_cache_write,
		  created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mc.ID, mc.Alias, mc.Provider, mc.ModelName, mc.APIBase,
		mc.ModelAuthType, mc.ModelAuthConfig, mc.ModelParams, mc.Description, mc.MaxInputTokens, mc.ModelType,
		mc.PriceInput, mc.PriceOutput, mc.PriceCacheRead, mc.PriceCacheWrite,
		mc.CreatedBy, mc.UpdatedBy, mc.CreatedTime, mc.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("ModelConfigRepo.Create(%s): %w", mc.ID, err)
	}
	return nil
}

func (r *ModelConfigRepo) Update(ctx context.Context, mc *ModelConfiguration) error {
	return r.updateOn(ctx, r.db, mc)
}

func (r *ModelConfigRepo) UpdateInTx(ctx context.Context, tx *dbutil.Tx, mc *ModelConfiguration) error {
	return r.updateOn(ctx, tx, mc)
}

func (r *ModelConfigRepo) updateOn(ctx context.Context, q dbutil.Querier, mc *ModelConfiguration) error {
	mc.UpdatedTime = time.Now().UnixMilli()
	result, err := q.ExecContext(ctx,
		`UPDATE model_configurations SET
			alias = ?, provider = ?, model_name = ?, api_base = ?,
			model_auth_type = ?, model_auth_config = ?, model_params = ?,
			description = ?, max_input_tokens = ?,
			price_input = ?, price_output = ?,
			price_cache_read = ?, price_cache_write = ?,
			updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		mc.Alias, mc.Provider, mc.ModelName, mc.APIBase,
		mc.ModelAuthType, mc.ModelAuthConfig, mc.ModelParams,
		mc.Description, mc.MaxInputTokens,
		mc.PriceInput, mc.PriceOutput, mc.PriceCacheRead, mc.PriceCacheWrite,
		mc.UpdatedBy, mc.UpdatedTime, mc.ID,
	)
	if err != nil {
		return fmt.Errorf("update model configuration: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model configuration not found")
	}
	return nil
}

func (r *ModelConfigRepo) GetByID(ctx context.Context, id string) (*ModelConfiguration, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	return dbutil.QueryRow[ModelConfiguration](ctx, r.db,
		`SELECT `+modelConfigSelectColumns+` FROM model_configurations WHERE id = ?`, id,
	)
}

func (r *ModelConfigRepo) Delete(ctx context.Context, id string) error {
	return r.deleteOn(ctx, r.db, id)
}

func (r *ModelConfigRepo) DeleteInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteOn(ctx, tx, id)
}

func (r *ModelConfigRepo) deleteOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx,
		`DELETE FROM model_configurations WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("ModelConfigRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("model configuration not found")
	}
	return nil
}

func (r *ModelConfigRepo) Count(ctx context.Context) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM model_configurations`,
	)
}
