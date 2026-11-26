// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

const (
	ModelTypeChat     = "chat"
	ModelTypeDecision = "decision"
)

type ModelConfiguration struct {
	ID              string  `db:"id"`
	Alias           string  `db:"alias"`
	Provider        string  `db:"provider"`
	ModelName       string  `db:"model_name"`
	APIBase         *string `db:"api_base"`
	ModelAuthType   string  `db:"model_auth_type"`
	ModelAuthConfig string  `db:"model_auth_config"`
	ModelParams     string  `db:"model_params"`
	Description     *string `db:"description"`
	MaxInputTokens  *int    `db:"max_input_tokens"`
	ModelType       string  `db:"model_type"`
	PriceInput      *int64  `db:"price_input"`
	PriceOutput     *int64  `db:"price_output"`
	PriceCacheRead  *int64  `db:"price_cache_read"`
	PriceCacheWrite *int64  `db:"price_cache_write"`
	CreatedBy       string  `db:"created_by"`
	UpdatedBy       string  `db:"updated_by"`
	CreatedTime     int64   `db:"created_time"`
	UpdatedTime     int64   `db:"updated_time"`
}
