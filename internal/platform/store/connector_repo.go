// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type ConnectorRepo struct {
	db *DB
}

func NewConnectorRepo(db *DB) *ConnectorRepo {
	return &ConnectorRepo{db: db}
}

func (r *ConnectorRepo) Create(ctx context.Context, c *Connector) error {
	if c.ID == "" {
		c.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if c.CreatedTime == 0 {
		c.CreatedTime = now
	}
	if c.UpdatedTime == 0 {
		c.UpdatedTime = now
	}
	c.Description = defaultStrPtr(c.Description, defaultDescriptionFallback)
	c.Subtype = defaultStrPtr(c.Subtype, "")
	c.Values = defaultStrPtr(c.Values, "{}")

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO connectors (id, name, description, type, subtype, "values", resource_key, content_hash, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, c.Type, c.Subtype, c.Values, c.ResourceKey, c.ContentHash,
		c.CreatedBy, c.UpdatedBy, c.CreatedTime, c.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("ConnectorRepo.Create(%s): %w", c.ID, err)
	}
	return nil
}

func (r *ConnectorRepo) GetByID(ctx context.Context, id string) (*Connector, error) {
	return r.getByIDOn(ctx, r.db, id)
}

func (r *ConnectorRepo) GetByIDInTx(ctx context.Context, tx *dbutil.Tx, id string) (*Connector, error) {
	return r.getByIDOn(ctx, tx, id)
}

func (r *ConnectorRepo) getByIDOn(ctx context.Context, q dbutil.Querier, id string) (*Connector, error) {
	return dbutil.QueryRow[Connector](ctx, q,
		`SELECT id, name, description, type, subtype, "values", resource_key, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM connectors WHERE id = ?`, id,
	)
}

func (r *ConnectorRepo) GetAllByName(ctx context.Context, name string) ([]*Connector, error) {
	connectors, err := dbutil.QueryRowPtrs[Connector](ctx, r.db,
		`SELECT id, name, description, type, subtype, "values", resource_key,
			created_by, updated_by, created_time, updated_time
		 FROM connectors WHERE name = ?`, name,
	)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetAllByName: %w", err)
	}
	return connectors, nil
}

func (r *ConnectorRepo) GetByNormalizedName(ctx context.Context, name string) (*Connector, error) {
	c, err := dbutil.QueryRow[Connector](ctx, r.db,
		`SELECT id, name, description, type, subtype, "values", resource_key, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM connectors WHERE LOWER(REPLACE(name, ' ', '')) = LOWER(REPLACE(?, ' ', ''))`, name,
	)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetByNormalizedName: %w", err)
	}
	return c, nil
}

func (r *ConnectorRepo) GetByIDs(ctx context.Context, ids []string) ([]*Connector, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetByIDs: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT id, name, description, type, subtype, "values", resource_key, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM connectors WHERE id IN (%s)`, ph,
	)
	connectors, err := dbutil.QueryRowPtrs[Connector](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetByIDs: %w", err)
	}
	return connectors, nil
}

type ConnectorSortField string

const (
	ConnectorSortDefault     ConnectorSortField = ""
	ConnectorSortName        ConnectorSortField = "name"
	ConnectorSortCreatedTime ConnectorSortField = "created_time"
)

type ConnectorOrder struct {
	Field ConnectorSortField
	Desc  bool
}

func (o ConnectorOrder) orderBy() string {
	switch {
	case o.Field == ConnectorSortName && o.Desc:
		return ` ORDER BY name DESC, id DESC`
	case o.Field == ConnectorSortName:
		return ` ORDER BY name ASC, id ASC`
	case o.Field == ConnectorSortCreatedTime && !o.Desc:
		return ` ORDER BY created_time ASC, id ASC`
	default:
		return ` ORDER BY created_time DESC, id DESC`
	}
}

type ConnectorFilter struct {
	Type         string
	Subtype      string
	Name         string
	ExcludeTypes []string
}

func (r *ConnectorRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int, filter ConnectorFilter, order ConnectorOrder) ([]*Connector, int, error) {
	where, args, err := buildConnectorFilter(filter)
	if err != nil {
		return nil, 0, fmt.Errorf("ConnectorRepo.ListPaginated filter: %w", err)
	}

	countQuery := `SELECT COUNT(*) FROM connectors` + where
	total, err := dbutil.QueryVal[int](ctx, r.db, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ConnectorRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	query := `SELECT id, name, description, type, subtype, "values", resource_key, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM connectors` + where + order.orderBy() + ` LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)

	connectors, err := dbutil.QueryRowPtrs[Connector](ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ConnectorRepo.ListPaginated query: %w", err)
	}

	return connectors, total, nil
}

func (r *ConnectorRepo) Update(ctx context.Context, c *Connector) error {
	return r.updateOn(ctx, r.db, c)
}

func (r *ConnectorRepo) UpdateInTx(ctx context.Context, tx *dbutil.Tx, c *Connector) error {
	return r.updateOn(ctx, tx, c)
}

func (r *ConnectorRepo) updateOn(ctx context.Context, q dbutil.Querier, c *Connector) error {
	c.UpdatedTime = time.Now().UnixMilli()
	result, err := q.ExecContext(ctx,
		`UPDATE connectors SET name = ?, description = ?, "values" = ?,
			updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		c.Name, c.Description, c.Values, c.UpdatedBy, c.UpdatedTime, c.ID,
	)
	if err != nil {
		return fmt.Errorf("update connector: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (r *ConnectorRepo) UpdateResourceKey(ctx context.Context, id, key, contentHash string) error {
	return r.updateResourceKeyOn(ctx, r.db, id, key, contentHash)
}

func (r *ConnectorRepo) UpdateResourceKeyInTx(ctx context.Context, tx *dbutil.Tx, id, key, contentHash string) error {
	return r.updateResourceKeyOn(ctx, tx, id, key, contentHash)
}

func (r *ConnectorRepo) updateResourceKeyOn(ctx context.Context, q dbutil.Querier, id, key, contentHash string) error {
	result, err := q.ExecContext(ctx,
		`UPDATE connectors SET resource_key = ?, content_hash = ?, updated_time = ? WHERE id = ?`,
		key, contentHash, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("update resource key: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (r *ConnectorRepo) UpdateValues(ctx context.Context, id, values string) error {
	return r.updateValuesOn(ctx, r.db, id, values)
}

func (r *ConnectorRepo) UpdateValuesInTx(ctx context.Context, tx *dbutil.Tx, id, values string) error {
	return r.updateValuesOn(ctx, tx, id, values)
}

func (r *ConnectorRepo) updateValuesOn(ctx context.Context, q dbutil.Querier, id, values string) error {
	result, err := q.ExecContext(ctx,
		`UPDATE connectors SET "values" = ?, updated_time = ? WHERE id = ?`,
		values, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("ConnectorRepo.UpdateValues(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (r *ConnectorRepo) Delete(ctx context.Context, id string) error {
	return r.deleteOn(ctx, r.db, id)
}

func (r *ConnectorRepo) DeleteInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteOn(ctx, tx, id)
}

func (r *ConnectorRepo) deleteOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx, `DELETE FROM connectors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("ConnectorRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("connector not found")
	}
	return nil
}

func (r *ConnectorRepo) GetAll(ctx context.Context) ([]*Connector, error) {
	return dbutil.QueryRowPtrs[Connector](ctx, r.db,
		`SELECT id, name, description, type, subtype, "values", resource_key, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM connectors ORDER BY created_time DESC`,
	)
}

func (r *ConnectorRepo) GetAgentCounts(ctx context.Context, connectorIDs []string) (map[string]int, error) {
	return r.getAgentCountsOn(ctx, r.db, connectorIDs)
}

func (r *ConnectorRepo) GetAgentCountsInTx(ctx context.Context, tx *dbutil.Tx, connectorIDs []string) (map[string]int, error) {
	return r.getAgentCountsOn(ctx, tx, connectorIDs)
}

func (r *ConnectorRepo) getAgentCountsOn(ctx context.Context, q dbutil.Querier, connectorIDs []string) (map[string]int, error) {
	if len(connectorIDs) == 0 {
		return map[string]int{}, nil
	}

	ph, args, err := dbutil.InClause(connectorIDs)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetAgentCounts: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT connector_id, COUNT(*) FROM agent_connectors
		 WHERE connector_id IN (%s) GROUP BY connector_id`,
		ph,
	)

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ConnectorRepo.GetAgentCounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var id string
		var count int
		if scanErr := rows.Scan(&id, &count); scanErr != nil {
			return nil, fmt.Errorf("ConnectorRepo.GetAgentCounts scan: %w", scanErr)
		}
		result[id] = count
	}
	return result, rows.Err()
}

func buildConnectorFilter(filter ConnectorFilter) (string, []any, error) {
	var conditions []string
	var args []any

	if filter.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, filter.Type)
	}
	if filter.Subtype != "" {
		conditions = append(conditions, "subtype = ?")
		args = append(args, filter.Subtype)
	}
	if filter.Name != "" {
		pred, arg := dbutil.LikeContainsFold("name", filter.Name)
		conditions = append(conditions, pred)
		args = append(args, arg)
	}
	if len(filter.ExcludeTypes) > 0 {
		placeholders, inArgs, err := dbutil.InClause(filter.ExcludeTypes)
		if err != nil {
			return "", nil, err
		}
		conditions = append(conditions, "type NOT IN ("+placeholders+")")
		args = append(args, inArgs...)
	}

	if len(conditions) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args, nil
}
