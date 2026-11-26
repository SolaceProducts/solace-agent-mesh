// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"time"
)

const (
	WhatsAppMessageProcessing = inboundClaimProcessing
	WhatsAppMessageRetrying   = inboundClaimRetrying
	WhatsAppMessageDone       = inboundClaimDone
)

type ClaimOutcome int

const (
	ClaimAcquired ClaimOutcome = iota
	ClaimDuplicate
	ClaimInFlight
)

func (o ClaimOutcome) String() string {
	switch o {
	case ClaimAcquired:
		return "acquired"
	case ClaimDuplicate:
		return "duplicate"
	case ClaimInFlight:
		return "in_flight"
	}
	return "unknown"
}

type WhatsAppClaim = DedupClaim

const WhatsAppStalledFailure = StalledClaimFailure

const claimRaceRetries = 3

type WhatsAppProcessedRepo struct {
	claims inboundClaimTable
}

func NewWhatsAppProcessedRepo(db *DB) *WhatsAppProcessedRepo {
	return &WhatsAppProcessedRepo{claims: inboundClaimTable{
		db: db, table: "whatsapp_processed_messages", keyColumn: "wamid", repoName: "WhatsAppProcessedRepo",
	}}
}

func (r *WhatsAppProcessedRepo) Claim(ctx context.Context, gatewayID, wamid string, lease time.Duration) (WhatsAppClaim, error) {
	return r.claims.Claim(ctx, gatewayID, wamid, lease)
}

func (r *WhatsAppProcessedRepo) MarkDone(ctx context.Context, gatewayID, wamid string, token int64) (bool, error) {
	return r.claims.MarkDone(ctx, gatewayID, wamid, token)
}

func (r *WhatsAppProcessedRepo) ReleaseForRetry(ctx context.Context, gatewayID, wamid string, token int64, kind string) (bool, error) {
	return r.claims.ReleaseForRetry(ctx, gatewayID, wamid, token, kind)
}

func (r *WhatsAppProcessedRepo) State(ctx context.Context, gatewayID, wamid string) (string, error) {
	return r.claims.State(ctx, gatewayID, wamid)
}

func (r *WhatsAppProcessedRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	return r.claims.DeleteOlderThan(ctx, before, batchSize)
}
