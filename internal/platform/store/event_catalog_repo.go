// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var (
	ErrEventCatalogNameTaken = errors.New("event catalog: name taken")
	ErrEventCatalogKeyTaken  = errors.New("event catalog: key taken")
)

type EventCatalogEntry struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	Key            string  `db:"event_key"`
	Description    string  `db:"description"`
	EntrypointID   string  `db:"entrypoint_id"`
	TopicPattern   string  `db:"topic_pattern"`
	PayloadSchema  *string `db:"payload_schema"`
	ExamplePayload *string `db:"example_payload"`
	Enabled        bool    `db:"enabled"`
	CreatedBy      string  `db:"created_by"`
	UpdatedBy      string  `db:"updated_by"`
	CreatedTime    int64   `db:"created_time"`
	UpdatedTime    int64   `db:"updated_time"`
}

const eventCatalogColumns = `id, name, event_key, description, entrypoint_id, topic_pattern, payload_schema, example_payload, enabled, created_by, updated_by, created_time, updated_time`

var eventCatalogSortColumns = map[string]string{
	"name":        "LOWER(name)",
	"key":         "event_key",
	"createdTime": "created_time",
	"updatedTime": "updated_time",
}

type EventCatalogListOptions struct {
	Enabled      *bool
	EntrypointID string
	PageQuery
}

type EventCatalogRepo struct {
	db *DB
}

func NewEventCatalogRepo(db *DB) *EventCatalogRepo {
	return &EventCatalogRepo{db: db}
}

func (r *EventCatalogRepo) Create(ctx context.Context, e *EventCatalogEntry) error {
	now := time.Now().UnixMilli()
	e.CreatedTime, e.UpdatedTime = now, now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO event_catalog_entries (`+eventCatalogColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Key, e.Description, e.EntrypointID, e.TopicPattern, e.PayloadSchema, e.ExamplePayload,
		dbutil.BoolToInt(e.Enabled), e.CreatedBy, e.UpdatedBy, e.CreatedTime, e.UpdatedTime)
	if err != nil {
		return fmt.Errorf("insert event catalog entry: %w", classifyEventCatalogWrite(err))
	}
	return nil
}

func (r *EventCatalogRepo) Get(ctx context.Context, id string) (*EventCatalogEntry, error) {
	e, err := dbutil.QueryRow[EventCatalogEntry](ctx, r.db,
		`SELECT `+eventCatalogColumns+` FROM event_catalog_entries WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EventCatalogRepo.Get(%s): %w", id, err)
	}
	return e, nil
}

func (r *EventCatalogRepo) NameTaken(ctx context.Context, name, exceptID string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM event_catalog_entries WHERE LOWER(name) = LOWER(?) AND id <> ?`, name, exceptID)
	if err != nil {
		return false, fmt.Errorf("EventCatalogRepo.NameTaken: %w", err)
	}
	return n > 0, nil
}

func (r *EventCatalogRepo) KeyTaken(ctx context.Context, key string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM event_catalog_entries WHERE event_key = ?`, key)
	if err != nil {
		return false, fmt.Errorf("EventCatalogRepo.KeyTaken: %w", err)
	}
	return n > 0, nil
}

func (r *EventCatalogRepo) CountByEntrypoint(ctx context.Context, entrypointID string) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM event_catalog_entries WHERE entrypoint_id = ?`, entrypointID)
	if err != nil {
		return 0, fmt.Errorf("EventCatalogRepo.CountByEntrypoint(%s): %w", entrypointID, err)
	}
	return n, nil
}

func (r *EventCatalogRepo) ListPage(ctx context.Context, opts EventCatalogListOptions) ([]*EventCatalogEntry, int, error) {
	var conds []string
	var args []any
	if opts.Enabled != nil {
		conds = append(conds, "enabled = ?")
		args = append(args, dbutil.BoolToInt(*opts.Enabled))
	}
	if opts.EntrypointID != "" {
		conds = append(conds, "entrypoint_id = ?")
		args = append(args, opts.EntrypointID)
	}
	entries, total, err := pageRows[EventCatalogEntry](ctx, r.db, pageSource{
		columns:     eventCatalogColumns,
		from:        "event_catalog_entries",
		where:       strings.Join(conds, " AND "),
		args:        args,
		sortColumns: eventCatalogSortColumns,
		defaultSort: "name",
	}, opts.PageQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("EventCatalogRepo.ListPage: %w", err)
	}
	return entries, total, nil
}

func (r *EventCatalogRepo) Update(ctx context.Context, e *EventCatalogEntry) (bool, error) {
	e.UpdatedTime = time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx,
		`UPDATE event_catalog_entries SET name = ?, description = ?, entrypoint_id = ?, topic_pattern = ?,
			payload_schema = ?, example_payload = ?, enabled = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		e.Name, e.Description, e.EntrypointID, e.TopicPattern, e.PayloadSchema, e.ExamplePayload,
		dbutil.BoolToInt(e.Enabled), e.UpdatedBy, e.UpdatedTime, e.ID)
	if err != nil {
		return false, fmt.Errorf("update event catalog entry: %w", classifyEventCatalogWrite(err))
	}
	return rowsChanged(res)
}

func (r *EventCatalogRepo) Delete(ctx context.Context, id string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM event_catalog_entries WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete event catalog entry: %w", err)
	}
	return rowsChanged(res)
}

func classifyEventCatalogWrite(err error) error {
	switch {
	case dbutil.IsUniqueViolationOn(err, "event_catalog_entries", "event_key"):
		return fmt.Errorf("%w: %w", ErrEventCatalogKeyTaken, err)
	case dbutil.IsUniqueViolation(err):
		return fmt.Errorf("%w: %w", ErrEventCatalogNameTaken, err)
	default:
		return err
	}
}
