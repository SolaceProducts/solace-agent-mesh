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
)

type PlatformWebuiSettingsRepo struct {
	db *DB
}

func NewPlatformWebuiSettingsRepo(db *DB) *PlatformWebuiSettingsRepo {
	return &PlatformWebuiSettingsRepo{db: db}
}

func (r *PlatformWebuiSettingsRepo) Get(ctx context.Context) (*PlatformWebuiSettings, error) {
	return dbutil.QueryRow[PlatformWebuiSettings](ctx, r.db,
		`SELECT id, app_name, welcome_message, collect_feedback, publish_feedback,
		        feedback_task_info_level,
		        disclaimer_text, logo_url, small_logo_url, system_purpose, response_format,
		        created_by, updated_by, created_time, updated_time
		 FROM platform_webui_settings WHERE id = ?`,
		PlatformWebuiSettingsSingletonID,
	)
}

func (r *PlatformWebuiSettingsRepo) WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

func (r *PlatformWebuiSettingsRepo) Set(ctx context.Context, s *PlatformWebuiSettings, actor string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.SetInTx(ctx, tx, s, actor)
	})
}

func (r *PlatformWebuiSettingsRepo) SetInTx(ctx context.Context, tx *dbutil.Tx, s *PlatformWebuiSettings, actor string) error {
	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO platform_webui_settings
		(id, app_name, welcome_message, collect_feedback, publish_feedback,
		 feedback_task_info_level,
		 disclaimer_text, logo_url, small_logo_url, system_purpose, response_format,
		 created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(tx.Dialect(), insertSQL, "id",
		[]string{"app_name", "welcome_message", "collect_feedback", "publish_feedback",
			"feedback_task_info_level",
			"disclaimer_text", "logo_url", "small_logo_url", "system_purpose", "response_format",
			"updated_by", "updated_time"})
	if _, err := tx.ExecContext(ctx, upsertSQL,
		PlatformWebuiSettingsSingletonID,
		s.AppName, s.WelcomeMessage, nullableBoolArg(s.CollectFeedback),
		nullableBoolArg(s.PublishFeedback),
		s.FeedbackTaskInfoLevel,
		s.DisclaimerText,
		s.LogoURL, s.SmallLogoURL, s.SystemPurpose, s.ResponseFormat,
		actor, actor, now, now,
	); err != nil {
		return fmt.Errorf("upsert platform_webui_settings: %w", err)
	}
	return nil
}

func nullableBoolArg(b *bool) any {
	if b == nil {
		return nil
	}
	return dbutil.BoolToInt(*b)
}

func (r *PlatformWebuiSettingsRepo) Delete(ctx context.Context) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.DeleteInTx(ctx, tx)
	})
}

func (r *PlatformWebuiSettingsRepo) DeleteInTx(ctx context.Context, tx *dbutil.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM platform_webui_settings WHERE id = ?`,
		PlatformWebuiSettingsSingletonID,
	); err != nil {
		return fmt.Errorf("delete platform_webui_settings: %w", err)
	}
	return nil
}
