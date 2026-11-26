// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func backfillAgentExtraConfigToCamelCase(ctx context.Context, db *store.DB, logger *slog.Logger) error {
	if db == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id, additional_configurations FROM agents`)
	if err != nil {
		return fmt.Errorf("backfill additional_configurations: select rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type rowToUpdate struct {
		ID  string
		Raw []byte
	}
	var updates []rowToUpdate

	for rows.Next() {
		var id string
		var raw []byte
		if scanErr := rows.Scan(&id, &raw); scanErr != nil {
			logger.Warn("backfill additional_configurations: scan row failed; skipping", "error", scanErr)
			continue
		}
		if len(raw) == 0 || isEmptyJSONObject(raw) {
			continue
		}
		var parsed map[string]any
		if jsonErr := json.Unmarshal(raw, &parsed); jsonErr != nil {
			logger.Warn("backfill additional_configurations: row is not a JSON object; skipping",
				"agentID", id)
			continue
		}
		translated := reverseTranslateAgentExtraConfig(parsed, agentExtraConfigSchema)
		newBytes, _ := json.Marshal(translated)
		origCanonical, _ := json.Marshal(parsed)
		if bytes.Equal(origCanonical, newBytes) {
			continue
		}
		updates = append(updates, rowToUpdate{ID: id, Raw: newBytes})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backfill additional_configurations: row iteration: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("backfill additional_configurations: close rows: %w", err)
	}

	if len(updates) == 0 {
		logger.Info("agent additional_configurations backfill: no rows needed translation")
		return nil
	}

	const updateSQL = `UPDATE agents SET additional_configurations = ? WHERE id = ?`
	rewritten := 0
	for _, u := range updates {
		if _, execErr := db.ExecContext(ctx, updateSQL, u.Raw, u.ID); execErr != nil {
			logger.Warn("backfill additional_configurations: write failed; will retry on next startup",
				"agentID", u.ID,
				"error", execErr)
			continue
		}
		rewritten++
	}
	logger.Info("agent additional_configurations backfill: rewrote rows from snake_case to camelCase",
		"rewritten", rewritten,
		"attempted", len(updates))
	return nil
}
