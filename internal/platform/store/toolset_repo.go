// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

type ToolPackageRepo struct {
	db *DB
}

func NewToolPackageRepo(db *DB) *ToolPackageRepo {
	return &ToolPackageRepo{db: db}
}

func (r *ToolPackageRepo) DB() *DB { return r.db }

func (r *ToolPackageRepo) Create(ctx context.Context, p *ToolPackage) error {
	if p.ID == "" {
		p.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if p.CreatedTime == 0 {
		p.CreatedTime = now
	}
	if p.UpdatedTime == 0 {
		p.UpdatedTime = now
	}

	if p.Capabilities == "" {
		p.Capabilities = "{}"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tool_packages (id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, capabilities,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.Runtime, p.DiscoveryStatus,
		p.DiscoveryErrors, p.ZipResourceKey, p.ZipFilename, p.ZipFileSize, p.Capabilities,
		p.CreatedBy, p.UpdatedBy, p.CreatedTime, p.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("insert tool_package: %w", err)
	}
	return nil
}

func (r *ToolPackageRepo) GetByID(ctx context.Context, id string) (*ToolPackage, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	return dbutil.QueryRow[ToolPackage](ctx, r.db,
		`SELECT id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, content_hash, capabilities,
			created_by, updated_by, created_time, updated_time
		 FROM tool_packages WHERE id = ?`, id,
	)
}

func (r *ToolPackageRepo) GetByName(ctx context.Context, name string) (*ToolPackage, error) {
	return dbutil.QueryRow[ToolPackage](ctx, r.db,
		`SELECT id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, content_hash, capabilities,
			created_by, updated_by, created_time, updated_time
		 FROM tool_packages WHERE name = ?`, name,
	)
}

func (r *ToolPackageRepo) GetPackageNamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("GetPackageNamesByIDs: %w", err)
	}
	type row struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT id, name FROM tool_packages WHERE id IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetPackageNamesByIDs: %w", err)
	}
	for _, r := range rows {
		result[r.ID] = r.Name
	}
	return result, nil
}

func (r *ToolPackageRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int, typeFilter string, discoveryStatus *string) ([]ToolPackage, int, error) {
	return r.ListWithOffset(ctx, (pageNumber-1)*pageSize, pageSize, discoveryStatus)
}

func (r *ToolPackageRepo) ListWithOffset(ctx context.Context, offset, limit int, discoveryStatus *string) ([]ToolPackage, int, error) {
	var (
		countArgs []any
		queryArgs []any
		where     string
	)
	if discoveryStatus != nil {
		where = " WHERE discovery_status = ?"
		countArgs = []any{*discoveryStatus}
		queryArgs = []any{*discoveryStatus, limit, offset}
	} else {
		queryArgs = []any{limit, offset}
	}

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tool_packages`+where, countArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("ToolPackageRepo.ListPaginated count: %w", err)
	}

	packages, err := dbutil.QueryRows[ToolPackage](ctx, r.db,
		`SELECT id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, content_hash, capabilities,
			created_by, updated_by, created_time, updated_time
		 FROM tool_packages`+where+` ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("ToolPackageRepo.ListPaginated query: %w", err)
	}

	return packages, total, nil
}

func (r *ToolPackageRepo) Delete(ctx context.Context, id string) error {
	return r.deleteOn(ctx, r.db, id)
}

func (r *ToolPackageRepo) DeleteInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteOn(ctx, tx, id)
}

func (r *ToolPackageRepo) RemoveFromAllAgentsInTx(ctx context.Context, tx *dbutil.Tx, packageName string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_toolsets WHERE toolset = ?`, packageName); err != nil {
		return fmt.Errorf("ToolPackageRepo.RemoveFromAllAgents(%s): %w", packageName, err)
	}
	return nil
}

func (r *ToolPackageRepo) deleteOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx, `DELETE FROM tool_packages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("ToolPackageRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool package not found")
	}
	return nil
}

func (r *ToolPackageRepo) UpdatePackage(ctx context.Context, id, name string, description *string, updatedBy string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE tool_packages SET name = ?, description = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
		name, description, updatedBy, now, id,
	)
	if err != nil {
		return fmt.Errorf("update tool package: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool package not found")
	}
	return nil
}

func (r *ToolPackageRepo) UpdateZipInfo(ctx context.Context, id, runtime, zipS3Key, zipFilename string, zipFileSize int64, updatedBy string) error {
	return r.updateZipInfoOn(ctx, r.db, id, runtime, zipS3Key, zipFilename, zipFileSize, updatedBy)
}

func (r *ToolPackageRepo) UpdateZipInfoInTx(ctx context.Context, tx *dbutil.Tx, id, runtime, zipS3Key, zipFilename string, zipFileSize int64, updatedBy string) error {
	return r.updateZipInfoOn(ctx, tx, id, runtime, zipS3Key, zipFilename, zipFileSize, updatedBy)
}

func (r *ToolPackageRepo) updateZipInfoOn(ctx context.Context, q dbutil.Querier, id, runtime, zipS3Key, zipFilename string, zipFileSize int64, updatedBy string) error {
	now := time.Now().UnixMilli()

	if _, err := q.ExecContext(ctx,
		`DELETE FROM tool_package_files WHERE package_id = ?`, id,
	); err != nil {
		return fmt.Errorf("clear tool_package_files: %w", err)
	}

	result, err := q.ExecContext(ctx,
		`UPDATE tool_packages
		 SET runtime = ?, zip_resource_key = ?, zip_filename = ?, zip_file_size = ?,
		     discovery_status = 'pending', discovery_errors = NULL,
		     updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		runtime, zipS3Key, zipFilename, zipFileSize, updatedBy, now, id,
	)
	if err != nil {
		return fmt.Errorf("update zip info: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool package not found")
	}
	return nil
}

func (r *ToolPackageRepo) SetContentHashInTx(ctx context.Context, tx *dbutil.Tx, id, hash string) error {
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx,
		`UPDATE tool_packages SET content_hash = ?, updated_time = ? WHERE id = ?`,
		hash, now, id,
	)
	if err != nil {
		return fmt.Errorf("ToolPackageRepo.SetContentHash(%s): %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("tool package not found")
	}
	return nil
}

func (r *ToolPackageRepo) UpdateDiscoveryStatus(ctx context.Context, id, status string, errors *string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_status = ?, discovery_errors = ?, updated_time = ? WHERE id = ?`,
		status, errors, now, id,
	)
	if err != nil {
		return fmt.Errorf("update discovery status: %w", err)
	}
	return nil
}

func (r *ToolPackageRepo) MarkReadyIfToolsRegistered(ctx context.Context, packageID string, toolNames []string) error {
	wanted := uniqueToolNames(toolNames)
	if len(wanted) == 0 {
		return nil
	}
	placeholders, nameArgs, inErr := dbutil.InClause(wanted)
	if inErr != nil {
		return fmt.Errorf("MarkReadyIfToolsRegistered: %w", inErr)
	}
	txErr := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		lock := `SELECT id FROM tool_packages WHERE id = ?`
		if tx.Dialect() == "postgres" {
			lock += ` FOR NO KEY UPDATE`
		}
		var locked string
		if err := tx.QueryRowContext(ctx, lock, packageID).Scan(&locked); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("lock tool package: %w", err)
		}
		var registered int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT tool_name) FROM tool_package_files
			 WHERE package_id = ? AND tool_name IN (`+placeholders+`)`,
			append([]any{packageID}, nameArgs...)...,
		).Scan(&registered); err != nil {
			return fmt.Errorf("count registered tools: %w", err)
		}
		if registered < len(wanted) {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE tool_packages SET discovery_status = 'ready', discovery_errors = NULL, updated_time = ? WHERE id = ?`,
			time.Now().UnixMilli(), packageID,
		); err != nil {
			return fmt.Errorf("mark ready: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return fmt.Errorf("MarkReadyIfToolsRegistered(%s): %w", packageID, txErr)
	}
	return nil
}

func uniqueToolNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		unique = append(unique, n)
	}
	return unique
}

func (r *ToolPackageRepo) FailStalePending(ctx context.Context, id string, cutoffMs int64, errors *string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_status = 'failed', discovery_errors = ?, updated_time = ?
		 WHERE id = ? AND discovery_status = 'pending' AND updated_time < ?`,
		errors, time.Now().UnixMilli(), id, cutoffMs,
	)
	if err != nil {
		return false, fmt.Errorf("fail stale pending package: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *ToolPackageRepo) UpdateCapabilities(ctx context.Context, id, capabilitiesJSON string) error {
	if capabilitiesJSON == "" {
		capabilitiesJSON = "{}"
	}
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE tool_packages SET capabilities = ?, updated_time = ? WHERE id = ?`,
		capabilitiesJSON, now, id,
	)
	if err != nil {
		return fmt.Errorf("update capabilities: %w", err)
	}
	return nil
}

func (r *ToolPackageRepo) AppendDiscoveryError(ctx context.Context, packageID, deploymentErrMsg string) error {
	pkg, err := r.GetByID(ctx, packageID)
	if err != nil {
		return fmt.Errorf("read package for error append: %w", err)
	}
	if pkg == nil {
		return fmt.Errorf("package not found: %s", packageID)
	}

	var errs []string
	if pkg.DiscoveryErrors != nil && *pkg.DiscoveryErrors != "" {
		_ = json.Unmarshal([]byte(*pkg.DiscoveryErrors), &errs)
	}
	errs = append(errs, deploymentErrMsg)
	b, err := json.Marshal(errs)
	if err != nil {
		return fmt.Errorf("marshal discovery errors: %w", err)
	}
	errJSON := string(b)

	now := time.Now().UnixMilli()
	_, err = r.db.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_errors = ?, updated_time = ? WHERE id = ?`,
		errJSON, now, packageID,
	)
	if err != nil {
		return fmt.Errorf("append discovery error: %w", err)
	}
	return nil
}

func (r *ToolPackageRepo) GetFilesForPackage(ctx context.Context, packageID string) ([]ToolPackageFile, error) {
	return dbutil.QueryRows[ToolPackageFile](ctx, r.db,
		`SELECT id, package_id, tool_name, tool_description, timeout_seconds,
			sandbox_profile, parameters, config_schema, auth, created_time
		 FROM tool_package_files WHERE package_id = ?`, packageID,
	)
}

func (r *ToolPackageRepo) ResolveWireToolName(ctx context.Context, packageID, bareName string) (string, error) {
	pkg, err := r.GetByID(ctx, packageID)
	if err != nil {
		return "", fmt.Errorf("resolve wire tool name: %w", err)
	}
	if pkg == nil {
		return "", nil
	}
	f, err := dbutil.QueryRow[ToolPackageFile](ctx, r.db,
		`SELECT id, package_id, tool_name, tool_description, timeout_seconds,
			sandbox_profile, parameters, config_schema, auth, created_time
		 FROM tool_package_files
		 WHERE package_id = ? AND tool_name = ?`,
		packageID, bareName,
	)
	if err != nil {
		return "", fmt.Errorf("resolve wire tool name: %w", err)
	}
	if f == nil {
		return "", nil
	}
	return pkg.Name + PackageToolSeparator + f.ToolName, nil
}

func (r *ToolPackageRepo) GetFilesForPackages(ctx context.Context, packageIDs []string) (map[string][]ToolPackageFile, error) {
	if len(packageIDs) == 0 {
		return map[string][]ToolPackageFile{}, nil
	}

	placeholders, args, err := dbutil.InClause(packageIDs)
	if err != nil {
		return nil, fmt.Errorf("get files for packages: %w", err)
	}
	files, err := dbutil.QueryRows[ToolPackageFile](ctx, r.db,
		`SELECT id, package_id, tool_name, tool_description, timeout_seconds,
			sandbox_profile, parameters, config_schema, auth, created_time
		 FROM tool_package_files WHERE package_id IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("get files for packages: %w", err)
	}

	result := make(map[string][]ToolPackageFile, len(packageIDs))
	for i := range files {
		result[files[i].PackageID] = append(result[files[i].PackageID], files[i])
	}
	return result, nil
}

func (r *ToolPackageRepo) UpsertToolFile(ctx context.Context, f *ToolPackageFile) error {
	if f.ID == "" {
		f.ID = idgen.New()
	}
	if f.CreatedTime == 0 {
		f.CreatedTime = time.Now().UnixMilli()
	}

	insertSQL := `INSERT INTO tool_package_files (id, package_id, tool_name, tool_description,
		timeout_seconds, sandbox_profile, parameters, config_schema, auth, created_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "package_id, tool_name",
		[]string{"tool_description", "timeout_seconds", "sandbox_profile", "parameters", "config_schema", "auth"})

	if _, err := r.db.ExecContext(ctx, upsertSQL,
		f.ID, f.PackageID, f.ToolName, f.ToolDescription,
		f.TimeoutSeconds, f.SandboxProfile, f.Parameters, f.ConfigSchema, f.Auth, f.CreatedTime,
	); err != nil {
		return fmt.Errorf("upsert tool file: %w", err)
	}

	return nil
}

func (r *ToolPackageRepo) RemoveToolFile(ctx context.Context, packageID, toolName string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tool_package_files WHERE package_id = ? AND tool_name = ?`,
			packageID, toolName,
		); err != nil {
			return fmt.Errorf("remove tool file: %w", err)
		}

		remaining, err := dbutil.QueryVal[int](ctx, tx,
			`SELECT COUNT(*) FROM tool_package_files WHERE package_id = ?`, packageID)
		if err != nil {
			return fmt.Errorf("count remaining files: %w", err)
		}

		if remaining == 0 {
			now := time.Now().UnixMilli()
			if _, err := tx.ExecContext(ctx,
				`UPDATE tool_packages SET discovery_status = 'removed', updated_time = ? WHERE id = ?`,
				now, packageID,
			); err != nil {
				return fmt.Errorf("set package to removed: %w", err)
			}
		}

		return nil
	})
}

func (r *ToolPackageRepo) SetFailedDiscovery(ctx context.Context, packageID, deploymentErrMsg string) error {
	b, err := json.Marshal([]string{deploymentErrMsg})
	if err != nil {
		return fmt.Errorf("marshal discovery errors: %w", err)
	}
	errJSON := string(b)
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE tool_packages SET discovery_status = 'failed', discovery_errors = ?, updated_time = ? WHERE id = ? AND discovery_status IN ('pending','failed')`,
		errJSON, now, packageID,
	)
	if err != nil {
		return fmt.Errorf("set failed discovery for %s: %w", packageID, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		slog.Default().With("logger_name", "toolset-repo").Debug("SetFailedDiscovery no-op: package already ready", "packageID", packageID)
	}
	return nil
}

func (r *ToolPackageRepo) ListPendingOlderThan(ctx context.Context, cutoffMs int64) ([]ToolPackage, error) {
	return dbutil.QueryRows[ToolPackage](ctx, r.db,
		`SELECT id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, content_hash, capabilities,
			created_by, updated_by, created_time, updated_time
		 FROM tool_packages
		 WHERE discovery_status = 'pending' AND updated_time < ?`, cutoffMs,
	)
}

func (r *ToolPackageRepo) GetReadyPackagesByNames(ctx context.Context, names []string) ([]ToolPackage, error) {
	if len(names) == 0 {
		return nil, nil
	}
	placeholders, args, err := dbutil.InClause(names)
	if err != nil {
		return nil, fmt.Errorf("GetReadyPackagesByNames: %w", err)
	}
	return dbutil.QueryRows[ToolPackage](ctx, r.db,
		`SELECT id, name, description, runtime, discovery_status,
			discovery_errors, zip_resource_key, zip_filename, zip_file_size, content_hash, capabilities,
			created_by, updated_by, created_time, updated_time
		 FROM tool_packages
		 WHERE name IN (`+placeholders+`) AND discovery_status = 'ready'`, args...,
	)
}

func (r *ToolPackageRepo) GetToolsetConfigsForAgent(ctx context.Context, agentID string) ([]AgentToolsetConfig, error) {
	return dbutil.QueryRows[AgentToolsetConfig](ctx, r.db,
		`SELECT agent_id, package_id, config_values
		 FROM agent_toolset_configs WHERE agent_id = ?`, agentID,
	)
}

func (r *ToolPackageRepo) GetAgentToolsetConfigsForPackage(ctx context.Context, packageID string) ([]AgentToolsetConfig, error) {
	return dbutil.QueryRows[AgentToolsetConfig](ctx, r.db,
		`SELECT agent_id, package_id, config_values
		 FROM agent_toolset_configs WHERE package_id = ?`, packageID,
	)
}

func (r *ToolPackageRepo) GetToolsetConfigsForAgents(ctx context.Context, agentIDs []string) (map[string][]AgentToolsetConfig, error) {
	result := make(map[string][]AgentToolsetConfig, len(agentIDs))
	if len(agentIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(agentIDs)
	if err != nil {
		return nil, fmt.Errorf("GetToolsetConfigsForAgents: %w", err)
	}
	rows, err := dbutil.QueryRows[AgentToolsetConfig](ctx, r.db,
		`SELECT agent_id, package_id, config_values
		 FROM agent_toolset_configs WHERE agent_id IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetToolsetConfigsForAgents: %w", err)
	}
	for _, row := range rows {
		result[row.AgentID] = append(result[row.AgentID], row)
	}
	return result, nil
}

func (r *ToolPackageRepo) SetToolsetConfigsForAgent(ctx context.Context, agentID string, configs []AgentToolsetConfig) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM agent_toolset_configs WHERE agent_id = ?`, agentID,
		); err != nil {
			return fmt.Errorf("clear agent toolset configs: %w", err)
		}

		for _, cfg := range configs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO agent_toolset_configs (agent_id, package_id, config_values)
				 VALUES (?, ?, ?)`,
				agentID, cfg.PackageID, cfg.ConfigValues,
			); err != nil {
				return fmt.Errorf("insert agent toolset config: %w", err)
			}
		}
		return nil
	})
}

func (r *ToolPackageRepo) GetToolsetConfig(ctx context.Context, packageID string) (*ToolsetConfig, error) {
	return dbutil.QueryRow[ToolsetConfig](ctx, r.db,
		`SELECT package_id, config_values, updated_by, updated_time
		 FROM toolset_configs WHERE package_id = ?`, packageID,
	)
}

func (r *ToolPackageRepo) GetToolsetConfigsForPackages(ctx context.Context, packageIDs []string) (map[string]ToolsetConfig, error) {
	result := make(map[string]ToolsetConfig, len(packageIDs))
	if len(packageIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(packageIDs)
	if err != nil {
		return nil, fmt.Errorf("GetToolsetConfigsForPackages: %w", err)
	}
	rows, err := dbutil.QueryRows[ToolsetConfig](ctx, r.db,
		`SELECT package_id, config_values, updated_by, updated_time
		 FROM toolset_configs WHERE package_id IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetToolsetConfigsForPackages: %w", err)
	}
	for _, row := range rows {
		result[row.PackageID] = row
	}
	return result, nil
}

func (r *ToolPackageRepo) SetToolsetConfig(ctx context.Context, packageID string, configValues *string, updatedBy string) error {
	return r.setToolsetConfigOn(ctx, r.db, packageID, configValues, updatedBy)
}

func (r *ToolPackageRepo) SetToolsetConfigInTx(ctx context.Context, tx *dbutil.Tx, packageID string, configValues *string, updatedBy string) error {
	return r.setToolsetConfigOn(ctx, tx, packageID, configValues, updatedBy)
}

func (r *ToolPackageRepo) setToolsetConfigOn(ctx context.Context, q dbutil.Querier, packageID string, configValues *string, updatedBy string) error {
	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO toolset_configs (package_id, config_values, updated_by, updated_time)
		VALUES (?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "package_id",
		[]string{"config_values", "updated_by", "updated_time"})
	if _, err := q.ExecContext(ctx, upsertSQL,
		packageID, configValues, updatedBy, now,
	); err != nil {
		return fmt.Errorf("upsert toolset_config: %w", err)
	}
	return nil
}

func (r *ToolPackageRepo) GetBuiltinToolsetConfigsForAgent(ctx context.Context, agentID string) ([]AgentBuiltinToolsetConfig, error) {
	return dbutil.QueryRows[AgentBuiltinToolsetConfig](ctx, r.db,
		`SELECT agent_id, toolset_name, config_values, volume_bindings, network_bindings
		 FROM agent_builtin_toolset_configs WHERE agent_id = ?
		 ORDER BY toolset_name`, agentID,
	)
}

func (r *ToolPackageRepo) GetBuiltinToolsetConfigsForAgents(ctx context.Context, agentIDs []string) (map[string][]AgentBuiltinToolsetConfig, error) {
	result := make(map[string][]AgentBuiltinToolsetConfig, len(agentIDs))
	if len(agentIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(agentIDs)
	if err != nil {
		return nil, fmt.Errorf("GetBuiltinToolsetConfigsForAgents: %w", err)
	}
	rows, err := dbutil.QueryRows[AgentBuiltinToolsetConfig](ctx, r.db,
		`SELECT agent_id, toolset_name, config_values, volume_bindings, network_bindings
		 FROM agent_builtin_toolset_configs WHERE agent_id IN (`+placeholders+`)
		 ORDER BY agent_id, toolset_name`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("GetBuiltinToolsetConfigsForAgents: %w", err)
	}
	for _, row := range rows {
		result[row.AgentID] = append(result[row.AgentID], row)
	}
	return result, nil
}

func (r *ToolPackageRepo) SetBuiltinToolsetConfigsForAgent(ctx context.Context, agentID string, configs []AgentBuiltinToolsetConfig) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM agent_builtin_toolset_configs WHERE agent_id = ?`, agentID,
		); err != nil {
			return fmt.Errorf("clear agent builtin toolset configs: %w", err)
		}

		for _, cfg := range configs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO agent_builtin_toolset_configs (agent_id, toolset_name, config_values, volume_bindings, network_bindings)
				 VALUES (?, ?, ?, ?, ?)`,
				agentID, cfg.ToolsetName, cfg.ConfigValues, cfg.VolumeBindings, cfg.NetworkBindings,
			); err != nil {
				return fmt.Errorf("insert agent builtin toolset config: %w", err)
			}
		}
		return nil
	})
}

func (r *ToolPackageRepo) GetAgentsUsingPackage(ctx context.Context, packageName string) ([]AgentToolsetRef, error) {
	return dbutil.QueryRows[AgentToolsetRef](ctx, r.db,
		`SELECT a.id, a.name, COALESCE(a.description, '') as description
		 FROM agents a
		 INNER JOIN agent_toolsets at2 ON a.id = at2.agent_id
		 WHERE at2.toolset = ?`, packageName,
	)
}

func (r *ToolPackageRepo) GetWorkflowIDsUsingPackage(ctx context.Context, packageName string) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT workflow_id FROM workflow_toolsets WHERE toolset = ?`, packageName)
	if err != nil {
		return nil, fmt.Errorf("ToolPackageRepo.GetWorkflowIDsUsingPackage(%s): %w", packageName, err)
	}
	return ids, nil
}

func (r *ToolPackageRepo) GetAgentsByPackageNames(ctx context.Context, packageNames []string) (map[string][]AgentToolsetRef, error) {
	result := make(map[string][]AgentToolsetRef, len(packageNames))
	for _, n := range packageNames {
		result[n] = nil
	}
	if len(packageNames) == 0 {
		return result, nil
	}

	placeholders, args, err := dbutil.InClause(packageNames)
	if err != nil {
		return nil, fmt.Errorf("batch fetch agents by toolset names: %w", err)
	}
	rows, err := dbutil.QueryRows[agentToolsetRefWithPackage](ctx, r.db,
		`SELECT a.id, a.name, COALESCE(a.description, '') as description, at2.toolset as package_name
		 FROM agents a
		 INNER JOIN agent_toolsets at2 ON a.id = at2.agent_id
		 WHERE at2.toolset IN (`+placeholders+`)`, args...,
	)
	if err != nil {
		return nil, fmt.Errorf("batch fetch agents by toolset names: %w", err)
	}
	for _, row := range rows {
		result[row.PackageName] = append(result[row.PackageName], AgentToolsetRef{
			ID:          row.ID,
			Name:        row.Name,
			Description: row.Description,
		})
	}
	return result, nil
}

type AgentToolsetRef struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
}

type AgentAutoInjectRef struct {
	ID                       string `db:"id"`
	Name                     string `db:"name"`
	Description              string `db:"description"`
	AdditionalConfigurations []byte `db:"additional_configurations"`
}

func (r *ToolPackageRepo) ListAgentAutoInjectRefs(ctx context.Context) ([]AgentAutoInjectRef, error) {
	rows, err := dbutil.QueryRows[AgentAutoInjectRef](ctx, r.db,
		`SELECT id, name, COALESCE(description, '') AS description, additional_configurations
		 FROM agents WHERE is_ephemeral = 0`)
	if err != nil {
		return nil, fmt.Errorf("list agent auto-inject refs: %w", err)
	}
	return rows, nil
}

type agentToolsetRefWithPackage struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	PackageName string `db:"package_name"`
}
