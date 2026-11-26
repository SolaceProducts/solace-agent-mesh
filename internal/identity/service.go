// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"time"
)

type EnrichmentStatus string

const (
	EnrichmentOK            EnrichmentStatus = "ok"
	EnrichmentUnavailable   EnrichmentStatus = "unavailable"
	EnrichmentStale         EnrichmentStatus = "stale"
	EnrichmentNotConfigured EnrichmentStatus = "not_configured"
)

type LookupResolution string

const (
	LookupUnknown       LookupResolution = ""
	LookupMatched       LookupResolution = "matched"
	LookupNoMatch       LookupResolution = "no_match"
	LookupAmbiguous     LookupResolution = "ambiguous"
	LookupRefused       LookupResolution = "refused"
	LookupNotConfigured LookupResolution = "not_configured"
	LookupDegraded      LookupResolution = "degraded"
)

func (r LookupResolution) Recognized() bool {
	switch r {
	case LookupUnknown, LookupMatched, LookupNoMatch, LookupAmbiguous,
		LookupRefused, LookupNotConfigured, LookupDegraded:
		return true
	}
	return false
}

type RolesSource string

const (
	RolesSourceUnknown  RolesSource = ""
	RolesSourceAssigned RolesSource = "assigned"
	RolesSourceDefaults RolesSource = "defaults"
)

const (
	scopeCacheTTL            = 5 * time.Minute
	profileCacheTTL          = 1 * time.Hour
	searchCacheTTL           = 30 * time.Minute
	searchConfiguredCacheTTL = 30 * time.Second
	coldCacheWait            = 3 * time.Second
)

type Resolution struct {
	Scopes           []string
	Profile          *Profile
	EnrichmentStatus EnrichmentStatus
	EnrichmentReason string
}

type IdentityService interface {
	Resolve(ctx context.Context, claims map[string]any) (*Resolution, error)
	SearchUsers(ctx context.Context, query string, limit int) ([]*Profile, error)
	GetUserProfile(ctx context.Context, userID string) (map[string]any, error)
	GetTimeOff(ctx context.Context, userID, start, end string) ([]TimeOffEntry, error)
	GetUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error)
}

type ScopeResolver interface {
	Resolve(ctx context.Context, claims map[string]any) ([]string, error)
}

type Enricher interface {
	EnrichAuthenticatedUser(ctx context.Context, lookupValue string) (*Profile, error)
	SearchUsers(ctx context.Context, query string, limit int) ([]*Profile, error)
	GetUserProfile(ctx context.Context, userID string) (map[string]any, error)
	GetTimeOff(ctx context.Context, userID, start, end string) ([]TimeOffEntry, error)
	GetUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error)
}

type ConfigSource interface {
	Get(ctx context.Context) (toolsetID, lookupKey string, configured bool, err error)
}
