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

var ErrSkillNotFound = errors.New("skill not found")

type SkillRepo struct {
	db *DB
}

func NewSkillRepo(db *DB) *SkillRepo {
	return &SkillRepo{db: db}
}

func (r *SkillRepo) Create(ctx context.Context, s *Skill) error {
	return r.createOn(ctx, r.db, s)
}

func (r *SkillRepo) CreateInTx(ctx context.Context, tx *dbutil.Tx, s *Skill) error {
	return r.createOn(ctx, tx, s)
}

func (r *SkillRepo) createOn(ctx context.Context, q dbutil.Querier, s *Skill) error {
	if s.ID == "" {
		s.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if s.CreatedTime == 0 {
		s.CreatedTime = now
	}
	if s.UpdatedTime == 0 {
		s.UpdatedTime = now
	}
	if s.DiscoveryStatus == "" {
		s.DiscoveryStatus = "pending"
	}

	_, err := q.ExecContext(ctx,
		`INSERT INTO skills (id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.Description, s.Tags, s.Origin, s.DiscoveryStatus,
		dbutil.BoolToInt(s.HasReferences), dbutil.BoolToInt(s.HasAssets), dbutil.BoolToInt(s.HasTools), s.ResourceCount, s.ReferenceCount, s.AssetCount,
		s.BundledTools, s.Toolsets, s.ConfigYAML,
		s.CreatedBy, s.UpdatedBy, s.CreatedTime, s.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("insert skill: %w", err)
	}
	return nil
}

func (r *SkillRepo) GetByID(ctx context.Context, id string) (*Skill, error) {
	return dbutil.QueryRow[Skill](ctx, r.db,
		`SELECT id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM skills WHERE id = ?`, id,
	)
}

func (r *SkillRepo) GetByIDs(ctx context.Context, ids []string) ([]Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.GetByIDs in clause: %w", err)
	}
	return dbutil.QueryRows[Skill](ctx, r.db,
		`SELECT id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM skills WHERE id IN (`+ph+`)`, args...,
	)
}

func (r *SkillRepo) GetByName(ctx context.Context, name string) (*Skill, error) {
	return dbutil.QueryRow[Skill](ctx, r.db,
		`SELECT id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM skills WHERE name = ?`, name,
	)
}

type skillContentHashRow struct {
	Name        string  `db:"name"`
	ContentHash *string `db:"content_hash"`
}

func (r *SkillRepo) ContentHashesByName(ctx context.Context, names []string) (map[string]string, error) {
	hashes := make(map[string]string, len(names))
	if len(names) == 0 {
		return hashes, nil
	}
	ph, args, err := dbutil.InClause(names)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.ContentHashesByName in clause: %w", err)
	}
	rows, err := dbutil.QueryRows[skillContentHashRow](ctx, r.db,
		`SELECT name, content_hash FROM skills WHERE name IN (`+ph+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.ContentHashesByName: %w", err)
	}
	for _, row := range rows {
		if row.ContentHash != nil && *row.ContentHash != "" {
			hashes[row.Name] = *row.ContentHash
		}
	}
	return hashes, nil
}

func (r *SkillRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]Skill, int, error) {
	return r.ListWithOffset(ctx, (pageNumber-1)*pageSize, pageSize)
}

func (r *SkillRepo) ListWithOffset(ctx context.Context, offset, limit int) ([]Skill, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM skills`)
	if err != nil {
		return nil, 0, fmt.Errorf("SkillRepo.ListWithOffset count: %w", err)
	}
	if limit <= 0 {
		return []Skill{}, total, nil
	}

	skills, err := dbutil.QueryRows[Skill](ctx, r.db,
		`SELECT id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM skills ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("SkillRepo.ListWithOffset query: %w", err)
	}

	return skills, total, nil
}

func (r *SkillRepo) UpsertByName(ctx context.Context, s *Skill) error {
	return r.upsertByNameOn(ctx, r.db, s)
}

func (r *SkillRepo) UpsertByNameInTx(ctx context.Context, tx *dbutil.Tx, s *Skill) error {
	return r.upsertByNameOn(ctx, tx, s)
}

func (r *SkillRepo) upsertByNameOn(ctx context.Context, q dbutil.Querier, s *Skill) error {
	if s.ID == "" {
		s.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if s.CreatedTime == 0 {
		s.CreatedTime = now
	}
	if s.DiscoveryStatus == "" {
		s.DiscoveryStatus = "pending"
	}

	insertSQL := `INSERT INTO skills (id, name, description, tags, origin, discovery_status, has_references, has_assets, has_tools, resource_count, reference_count, asset_count, bundled_tools, toolsets, config_yaml, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "name",
		[]string{"description", "tags", "discovery_status", "has_references", "has_assets", "has_tools", "resource_count", "reference_count", "asset_count", "bundled_tools", "toolsets", "updated_by", "updated_time"})

	_, err := q.ExecContext(ctx, upsertSQL,
		s.ID, s.Name, s.Description, s.Tags, s.Origin, s.DiscoveryStatus,
		dbutil.BoolToInt(s.HasReferences), dbutil.BoolToInt(s.HasAssets), dbutil.BoolToInt(s.HasTools), s.ResourceCount, s.ReferenceCount, s.AssetCount,
		s.BundledTools, s.Toolsets, s.ConfigYAML,
		s.CreatedBy, s.UpdatedBy, s.CreatedTime, now)
	if err != nil {
		return fmt.Errorf("SkillRepo.UpsertByName(%s): %w", s.Name, err)
	}
	return nil
}

func (r *SkillRepo) Update(ctx context.Context, s *Skill) error {
	return r.updateOn(ctx, r.db, s)
}

func (r *SkillRepo) UpdateInTx(ctx context.Context, tx *dbutil.Tx, s *Skill) error {
	return r.updateOn(ctx, tx, s)
}

func (r *SkillRepo) updateOn(ctx context.Context, q dbutil.Querier, s *Skill) error {
	s.UpdatedTime = time.Now().UnixMilli()
	result, err := q.ExecContext(ctx,
		`UPDATE skills SET name = ?, description = ?, tags = ?, origin = ?, discovery_status = ?,
			has_references = ?, has_assets = ?, has_tools = ?, resource_count = ?, reference_count = ?, asset_count = ?,
			bundled_tools = ?, toolsets = ?, config_yaml = ?,
			updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		s.Name, s.Description, s.Tags, s.Origin, s.DiscoveryStatus,
		dbutil.BoolToInt(s.HasReferences), dbutil.BoolToInt(s.HasAssets), dbutil.BoolToInt(s.HasTools), s.ResourceCount, s.ReferenceCount, s.AssetCount,
		s.BundledTools, s.Toolsets, s.ConfigYAML,
		s.UpdatedBy, s.UpdatedTime, s.ID,
	)
	if err != nil {
		return fmt.Errorf("update skill: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSkillNotFound
	}
	return nil
}

func (r *SkillRepo) Delete(ctx context.Context, id string) error {
	return r.deleteOn(ctx, r.db, id)
}

func (r *SkillRepo) DeleteInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteOn(ctx, tx, id)
}

func (r *SkillRepo) deleteOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("SkillRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrSkillNotFound
	}
	return nil
}

func (r *SkillRepo) SetDiscoveryStatus(ctx context.Context, name, status string) error {
	return r.setDiscoveryStatusOn(ctx, r.db, name, status)
}

func (r *SkillRepo) SetDiscoveryStatusInTx(ctx context.Context, tx *dbutil.Tx, name, status string) error {
	return r.setDiscoveryStatusOn(ctx, tx, name, status)
}

func (r *SkillRepo) setDiscoveryStatusOn(ctx context.Context, q dbutil.Querier, name, status string) error {
	now := time.Now().UnixMilli()
	result, err := q.ExecContext(ctx,
		`UPDATE skills SET discovery_status = ?, updated_time = ? WHERE name = ?`,
		status, now, name,
	)
	if err != nil {
		return fmt.Errorf("SkillRepo.SetDiscoveryStatus(%s): %w", name, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrSkillNotFound
	}
	return nil
}

func (r *SkillRepo) SetContentHashInTx(ctx context.Context, tx *dbutil.Tx, name, hash string) error {
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx,
		`UPDATE skills SET content_hash = ?, updated_time = ? WHERE name = ?`,
		hash, now, name,
	)
	if err != nil {
		return fmt.Errorf("SkillRepo.SetContentHash(%s): %w", name, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrSkillNotFound
	}
	return nil
}

func (r *SkillRepo) FailStalePending(ctx context.Context, id string, cutoffMs int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE skills SET discovery_status = 'failed', updated_time = ?
		 WHERE id = ? AND discovery_status = 'pending' AND updated_time < ?`,
		time.Now().UnixMilli(), id, cutoffMs,
	)
	if err != nil {
		return false, fmt.Errorf("SkillRepo.FailStalePending(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

func (r *SkillRepo) SetAgentSkills(ctx context.Context, agentID string, skillIDs []string) error {
	rows := make([]AgentSkillRow, len(skillIDs))
	for i, id := range skillIDs {
		rows[i] = AgentSkillRow{AgentID: agentID, SkillID: id}
	}
	return r.SetAgentSkillsWithConfigs(ctx, agentID, rows)
}

func (r *SkillRepo) SetAgentSkillsWithConfigs(ctx context.Context, agentID string, rows []AgentSkillRow) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM agent_skills WHERE agent_id = ?`, agentID,
		); err != nil {
			return fmt.Errorf("clear agent_skills: %w", err)
		}

		for _, row := range rows {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO agent_skills (agent_id, skill_id, config_values) VALUES (?, ?, ?)`,
				agentID, row.SkillID, row.ConfigValues,
			); err != nil {
				return fmt.Errorf("insert agent_skill %s: %w", row.SkillID, err)
			}
		}
		return nil
	})
}

func (r *SkillRepo) GetAgentSkillRowsForAgent(ctx context.Context, agentID string) ([]AgentSkillRow, error) {
	return dbutil.QueryRows[AgentSkillRow](ctx, r.db,
		`SELECT agent_id, skill_id, config_values FROM agent_skills WHERE agent_id = ? ORDER BY skill_id`,
		agentID,
	)
}

func (r *SkillRepo) GetAgentIDsBySkillID(ctx context.Context, skillID string) ([]string, error) {
	type idRow struct {
		AgentID string `db:"agent_id"`
	}
	rows, err := dbutil.QueryRows[idRow](ctx, r.db,
		`SELECT agent_id FROM agent_skills WHERE skill_id = ? ORDER BY agent_id`,
		skillID,
	)
	if err != nil {
		return nil, fmt.Errorf("GetAgentIDsBySkillID: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.AgentID)
	}
	return ids, nil
}

func (r *SkillRepo) GetSkillConfig(ctx context.Context, skillID string) (*SkillConfig, error) {
	return dbutil.QueryRow[SkillConfig](ctx, r.db,
		`SELECT skill_id, config_values, updated_by, updated_time
		 FROM skill_configs WHERE skill_id = ?`, skillID,
	)
}

func (r *SkillRepo) GetSkillConfigsForSkills(ctx context.Context, skillIDs []string) (map[string]SkillConfig, error) {
	result := make(map[string]SkillConfig, len(skillIDs))
	if len(skillIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(skillIDs)
	if err != nil {
		return nil, fmt.Errorf("GetSkillConfigsForSkills: %w", err)
	}
	rows, err := dbutil.QueryRows[SkillConfig](ctx, r.db,
		`SELECT skill_id, config_values, updated_by, updated_time
		 FROM skill_configs WHERE skill_id IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetSkillConfigsForSkills: %w", err)
	}
	for _, row := range rows {
		result[row.SkillID] = row
	}
	return result, nil
}

func (r *SkillRepo) SetSkillConfig(ctx context.Context, skillID string, configValues *string, updatedBy string) error {
	return r.setSkillConfigOn(ctx, r.db, skillID, configValues, updatedBy)
}

func (r *SkillRepo) SetSkillConfigInTx(ctx context.Context, tx *dbutil.Tx, skillID string, configValues *string, updatedBy string) error {
	return r.setSkillConfigOn(ctx, tx, skillID, configValues, updatedBy)
}

func (r *SkillRepo) setSkillConfigOn(ctx context.Context, q dbutil.Querier, skillID string, configValues *string, updatedBy string) error {
	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO skill_configs (skill_id, config_values, updated_by, updated_time)
		VALUES (?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "skill_id",
		[]string{"config_values", "updated_by", "updated_time"})
	if _, err := q.ExecContext(ctx, upsertSQL,
		skillID, configValues, updatedBy, now,
	); err != nil {
		return fmt.Errorf("upsert skill_config: %w", err)
	}
	return nil
}

func (r *SkillRepo) GetSkillIDsForAgent(ctx context.Context, agentID string) ([]string, error) {
	type row struct {
		SkillID string `db:"skill_id"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT skill_id FROM agent_skills WHERE agent_id = ? ORDER BY skill_id`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.GetSkillIDsForAgent(%s): %w", agentID, err)
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.SkillID
	}
	return ids, nil
}

func (r *SkillRepo) GetSkillIDsForAgents(ctx context.Context, agentIDs []string) (map[string][]string, error) {
	if len(agentIDs) == 0 {
		return map[string][]string{}, nil
	}
	type row struct {
		AgentID string `db:"agent_id"`
		SkillID string `db:"skill_id"`
	}
	ph, args, err := dbutil.InClause(agentIDs)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.GetSkillIDsForAgents: build IN clause: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT agent_id, skill_id FROM agent_skills WHERE agent_id IN (%s) ORDER BY agent_id, skill_id`,
		ph,
	)
	rows, err := dbutil.QueryRows[row](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.GetSkillIDsForAgents: %w", err)
	}
	out := make(map[string][]string, len(agentIDs))
	for _, r := range rows {
		out[r.AgentID] = append(out[r.AgentID], r.SkillID)
	}
	return out, nil
}

func (r *SkillRepo) ListPendingOlderThan(ctx context.Context, cutoffMs int64) ([]Skill, error) {
	return dbutil.QueryRows[Skill](ctx, r.db,
		`SELECT id, name, description, tags, origin, discovery_status,
			has_references, has_assets, has_tools, resource_count, reference_count, asset_count,
			bundled_tools, toolsets, config_yaml, content_hash,
			created_by, updated_by, created_time, updated_time
		 FROM skills
		 WHERE discovery_status = 'pending' AND updated_time < ?`, cutoffMs,
	)
}

func (r *SkillRepo) GetAgentsUsingSkill(ctx context.Context, skillID string) ([]string, error) {
	type row struct {
		AgentID string `db:"agent_id"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT agent_id FROM agent_skills WHERE skill_id = ? ORDER BY agent_id`, skillID,
	)
	if err != nil {
		return nil, fmt.Errorf("SkillRepo.GetAgentsUsingSkill(%s): %w", skillID, err)
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.AgentID
	}
	return ids, nil
}

type SkillAgentRef struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
}

type skillAgentRefWithSkillID struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	SkillID     string `db:"skill_id"`
}

func (r *SkillRepo) GetAgentsBySkillIDs(ctx context.Context, skillIDs []string) (map[string][]SkillAgentRef, error) {
	result := make(map[string][]SkillAgentRef, len(skillIDs))
	for _, id := range skillIDs {
		result[id] = nil
	}
	if len(skillIDs) == 0 {
		return result, nil
	}

	placeholders, args, err := dbutil.InClause(skillIDs)
	if err != nil {
		return nil, fmt.Errorf("GetAgentsBySkillIDs: %w", err)
	}
	rows, err := dbutil.QueryRows[skillAgentRefWithSkillID](ctx, r.db,
		`SELECT a.id, a.name, COALESCE(a.description, '') as description, ask.skill_id as skill_id
		 FROM agents a
		 INNER JOIN agent_skills ask ON a.id = ask.agent_id
		 WHERE ask.skill_id IN (`+placeholders+`)
		 ORDER BY a.name`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetAgentsBySkillIDs: %w", err)
	}
	for _, row := range rows {
		result[row.SkillID] = append(result[row.SkillID], SkillAgentRef{
			ID:          row.ID,
			Name:        row.Name,
			Description: row.Description,
		})
	}
	return result, nil
}

func (r *SkillRepo) GetAgentsBySkillRefNames(ctx context.Context, names []string) (map[string][]SkillAgentRef, error) {
	result := make(map[string][]SkillAgentRef, len(names))
	for _, n := range names {
		result[n] = nil
	}
	if len(names) == 0 {
		return result, nil
	}
	wanted := make(map[string]struct{}, len(names))
	for _, n := range names {
		wanted[n] = struct{}{}
	}

	type agentRow struct {
		ID          string  `db:"id"`
		Name        string  `db:"name"`
		Description string  `db:"description"`
		SkillRefs   *string `db:"skill_refs"`
	}
	rows, err := dbutil.QueryRows[agentRow](ctx, r.db,
		`SELECT id, name, COALESCE(description, '') as description, skill_refs
		 FROM agents
		 WHERE skill_refs IS NOT NULL AND skill_refs != ''
		 ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("GetAgentsBySkillRefNames: %w", err)
	}
	for _, row := range rows {
		if row.SkillRefs == nil {
			continue
		}
		var refs []string
		if uErr := json.Unmarshal([]byte(*row.SkillRefs), &refs); uErr != nil {
			continue
		}
		for _, ref := range refs {
			if _, ok := wanted[ref]; ok {
				result[ref] = append(result[ref], SkillAgentRef{
					ID:          row.ID,
					Name:        row.Name,
					Description: row.Description,
				})
			}
		}
	}
	return result, nil
}
