// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type GatewayRepo struct {
	db *DB
}

func NewGatewayRepo(db *DB) *GatewayRepo {
	return &GatewayRepo{db: db}
}

func (r *GatewayRepo) DB() *DB { return r.db }

func (r *GatewayRepo) Create(ctx context.Context, gw *Gateway) error {
	if gw.ID == "" {
		gw.ID = idgen.New()
	}
	if gw.DNSID == nil {
		id := generateDNSID()
		gw.DNSID = &id
	}
	now := time.Now().UnixMilli()
	if gw.CreatedTime == 0 {
		gw.CreatedTime = now
	}
	if gw.UpdatedTime == 0 {
		gw.UpdatedTime = now
	}
	gw.Description = defaultStrPtr(gw.Description, defaultDescriptionFallback)
	gw.Values = defaultStrPtr(gw.Values, "{}")

	var slugArg any
	if gw.Slug != "" {
		slugArg = gw.Slug
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO gateways (id, slug, dns_id, name, description, type, "values", public_url, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		gw.ID, slugArg, gw.DNSID, gw.Name, gw.Description, gw.Type, gw.Values, gw.PublicURL,
		gw.CreatedBy, gw.UpdatedBy, gw.CreatedTime, gw.UpdatedTime,
	)
	if err != nil {
		if isSlugUniqueViolation(err) {
			return &GatewaySlugConflictError{Slug: gw.Slug}
		}
		return fmt.Errorf("GatewayRepo.Create(%s): %w", gw.ID, err)
	}
	return nil
}

func generateDNSID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	const size = 8
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[b[i]%byte(len(alphabet))]
	}
	return string(b)
}

func (r *GatewayRepo) GetByID(ctx context.Context, id string) (*Gateway, error) {
	return dbutil.QueryRow[Gateway](ctx, r.db,
		`SELECT id, COALESCE(slug, '') AS slug, dns_id, name, description, type, "values", public_url,
			created_by, updated_by, created_time, updated_time
		 FROM gateways WHERE id = ?`, id,
	)
}

func (r *GatewayRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int, typeFilter string) ([]*Gateway, int, error) {
	where := ""
	var args []any
	if typeFilter != "" {
		where = " WHERE type = ?"
		args = append(args, typeFilter)
	}

	countQuery := `SELECT COUNT(*) FROM gateways` + where
	total, err := dbutil.QueryVal[int](ctx, r.db, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("GatewayRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	query := `SELECT id, COALESCE(slug, '') AS slug, dns_id, name, description, type, "values", public_url,
			created_by, updated_by, created_time, updated_time
		 FROM gateways` + where + ` ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)

	gateways, err := dbutil.QueryRowPtrs[Gateway](ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("GatewayRepo.ListPaginated query: %w", err)
	}

	return gateways, total, nil
}

func (r *GatewayRepo) Update(ctx context.Context, gw *Gateway) error {
	gw.UpdatedTime = time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE gateways SET name = ?, description = ?, "values" = ?,
			public_url = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		gw.Name, gw.Description, gw.Values, gw.PublicURL,
		gw.UpdatedBy, gw.UpdatedTime, gw.ID,
	)
	if err != nil {
		return fmt.Errorf("update gateway: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("gateway not found")
	}
	return nil
}

func (r *GatewayRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM gateways WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("GatewayRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("gateway not found")
	}
	return nil
}
