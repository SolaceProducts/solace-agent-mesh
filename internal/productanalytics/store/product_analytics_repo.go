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

const (
	consentStateUnset   = "unset"
	consentStateGranted = "granted"
	consentStateDenied  = "denied"
)

type ProductAnalyticsRepo struct {
	db *DB
}

func NewProductAnalyticsRepo(db *DB) *ProductAnalyticsRepo {
	return &ProductAnalyticsRepo{db: db}
}

func (r *ProductAnalyticsRepo) Get(ctx context.Context) (*ProductAnalyticsState, error) {
	return dbutil.QueryRow[ProductAnalyticsState](ctx, r.db,
		`SELECT id, install_id, binding_secret, consent_state, created_time, updated_time
		 FROM product_analytics_state WHERE id = ?`,
		ProductAnalyticsStateSingletonID,
	)
}

func (r *ProductAnalyticsRepo) EnsureIdentity(ctx context.Context) (string, string, error) {
	row, err := r.Get(ctx)
	if err != nil {
		return "", "", err
	}
	switch {
	case row == nil:
		return r.insertIfAbsent(ctx)
	case !isValidInstallID(row.InstallID):
		return r.replaceIdentity(ctx, row.InstallID)
	default:
		return row.InstallID, row.BindingSecret, nil
	}
}

func (r *ProductAnalyticsRepo) insertIfAbsent(ctx context.Context) (string, string, error) {
	now := time.Now().UnixMilli()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO product_analytics_state (id, install_id, binding_secret, consent_state, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		ProductAnalyticsStateSingletonID, idgen.New(), "", consentStateUnset, now, now,
	); err != nil {
		return "", "", fmt.Errorf("insert product_analytics_state: %w", err)
	}
	row, err := r.Get(ctx)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", fmt.Errorf("product_analytics_state missing after insert")
	}
	return row.InstallID, row.BindingSecret, nil
}

func (r *ProductAnalyticsRepo) replaceIdentity(ctx context.Context, observed string) (string, string, error) {
	installID := idgen.New()
	res, err := r.db.ExecContext(ctx,
		`UPDATE product_analytics_state SET install_id = ?, binding_secret = '', updated_time = ?
		 WHERE id = ? AND install_id = ? AND consent_state IN (?, ?, ?)`,
		installID, time.Now().UnixMilli(), ProductAnalyticsStateSingletonID, observed, consentStateUnset, consentStateGranted, "",
	)
	if err != nil {
		return "", "", fmt.Errorf("replace product_analytics install_id: %w", err)
	}
	if affected, affErr := res.RowsAffected(); affErr == nil && affected == 0 {
		return r.persistedIdentity(ctx)
	}
	return installID, "", nil
}

func (r *ProductAnalyticsRepo) ReplaceBindingSecret(ctx context.Context, observed, replacement string) (string, error) {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE product_analytics_state SET binding_secret = ?, updated_time = ?
		 WHERE id = ? AND binding_secret = ? AND consent_state IN (?, ?, ?)`,
		replacement, time.Now().UnixMilli(), ProductAnalyticsStateSingletonID, observed,
		consentStateUnset, consentStateGranted, "",
	); err != nil {
		return "", fmt.Errorf("replace product_analytics binding_secret: %w", err)
	}
	row, err := r.Get(ctx)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	return row.BindingSecret, nil
}

func (r *ProductAnalyticsRepo) persistedIdentity(ctx context.Context) (string, string, error) {
	row, err := r.Get(ctx)
	if err != nil {
		return "", "", err
	}
	if row == nil || !isValidInstallID(row.InstallID) {
		return "", "", nil
	}
	return row.InstallID, row.BindingSecret, nil
}

func (r *ProductAnalyticsRepo) ConsentState(ctx context.Context) (string, error) {
	row, err := r.Get(ctx)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	return row.ConsentState, nil
}

func (r *ProductAnalyticsRepo) SetConsent(ctx context.Context, consentState string) error {
	installIDIfCreating := idgen.New()
	updateCols := []string{"consent_state", "updated_time"}
	if consentState == consentStateDenied {
		installIDIfCreating = ""
		updateCols = append(updateCols, "install_id", "binding_secret")
	}

	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO product_analytics_state (id, install_id, binding_secret, consent_state, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "id", updateCols)
	if _, err := r.db.ExecContext(ctx, upsertSQL,
		ProductAnalyticsStateSingletonID, installIDIfCreating, "", consentState, now, now,
	); err != nil {
		return fmt.Errorf("set product_analytics consent: %w", err)
	}
	return nil
}

func isValidInstallID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (r *ProductAnalyticsRepo) ClaimInventoryDay(ctx context.Context, installID, day string) (bool, error) {
	return dbutil.WithTxVal(ctx, r.db.DB, func(tx *dbutil.Tx) (bool, error) {
		now := time.Now().UnixMilli()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO product_analytics_inventory_claim (install_id, claimed_day, updated_time)
			 VALUES (?, ?, ?) ON CONFLICT (install_id) DO NOTHING`,
			installID, day, now,
		)
		if err != nil {
			return false, fmt.Errorf("claim product analytics inventory day: %w", err)
		}
		if affected, affErr := res.RowsAffected(); affErr == nil && affected > 0 {
			if _, pruneErr := tx.ExecContext(ctx,
				`DELETE FROM product_analytics_inventory_claim WHERE install_id != ?`, installID); pruneErr != nil {
				return false, fmt.Errorf("prune product analytics inventory claims: %w", pruneErr)
			}
			return true, nil
		}

		res, err = tx.ExecContext(ctx,
			`UPDATE product_analytics_inventory_claim SET claimed_day = ?, updated_time = ?
			 WHERE install_id = ? AND claimed_day < ?`,
			day, now, installID, day,
		)
		if err != nil {
			return false, fmt.Errorf("advance product analytics inventory day: %w", err)
		}
		affected, affErr := res.RowsAffected()
		if affErr != nil {
			return false, fmt.Errorf("advance product analytics inventory day: %w", affErr)
		}
		return affected > 0, nil
	})
}
