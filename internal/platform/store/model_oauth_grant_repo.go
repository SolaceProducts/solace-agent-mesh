// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	ModelOAuthGrantFlowAuthorizationCode = "authorization_code"
	ModelOAuthGrantFlowDeviceCode        = "device_code"
)

const (
	ModelOAuthGrantStatusActive                  = "active"
	ModelOAuthGrantStatusReauthorizationRequired = "reauthorization_required"
)

const ModelOAuthGrantRefresher = "system"

type ModelOAuthGrant struct {
	ID                     string `db:"id"`
	Name                   string `db:"name"`
	Flow                   string `db:"flow"`
	Issuer                 string `db:"issuer"`
	TokenEndpoint          string `db:"token_endpoint"`
	ClientID               string `db:"client_id"`
	Audience               string `db:"audience"`
	Scopes                 string `db:"scopes"`
	APIBase                string `db:"api_base"`
	AccountSubject         string `db:"account_subject"`
	AccountLabel           string `db:"account_label"`
	SignInFingerprint      string `db:"sign_in_fingerprint"`
	RefreshToken           string `db:"refresh_token"`
	AccessToken            string `db:"access_token"`
	AccessTokenExpiresTime int64  `db:"access_token_expires_time"`
	Status                 string `db:"status"`
	LastError              string `db:"last_error"`
	Version                int64  `db:"version"`
	RefreshStartedTime     int64  `db:"refresh_started_time"`
	CreatedBy              string `db:"created_by"`
	UpdatedBy              string `db:"updated_by"`
	CreatedTime            int64  `db:"created_time"`
	UpdatedTime            int64  `db:"updated_time"`
}

type ModelOAuthGrantTokens struct {
	RefreshToken           string
	AccessToken            string
	AccessTokenExpiresTime int64
}

type ModelOAuthGrantRepo struct {
	db *DB
}

func NewModelOAuthGrantRepo(db *DB) *ModelOAuthGrantRepo {
	return &ModelOAuthGrantRepo{db: db}
}

const modelOAuthGrantSelectColumns = `id, name, flow, issuer, token_endpoint, client_id, audience, scopes, api_base,
	account_subject, account_label, sign_in_fingerprint, refresh_token, access_token, access_token_expires_time,
	status, last_error, version, refresh_started_time, created_by, updated_by, created_time, updated_time`

func (r *ModelOAuthGrantRepo) Get(ctx context.Context, id string) (*ModelOAuthGrant, error) {
	return dbutil.QueryRow[ModelOAuthGrant](ctx, r.db,
		`SELECT `+modelOAuthGrantSelectColumns+` FROM model_oauth_grants WHERE id = ?`, id,
	)
}

func (r *ModelOAuthGrantRepo) Create(ctx context.Context, s *ModelOAuthGrant, actor string) error {
	if s.ID == "" {
		s.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	s.Status = ModelOAuthGrantStatusActive
	s.LastError = ""
	s.Version = 1
	s.CreatedBy, s.UpdatedBy = actor, actor
	s.CreatedTime, s.UpdatedTime = now, now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO model_oauth_grants (`+modelOAuthGrantSelectColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.Flow, s.Issuer, s.TokenEndpoint, s.ClientID, s.Audience, s.Scopes, s.APIBase,
		s.AccountSubject, s.AccountLabel, s.SignInFingerprint, s.RefreshToken, s.AccessToken, s.AccessTokenExpiresTime,
		s.Status, s.LastError, s.Version, 0, s.CreatedBy, s.UpdatedBy, s.CreatedTime, s.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("insert OAuth grant: %w", err)
	}
	return nil
}

func (r *ModelOAuthGrantRepo) ClaimRefresh(ctx context.Context, id string, readVersion, startedTime int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET refresh_started_time = ?, version = version + 1, updated_by = ?, updated_time = ?
		 WHERE id = ? AND version = ?`,
		startedTime, ModelOAuthGrantRefresher, time.Now().UnixMilli(),
		id, readVersion,
	)
	if err != nil {
		return false, fmt.Errorf("claim OAuth grant refresh: %w", err)
	}
	return rowsChanged(res)
}

func (r *ModelOAuthGrantRepo) ReleaseRefresh(ctx context.Context, id string, claimedVersion int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET refresh_started_time = 0, version = version + 1, updated_by = ?, updated_time = ?
		 WHERE id = ? AND version = ?`,
		ModelOAuthGrantRefresher, time.Now().UnixMilli(),
		id, claimedVersion,
	)
	if err != nil {
		return fmt.Errorf("release OAuth grant refresh: %w", err)
	}
	return nil
}

func (r *ModelOAuthGrantRepo) SaveRefreshed(ctx context.Context, id string, claimedVersion int64, tokens ModelOAuthGrantTokens) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET refresh_token = ?, access_token = ?, access_token_expires_time = ?,
		     status = ?, last_error = '', refresh_started_time = 0, version = version + 1,
		     updated_by = ?, updated_time = ?
		 WHERE id = ? AND version = ?`,
		tokens.RefreshToken, tokens.AccessToken, tokens.AccessTokenExpiresTime,
		ModelOAuthGrantStatusActive, ModelOAuthGrantRefresher, time.Now().UnixMilli(),
		id, claimedVersion,
	)
	if err != nil {
		return false, fmt.Errorf("save refreshed OAuth grant: %w", err)
	}
	return rowsChanged(res)
}

func (r *ModelOAuthGrantRepo) MarkReauthorizationRequired(ctx context.Context, id string, claimedVersion int64, lastError string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET access_token = '', access_token_expires_time = 0, status = ?, last_error = ?,
		     refresh_started_time = 0, version = version + 1, updated_by = ?, updated_time = ?
		 WHERE id = ? AND version = ?`,
		ModelOAuthGrantStatusReauthorizationRequired, lastError, ModelOAuthGrantRefresher, time.Now().UnixMilli(),
		id, claimedVersion,
	)
	if err != nil {
		return false, fmt.Errorf("mark OAuth grant reauthorization required: %w", err)
	}
	return rowsChanged(res)
}

func (r *ModelOAuthGrantRepo) Replace(ctx context.Context, id string, tokens ModelOAuthGrantTokens, signInFingerprint, accountLabel, actor string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET refresh_token = ?, access_token = ?, access_token_expires_time = ?, sign_in_fingerprint = ?, account_label = ?,
		     status = ?, last_error = '', refresh_started_time = 0, version = version + 1,
		     updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		tokens.RefreshToken, tokens.AccessToken, tokens.AccessTokenExpiresTime, signInFingerprint, accountLabel,
		ModelOAuthGrantStatusActive, actor, time.Now().UnixMilli(),
		id,
	)
	if err != nil {
		return false, fmt.Errorf("replace OAuth grant: %w", err)
	}
	return rowsChanged(res)
}

func (r *ModelOAuthGrantRepo) Overwrite(ctx context.Context, s *ModelOAuthGrant, actor string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE model_oauth_grants
		 SET name = ?, flow = ?, issuer = ?, token_endpoint = ?, client_id = ?, audience = ?, scopes = ?, api_base = ?,
		     account_subject = ?, account_label = ?, sign_in_fingerprint = ?,
		     refresh_token = ?, access_token = ?, access_token_expires_time = ?,
		     status = ?, last_error = '', refresh_started_time = 0, version = version + 1,
		     updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		s.Name, s.Flow, s.Issuer, s.TokenEndpoint, s.ClientID, s.Audience, s.Scopes, s.APIBase,
		s.AccountSubject, s.AccountLabel, s.SignInFingerprint,
		s.RefreshToken, s.AccessToken, s.AccessTokenExpiresTime,
		ModelOAuthGrantStatusActive, actor, time.Now().UnixMilli(),
		s.ID,
	)
	if err != nil {
		return false, fmt.Errorf("overwrite OAuth grant: %w", err)
	}
	return rowsChanged(res)
}

func (r *ModelOAuthGrantRepo) Delete(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM model_oauth_grants WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete OAuth grant: %w", err)
	}
	return nil
}

func rowsChanged(res sql.Result) (bool, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}
