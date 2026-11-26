// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type AgentRepo struct {
	db *DB
}

func NewAgentRepo(db *DB) *AgentRepo {
	return &AgentRepo{db: db}
}

func (r *AgentRepo) DB() *DB { return r.db }

func (r *AgentRepo) Create(ctx context.Context, a *Agent, toolsets, inputModes, outputModes, connectorIDs []string, exclusions map[string]string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := insertAgentRow(ctx, tx, a); err != nil {
			return err
		}

		if err := r.setToolsetsWithExclusions(ctx, tx, a.ID, toolsets, exclusions); err != nil {
			return fmt.Errorf("AgentRepo.Create(%s) set toolsets: %w", a.ID, err)
		}
		if err := r.setInputModes(ctx, tx, a.ID, inputModes); err != nil {
			return fmt.Errorf("AgentRepo.Create(%s) set input modes: %w", a.ID, err)
		}
		if err := r.setOutputModes(ctx, tx, a.ID, outputModes); err != nil {
			return fmt.Errorf("AgentRepo.Create(%s) set output modes: %w", a.ID, err)
		}
		return r.setConnectors(ctx, tx, a.ID, connectorIDs)
	})
}

func (r *AgentRepo) GetByName(ctx context.Context, name string) (*Agent, []string, []string, []string, []string, error) {
	a, err := dbutil.QueryRow[Agent](ctx, r.db,
		`SELECT id, name, description, type, system_prompt, skills, skill_refs, model_provider, additional_configurations,
			created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind
		 FROM agents WHERE LOWER(REPLACE(name, ' ', '')) = ?`, normalizeName(name),
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByName(%s): %w", name, err)
	}
	if a == nil {
		return nil, nil, nil, nil, nil, nil
	}

	toolsets, err := r.getToolsets(ctx, a.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByName(%s) toolsets: %w", name, err)
	}
	inputModes, err := r.getInputModes(ctx, a.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByName(%s) input modes: %w", name, err)
	}
	outputModes, err := r.getOutputModes(ctx, a.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByName(%s) output modes: %w", name, err)
	}
	connectorIDs, err := r.getConnectors(ctx, a.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByName(%s) connectors: %w", name, err)
	}

	return a, toolsets, inputModes, outputModes, connectorIDs, nil
}

func (r *AgentRepo) GetByID(ctx context.Context, id string) (*Agent, []string, []string, []string, []string, error) {
	a, err := dbutil.QueryRow[Agent](ctx, r.db,
		`SELECT id, name, description, type, system_prompt, skills, skill_refs, model_provider, additional_configurations,
			created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind
		 FROM agents WHERE id = ?`, id,
	)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByID(%s): %w", id, err)
	}
	if a == nil {
		return nil, nil, nil, nil, nil, nil
	}

	toolsets, err := r.getToolsets(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByID(%s) toolsets: %w", id, err)
	}
	inputModes, err := r.getInputModes(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByID(%s) input modes: %w", id, err)
	}
	outputModes, err := r.getOutputModes(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByID(%s) output modes: %w", id, err)
	}
	connectorIDs, err := r.getConnectors(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("AgentRepo.GetByID(%s) connectors: %w", id, err)
	}

	return a, toolsets, inputModes, outputModes, connectorIDs, nil
}

type AgentWithRelations struct {
	Agent         *Agent
	Toolsets      []string
	ExcludedTools map[string]string
	InputModes    []string
	OutputModes   []string
	ConnectorIDs  []string
}

func (r *AgentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]AgentWithRelations, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM agents WHERE type NOT IN ('a2a_proxy', 'link') AND is_ephemeral = 0`)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	agents, err := dbutil.QueryRowPtrs[Agent](ctx, r.db,
		`SELECT id, name, description, type, system_prompt, skills, skill_refs, model_provider, additional_configurations,
			created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind
		 FROM agents WHERE type NOT IN ('a2a_proxy', 'link') AND is_ephemeral = 0
		 ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentRepo.ListPaginated query: %w", err)
	}

	result := make([]AgentWithRelations, len(agents))
	for i, a := range agents {
		toolsets, excl, tErr := r.GetToolsetsWithExclusions(ctx, a.ID)
		if tErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListPaginated(%s) toolsets: %w", a.ID, tErr)
		}
		inputModes, imErr := r.getInputModes(ctx, a.ID)
		if imErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListPaginated(%s) input modes: %w", a.ID, imErr)
		}
		outputModes, omErr := r.getOutputModes(ctx, a.ID)
		if omErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListPaginated(%s) output modes: %w", a.ID, omErr)
		}
		connectorIDs, cErr := r.getConnectors(ctx, a.ID)
		if cErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListPaginated(%s) connectors: %w", a.ID, cErr)
		}
		result[i] = AgentWithRelations{
			Agent:         a,
			Toolsets:      toolsets,
			ExcludedTools: excl,
			InputModes:    inputModes,
			OutputModes:   outputModes,
			ConnectorIDs:  connectorIDs,
		}
	}

	return result, total, nil
}

const defaultAdditionalConfigurationsJSON = "{}"

func (r *AgentRepo) Update(ctx context.Context, a *Agent, toolsets, inputModes, outputModes, connectorIDs []string, exclusions map[string]string) error {
	a.UpdatedTime = time.Now().UnixMilli()
	if len(a.AdditionalConfigurations) == 0 {
		a.AdditionalConfigurations = []byte(defaultAdditionalConfigurationsJSON)
	}

	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE agents SET name = ?, description = ?, system_prompt = ?, skills = ?, skill_refs = ?,
				model_provider = ?, additional_configurations = ?, updated_by = ?, updated_time = ?
			 WHERE id = ?`,
			a.Name, a.Description, a.SystemPrompt, a.Skills, a.SkillRefs,
			a.ModelProvider, a.AdditionalConfigurations, a.UpdatedBy, a.UpdatedTime, a.ID,
		)
		if err != nil {
			return fmt.Errorf("update agent: %w", err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return fmt.Errorf("agent not found")
		}

		if err := r.setToolsetsWithExclusions(ctx, tx, a.ID, toolsets, exclusions); err != nil {
			return fmt.Errorf("AgentRepo.Update(%s) set toolsets: %w", a.ID, err)
		}
		if err := r.setInputModes(ctx, tx, a.ID, inputModes); err != nil {
			return fmt.Errorf("AgentRepo.Update(%s) set input modes: %w", a.ID, err)
		}
		if err := r.setOutputModes(ctx, tx, a.ID, outputModes); err != nil {
			return fmt.Errorf("AgentRepo.Update(%s) set output modes: %w", a.ID, err)
		}
		return r.setConnectors(ctx, tx, a.ID, connectorIDs)
	})
}

func (r *AgentRepo) GetAll(ctx context.Context) ([]*Agent, error) {
	return r.getAll(ctx, `WHERE is_ephemeral = 0`)
}

func (r *AgentRepo) GetAllWithEphemeral(ctx context.Context) ([]*Agent, error) {
	return r.getAll(ctx, ``)
}

func (r *AgentRepo) getAll(ctx context.Context, where string) ([]*Agent, error) {
	return dbutil.QueryRowPtrs[Agent](ctx, r.db,
		`SELECT id, name, description, type, system_prompt, skills, skill_refs, model_provider, additional_configurations,
			created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind
		 FROM agents `+where+` ORDER BY created_time DESC`,
	)
}

func (r *AgentRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("AgentRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (r *AgentRepo) setToolsetsWithExclusions(ctx context.Context, q dbutil.Querier, agentID string, toolsets []string, exclusions map[string]string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM agent_toolsets WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("clear agent_toolsets: %w", err)
	}
	for _, ts := range toolsets {
		var excl *string
		if exclusions != nil {
			if v, ok := exclusions[ts]; ok {
				excl = &v
			}
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO agent_toolsets (agent_id, toolset, excluded_tools) VALUES (?, ?, ?)`,
			agentID, ts, excl,
		); err != nil {
			return fmt.Errorf("insert agent_toolsets: %w", err)
		}
	}
	return nil
}

func (r *AgentRepo) GetToolsetsWithExclusions(ctx context.Context, agentID string) (toolsets []string, exclusions map[string]string, err error) {
	rows, queryErr := dbutil.QueryRows[AgentToolsetRow](ctx, r.db,
		`SELECT agent_id, toolset, excluded_tools FROM agent_toolsets WHERE agent_id = ?`, agentID)
	if queryErr != nil {
		return nil, nil, fmt.Errorf("AgentRepo.GetToolsetsWithExclusions(%s): %w", agentID, queryErr)
	}
	toolsets = make([]string, 0, len(rows))
	for _, row := range rows {
		toolsets = append(toolsets, row.Toolset)
		if row.ExcludedTools != nil {
			if exclusions == nil {
				exclusions = make(map[string]string)
			}
			exclusions[row.Toolset] = *row.ExcludedTools
		}
	}
	return toolsets, exclusions, nil
}

const (
	sqlDeleteAgentInputModes  = `DELETE FROM agent_input_modes WHERE agent_id = ?`
	sqlInsertAgentInputModes  = `INSERT INTO agent_input_modes (agent_id, mode) VALUES (?, ?)`
	sqlSelectAgentInputModes  = `SELECT mode FROM agent_input_modes WHERE agent_id = ? ORDER BY mode`
	sqlDeleteAgentOutputModes = `DELETE FROM agent_output_modes WHERE agent_id = ?`
	sqlInsertAgentOutputModes = `INSERT INTO agent_output_modes (agent_id, mode) VALUES (?, ?)`
	sqlSelectAgentOutputModes = `SELECT mode FROM agent_output_modes WHERE agent_id = ? ORDER BY mode`
	sqlDeleteAgentConnectors  = `DELETE FROM agent_connectors WHERE agent_id = ?`
	sqlInsertAgentConnectors  = `INSERT INTO agent_connectors (agent_id, connector_id) VALUES (?, ?)`
	sqlSelectAgentConnectors  = `SELECT connector_id FROM agent_connectors WHERE agent_id = ? ORDER BY connector_id`
	sqlSelectAgentToolsets    = `SELECT toolset FROM agent_toolsets WHERE agent_id = ? ORDER BY toolset`
)

func (r *AgentRepo) setInputModes(ctx context.Context, q dbutil.Querier, agentID string, modes []string) error {
	return setJunctionLiteral(ctx, q, sqlDeleteAgentInputModes, sqlInsertAgentInputModes, "agent_input_modes", agentID, modes)
}

func (r *AgentRepo) setOutputModes(ctx context.Context, q dbutil.Querier, agentID string, modes []string) error {
	return setJunctionLiteral(ctx, q, sqlDeleteAgentOutputModes, sqlInsertAgentOutputModes, "agent_output_modes", agentID, modes)
}

func (r *AgentRepo) setConnectors(ctx context.Context, q dbutil.Querier, agentID string, connectorIDs []string) error {
	return setJunctionLiteral(ctx, q, sqlDeleteAgentConnectors, sqlInsertAgentConnectors, "agent_connectors", agentID, connectorIDs)
}

func setJunctionLiteral(ctx context.Context, q dbutil.Querier, deleteSQL, insertSQL, label, agentID string, values []string) error {
	if _, err := q.ExecContext(ctx, deleteSQL, agentID); err != nil {
		return fmt.Errorf("clear %s: %w", label, err)
	}
	for _, v := range values {
		if _, err := q.ExecContext(ctx, insertSQL, agentID, v); err != nil {
			return fmt.Errorf("insert %s: %w", label, err)
		}
	}
	return nil
}

func (r *AgentRepo) getToolsets(ctx context.Context, agentID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectAgentToolsets, agentID)
}

func (r *AgentRepo) getInputModes(ctx context.Context, agentID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectAgentInputModes, agentID)
}

func (r *AgentRepo) getOutputModes(ctx context.Context, agentID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectAgentOutputModes, agentID)
}

func (r *AgentRepo) getConnectors(ctx context.Context, agentID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectAgentConnectors, agentID)
}

func (r *AgentRepo) ListByConnectorID(ctx context.Context, connectorID string, pageNumber, pageSize int) ([]AgentWithRelations, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM agent_connectors ac
		 JOIN agents a ON a.id = ac.agent_id
		 WHERE ac.connector_id = ? AND a.type NOT IN ('a2a_proxy', 'link') AND a.is_ephemeral = 0`, connectorID)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) count: %w", connectorID, err)
	}

	offset := (pageNumber - 1) * pageSize
	query := `SELECT a.id, a.name, a.description, a.type, a.system_prompt, a.skills, a.skill_refs, a.model_provider, a.additional_configurations,
			a.created_by, a.updated_by, a.created_time, a.updated_time,
			a.is_ephemeral, a.expires_at, a.source_session_id, a.source_component_name, a.source_component_kind
		 FROM agents a
		 JOIN agent_connectors ac ON a.id = ac.agent_id
		 WHERE ac.connector_id = ? AND a.type NOT IN ('a2a_proxy', 'link') AND a.is_ephemeral = 0
		 ORDER BY a.created_time DESC, a.id DESC LIMIT ? OFFSET ?`

	agents, err := dbutil.QueryRowPtrs[Agent](ctx, r.db, query, connectorID, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) query: %w", connectorID, err)
	}

	var result []AgentWithRelations
	for _, a := range agents {
		toolsets, excl, tErr := r.GetToolsetsWithExclusions(ctx, a.ID)
		if tErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) toolsets: %w", a.ID, tErr)
		}
		inputModes, imErr := r.getInputModes(ctx, a.ID)
		if imErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) input modes: %w", a.ID, imErr)
		}
		outputModes, omErr := r.getOutputModes(ctx, a.ID)
		if omErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) output modes: %w", a.ID, omErr)
		}
		connectorIDs, cErr := r.getConnectors(ctx, a.ID)
		if cErr != nil {
			return nil, 0, fmt.Errorf("AgentRepo.ListByConnectorID(%s) connectors: %w", a.ID, cErr)
		}
		result = append(result, AgentWithRelations{
			Agent:         a,
			Toolsets:      toolsets,
			ExcludedTools: excl,
			InputModes:    inputModes,
			OutputModes:   outputModes,
			ConnectorIDs:  connectorIDs,
		})
	}

	return result, total, nil
}

func (r *AgentRepo) GetEphemeralForSession(ctx context.Context, sessionID, ownerUserID string) (*Agent, error) {
	a, err := dbutil.QueryRow[Agent](ctx, r.db,
		`SELECT id, name, description, type, system_prompt, skills, skill_refs, model_provider,
			created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind
		 FROM agents
		 WHERE is_ephemeral = 1 AND source_session_id = ? AND created_by = ?
		 ORDER BY created_time DESC LIMIT 1`, sessionID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("AgentRepo.GetEphemeralForSession(%s): %w", sessionID, err)
	}
	return a, nil
}

func (r *AgentRepo) ListExpiredEphemeralIDs(ctx context.Context, now int64) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM agents WHERE is_ephemeral = 1 AND expires_at IS NOT NULL AND expires_at < ?`, now)
	if err != nil {
		return nil, fmt.Errorf("AgentRepo.ListExpiredEphemeralIDs: %w", err)
	}
	return ids, nil
}

func (r *AgentRepo) GetDeploymentStatus(ctx context.Context, agentID string) (string, error) {
	status, action, found, err := r.latestDeployment(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("AgentRepo.GetDeploymentStatus(%s): %w", agentID, err)
	}
	if !found {
		return "not_deployed", nil
	}
	return MapDeploymentDisplayStatus(status, action), nil
}

func (r *AgentRepo) latestDeployment(ctx context.Context, agentID string) (status, action string, found bool, err error) {
	type row struct {
		Status string `db:"status"`
		Action string `db:"action"`
	}
	res, err := dbutil.QueryRow[row](ctx, r.db,
		`SELECT status, action FROM agent_deployments
		 WHERE agent_id = ? ORDER BY created_time DESC, id DESC LIMIT 1`, agentID)
	if err != nil {
		return "", "", false, err
	}
	if res == nil {
		return "", "", false, nil
	}
	return res.Status, res.Action, true, nil
}

func MapDeploymentDisplayStatus(status, action string) string {
	if (action == "deploy" || action == "update") && status == "success" {
		return "deployed"
	}
	if (action == "deploy" || action == "update") && status == "failed" {
		return "deploy_failed"
	}
	return "not_deployed"
}

func MayHaveLiveInstance(status, action string) bool {
	return action != "undeploy" || status != "success"
}

func (r *AgentRepo) HasPossiblyLiveDeployment(ctx context.Context, agentID string) (bool, error) {
	status, action, found, err := r.latestDeployment(ctx, agentID)
	if err != nil {
		return false, fmt.Errorf("AgentRepo.HasPossiblyLiveDeployment(%s): %w", agentID, err)
	}
	if !found {
		return false, nil
	}
	return MayHaveLiveInstance(status, action), nil
}

type DeploymentOutcome struct {
	Action string
	Status string
}

func (r *AgentRepo) GetDeploymentStatuses(ctx context.Context, agentIDs []string) (map[string]string, error) {
	latest, err := r.GetLatestDeployments(ctx, agentIDs)
	statuses := make(map[string]string, len(latest))
	for agentID, outcome := range latest {
		statuses[agentID] = MapDeploymentDisplayStatus(outcome.Status, outcome.Action)
	}
	return statuses, err
}

func (r *AgentRepo) GetLatestDeployments(ctx context.Context, agentIDs []string) (map[string]DeploymentOutcome, error) {
	if len(agentIDs) == 0 {
		return map[string]DeploymentOutcome{}, nil
	}

	ph, args, err := dbutil.InClause(agentIDs)
	if err != nil {
		return nil, fmt.Errorf("AgentRepo.GetLatestDeployments: %w", err)
	}
	query := `SELECT d.id, d.agent_id, d.status, d.action
		 FROM agent_deployments d
		 INNER JOIN (
		   SELECT agent_id, MAX(created_time) as max_time
		   FROM agent_deployments
		   WHERE agent_id IN (` + ph + `)
		   GROUP BY agent_id
		 ) latest ON d.agent_id = latest.agent_id AND d.created_time = latest.max_time`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("AgentRepo.GetLatestDeployments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]DeploymentOutcome)
	maxID := make(map[string]string)
	for rows.Next() {
		var id, agentID, status, action string
		if scanErr := rows.Scan(&id, &agentID, &status, &action); scanErr != nil {
			return nil, fmt.Errorf("AgentRepo.GetLatestDeployments scan: %w", scanErr)
		}
		if prev, ok := maxID[agentID]; ok && prev >= id {
			continue
		}
		maxID[agentID] = id
		result[agentID] = DeploymentOutcome{Action: action, Status: status}
	}
	return result, rows.Err()
}

func (r *AgentRepo) IDByName(ctx context.Context, name string) (string, error) {
	query, arg := `SELECT id FROM agents WHERE LOWER(REPLACE(name, ' ', '')) = ? ORDER BY id LIMIT 1`, normalizeName(name)
	if id, kind, ok := a2a.IDFromInstanceName(name); ok && kind == a2a.KindAgent {
		query, arg = `SELECT id FROM agents WHERE id = ?`, id
	}
	id, err := dbutil.QueryVal[string](ctx, r.db, query, arg)
	if err != nil {
		return "", fmt.Errorf("AgentRepo.IDByName(%s): %w", name, err)
	}
	return id, nil
}
