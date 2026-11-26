// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var ErrLinkAgentNotFound = errors.New("link agent not found")

type LinkAgentWake struct {
	URL            string            `json:"url"`
	TokenURL       string            `json:"token_url,omitempty"`
	ClientID       string            `json:"client_id,omitempty"`
	ClientSecret   string            `json:"client_secret,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	OnStart        bool              `json:"on_start"`
}

type LinkAgent struct {
	ID               string
	Name             string
	Description      string
	EntrypointID     string
	TokenSHA256      string
	TokenPrefix      string
	Enabled          bool
	Owner            string
	Team             string
	Peers            []string
	ShareUserProfile bool
	Wake             *LinkAgentWake
	CreatedBy        string
	UpdatedBy        string
	CreatedTime      int64
	UpdatedTime      int64
}

type linkAgentRow struct {
	ID               string  `db:"id"`
	Name             string  `db:"name"`
	Description      *string `db:"description"`
	EntrypointID     string  `db:"entrypoint_id"`
	TokenSHA256      string  `db:"token_sha256"`
	TokenPrefix      string  `db:"token_prefix"`
	Enabled          bool    `db:"enabled"`
	Owner            string  `db:"owner"`
	Team             string  `db:"team"`
	Peers            string  `db:"peers"`
	ShareUserProfile bool    `db:"share_user_profile"`
	Wake             *string `db:"wake"`
	CreatedBy        string  `db:"created_by"`
	UpdatedBy        string  `db:"updated_by"`
	CreatedTime      int64   `db:"created_time"`
	UpdatedTime      int64   `db:"updated_time"`
}

const linkAgentSelect = `SELECT l.id, a.name, a.description, l.entrypoint_id, l.token_sha256, l.token_prefix,
		l.enabled, l.owner, l.team, l.peers, l.share_user_profile, l.wake,
		l.created_by, l.updated_by, l.created_time, l.updated_time
	 FROM link_agents l JOIN agents a ON a.id = l.id`

const linkAgentOrder = ` ORDER BY l.created_time DESC, l.id DESC`

func (row *linkAgentRow) linkAgent() (*LinkAgent, error) {
	a := &LinkAgent{
		ID: row.ID, Name: row.Name, EntrypointID: row.EntrypointID,
		TokenSHA256: row.TokenSHA256, TokenPrefix: row.TokenPrefix,
		Enabled: row.Enabled, Owner: row.Owner, Team: row.Team, ShareUserProfile: row.ShareUserProfile,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedTime: row.CreatedTime, UpdatedTime: row.UpdatedTime,
	}
	if row.Description != nil {
		a.Description = *row.Description
	}
	if err := json.Unmarshal([]byte(row.Peers), &a.Peers); err != nil {
		return nil, fmt.Errorf("link agent %s: stored peers: %w", row.ID, err)
	}
	if row.Wake != nil && *row.Wake != "" {
		a.Wake = &LinkAgentWake{}
		if err := json.Unmarshal([]byte(*row.Wake), a.Wake); err != nil {
			return nil, fmt.Errorf("link agent %s: stored wake settings: %w", row.ID, err)
		}
	}
	return a, nil
}

func linkAgentsOf(rows []*linkAgentRow) ([]*LinkAgent, error) {
	agents := make([]*LinkAgent, 0, len(rows))
	for _, row := range rows {
		a, err := row.linkAgent()
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func storedLinkAgentLists(a *LinkAgent) (peers string, wake *string, err error) {
	names := a.Peers
	if names == nil {
		names = []string{}
	}
	peersJSON, err := json.Marshal(names)
	if err != nil {
		return "", nil, fmt.Errorf("encode peers: %w", err)
	}
	if a.Wake == nil {
		return string(peersJSON), nil, nil
	}
	wakeJSON, err := json.Marshal(a.Wake)
	if err != nil {
		return "", nil, fmt.Errorf("encode wake settings: %w", err)
	}
	encoded := string(wakeJSON)
	return string(peersJSON), &encoded, nil
}

type LinkAgentRepo struct {
	db *DB
}

func NewLinkAgentRepo(db *DB) *LinkAgentRepo {
	return &LinkAgentRepo{db: db}
}

func (r *LinkAgentRepo) DB() *DB { return r.db }

func (r *LinkAgentRepo) Create(ctx context.Context, a *LinkAgent) error {
	if a.ID == "" {
		a.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if a.CreatedTime == 0 {
		a.CreatedTime = now
	}
	if a.UpdatedTime == 0 {
		a.UpdatedTime = now
	}
	peers, wake, err := storedLinkAgentLists(a)
	if err != nil {
		return fmt.Errorf("LinkAgentRepo.Create(%s): %w", a.ID, err)
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		mirror := &Agent{
			ID: a.ID, Name: a.Name, Description: &a.Description, Type: AgentTypeLink,
			CreatedBy: a.CreatedBy, UpdatedBy: a.UpdatedBy, CreatedTime: a.CreatedTime, UpdatedTime: a.UpdatedTime,
		}
		if err := insertAgentRow(ctx, tx, mirror); err != nil {
			return fmt.Errorf("LinkAgentRepo.Create(%s): %w", a.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO link_agents (id, entrypoint_id, token_sha256, token_prefix, enabled, owner, team, peers,
				share_user_profile, wake, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.EntrypointID, a.TokenSHA256, a.TokenPrefix, dbutil.BoolToInt(a.Enabled), a.Owner, a.Team, peers,
			dbutil.BoolToInt(a.ShareUserProfile), wake, a.CreatedBy, a.UpdatedBy, a.CreatedTime, a.UpdatedTime,
		); err != nil {
			return fmt.Errorf("LinkAgentRepo.Create(%s) settings: %w", a.ID, err)
		}
		return nil
	})
}

func (r *LinkAgentRepo) GetByID(ctx context.Context, id string) (*LinkAgent, error) {
	row, err := dbutil.QueryRow[linkAgentRow](ctx, r.db, linkAgentSelect+` WHERE l.id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("LinkAgentRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, nil
	}
	return row.linkAgent()
}

func (r *LinkAgentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]*LinkAgent, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM link_agents`)
	if err != nil {
		return nil, 0, fmt.Errorf("LinkAgentRepo.ListPaginated count: %w", err)
	}
	rows, err := dbutil.QueryRowPtrs[linkAgentRow](ctx, r.db,
		linkAgentSelect+linkAgentOrder+` LIMIT ? OFFSET ?`, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("LinkAgentRepo.ListPaginated query: %w", err)
	}
	agents, err := linkAgentsOf(rows)
	return agents, total, err
}

func (r *LinkAgentRepo) ListAll(ctx context.Context) ([]*LinkAgent, error) {
	rows, err := dbutil.QueryRowPtrs[linkAgentRow](ctx, r.db, linkAgentSelect+linkAgentOrder)
	if err != nil {
		return nil, fmt.Errorf("LinkAgentRepo.ListAll: %w", err)
	}
	return linkAgentsOf(rows)
}

func (r *LinkAgentRepo) ListByEntrypoint(ctx context.Context, entrypointID string) ([]*LinkAgent, error) {
	rows, err := dbutil.QueryRowPtrs[linkAgentRow](ctx, r.db,
		linkAgentSelect+` WHERE l.entrypoint_id = ?`+linkAgentOrder, entrypointID)
	if err != nil {
		return nil, fmt.Errorf("LinkAgentRepo.ListByEntrypoint(%s): %w", entrypointID, err)
	}
	return linkAgentsOf(rows)
}

func (r *LinkAgentRepo) Update(ctx context.Context, a *LinkAgent) error {
	return r.update(ctx, a, false)
}

func (r *LinkAgentRepo) UpdateWithToken(ctx context.Context, a *LinkAgent) error {
	return r.update(ctx, a, true)
}

func IsLinkAgentTokenViolation(err error) bool {
	return dbutil.IsUniqueViolationOn(err, "link_agents", "token_sha256")
}

func (r *LinkAgentRepo) update(ctx context.Context, a *LinkAgent, withToken bool) error {
	a.UpdatedTime = time.Now().UnixMilli()
	peers, wake, err := storedLinkAgentLists(a)
	if err != nil {
		return fmt.Errorf("LinkAgentRepo.Update(%s): %w", a.ID, err)
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE link_agents SET entrypoint_id = ?, enabled = ?, owner = ?, team = ?, peers = ?,
				share_user_profile = ?, wake = ?, updated_by = ?, updated_time = ?
			 WHERE id = ?`,
			a.EntrypointID, dbutil.BoolToInt(a.Enabled), a.Owner, a.Team, peers,
			dbutil.BoolToInt(a.ShareUserProfile), wake, a.UpdatedBy, a.UpdatedTime, a.ID,
		)
		if err != nil {
			return fmt.Errorf("LinkAgentRepo.Update(%s): %w", a.ID, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrLinkAgentNotFound
		}
		if withToken {
			if _, err := tx.ExecContext(ctx,
				`UPDATE link_agents SET token_sha256 = ?, token_prefix = ? WHERE id = ?`,
				a.TokenSHA256, a.TokenPrefix, a.ID,
			); err != nil {
				return fmt.Errorf("LinkAgentRepo.Update(%s) token: %w", a.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE agents SET name = ?, description = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
			a.Name, a.Description, a.UpdatedBy, a.UpdatedTime, a.ID,
		); err != nil {
			return fmt.Errorf("LinkAgentRepo.Update(%s) mirror: %w", a.ID, err)
		}
		return nil
	})
}

func (r *LinkAgentRepo) TokenDigestHeldByAnother(ctx context.Context, tokenSHA256, exceptID string) (bool, error) {
	held, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM link_agents WHERE token_sha256 = ? AND id <> ?`, tokenSHA256, exceptID)
	if err != nil {
		return false, fmt.Errorf("LinkAgentRepo.TokenDigestHeldByAnother: %w", err)
	}
	return held > 0, nil
}

func (r *LinkAgentRepo) SetToken(ctx context.Context, id, tokenSHA256, tokenPrefix, updatedBy string) error {
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE link_agents SET token_sha256 = ?, token_prefix = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
			tokenSHA256, tokenPrefix, updatedBy, now, id)
		if err != nil {
			return fmt.Errorf("LinkAgentRepo.SetToken(%s): %w", id, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrLinkAgentNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agents SET updated_by = ?, updated_time = ? WHERE id = ?`, updatedBy, now, id); err != nil {
			return fmt.Errorf("LinkAgentRepo.SetToken(%s) mirror: %w", id, err)
		}
		return nil
	})
}

func (r *LinkAgentRepo) Delete(ctx context.Context, id string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM link_agents WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("LinkAgentRepo.Delete(%s): %w", id, err)
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrLinkAgentNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE id = ? AND type = ?`, id, AgentTypeLink); err != nil {
			return fmt.Errorf("LinkAgentRepo.Delete(%s) mirror: %w", id, err)
		}
		return nil
	})
}

const deliveredLinkAgentRows = `(status = 'success' OR (failure_reason = 'runtime_error' AND action = 'undeploy'))`

const linkAgentDeliveredDeployments = `SELECT ` + agentDeploymentCols + ` FROM (
	  SELECT ` + agentDeploymentCols + `, ROW_NUMBER() OVER (PARTITION BY agent_id ORDER BY ` + newestFirst + `) AS newest
	  FROM agent_deployments
	  WHERE agent_id IN (SELECT id FROM link_agents) AND ` + deliveredLinkAgentRows + `
	) ranked WHERE newest = 1`

func (r *LinkAgentRepo) DeliveredDeployments(ctx context.Context) (map[string]*AgentDeployment, error) {
	rows, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db, linkAgentDeliveredDeployments)
	if err != nil {
		return nil, fmt.Errorf("LinkAgentRepo.DeliveredDeployments: %w", err)
	}
	delivered := make(map[string]*AgentDeployment, len(rows))
	for _, row := range rows {
		delivered[row.AgentID] = row
	}
	return delivered, nil
}

func (r *LinkAgentRepo) IDs(ctx context.Context) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db, `SELECT id FROM link_agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("LinkAgentRepo.IDs: %w", err)
	}
	return ids, nil
}
