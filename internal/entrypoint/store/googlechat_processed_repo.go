// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"time"
)

type GoogleChatProcessedRepo struct {
	claims inboundClaimTable
}

func NewGoogleChatProcessedRepo(db *DB) *GoogleChatProcessedRepo {
	return &GoogleChatProcessedRepo{claims: inboundClaimTable{
		db: db, table: "googlechat_processed_events", keyColumn: "event_key", repoName: "GoogleChatProcessedRepo",
	}}
}

func (r *GoogleChatProcessedRepo) Claim(ctx context.Context, gatewayID, eventKey string, lease time.Duration) (DedupClaim, error) {
	return r.claims.Claim(ctx, gatewayID, eventKey, lease)
}

func (r *GoogleChatProcessedRepo) MarkDone(ctx context.Context, gatewayID, eventKey string, token int64) (bool, error) {
	return r.claims.MarkDone(ctx, gatewayID, eventKey, token)
}

func (r *GoogleChatProcessedRepo) ReleaseForRetry(ctx context.Context, gatewayID, eventKey string, token int64, kind string) (bool, error) {
	return r.claims.ReleaseForRetry(ctx, gatewayID, eventKey, token, kind)
}

func (r *GoogleChatProcessedRepo) State(ctx context.Context, gatewayID, eventKey string) (string, error) {
	return r.claims.State(ctx, gatewayID, eventKey)
}

func (r *GoogleChatProcessedRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	return r.claims.DeleteOlderThan(ctx, before, batchSize)
}
