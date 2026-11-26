// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type IdentityService interface {
	SearchUsers(ctx context.Context, query string, limit int) ([]*identity.Profile, error)
}

type PeopleSearchProbe interface {
	SearchConfigured(ctx context.Context) (bool, error)
}

type LazyEnrichmentLookup func(ctx context.Context, id *Subject) (CachedProfile, error)

type IdentityMiddlewareOption func(*identityMiddleware)

func WithUnavailableLogWindow(d time.Duration) IdentityMiddlewareOption {
	return func(m *identityMiddleware) { m.warnWindow = d }
}

func WithLazyEnrichmentLookup(fn LazyEnrichmentLookup) IdentityMiddlewareOption {
	return func(m *identityMiddleware) { m.lazyLookup = fn }
}

func WithLazyLookupBackoff(d time.Duration) IdentityMiddlewareOption {
	return func(m *identityMiddleware) { m.lazyBackoffWindow = d }
}

func IdentityMiddleware(cache *ProfileCache, logger *slog.Logger, opts ...IdentityMiddlewareOption) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	m := &identityMiddleware{
		cache:             cache,
		logger:            logger.With("logger_name", "identity-middleware"),
		warnWindow:        time.Minute,
		warnSeen:          make(map[string]time.Time),
		lazyBackoffWindow: 30 * time.Second,
		lazyBackoffUntil:  make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m.wrap
}

type identityMiddleware struct {
	cache      *ProfileCache
	lazyLookup LazyEnrichmentLookup
	logger     *slog.Logger
	warnWindow time.Duration

	warnMu          sync.Mutex
	warnSeen        map[string]time.Time
	suppressedCount int64

	lazyBackoffWindow time.Duration
	lazyBackoffMu     sync.Mutex
	lazyBackoffUntil  map[string]time.Time
}

func (m *identityMiddleware) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := SubjectFromContext(r.Context())
		if !ok || id == nil {
			next.ServeHTTP(w, r)
			return
		}

		if id.profile != nil || id.enrichmentStatus != "" {
			m.checkProfileLeak(id)
			next.ServeHTTP(w, r)
			return
		}

		if m.cache == nil {
			id.enrichmentStatus = identity.EnrichmentNotConfigured
			next.ServeHTTP(w, r)
			return
		}

		entry, hit := m.cache.Get(id.userID)
		if !hit {
			if m.lazyLookup != nil {
				if m.inLazyBackoff(id.userID) {
					id.enrichmentStatus = identity.EnrichmentUnavailable
					id.enrichmentReason = "enrichment backend unavailable (lookup backoff)"
					m.logEnrichmentDegraded(id.userID, identity.EnrichmentUnavailable, id.enrichmentReason)
					next.ServeHTTP(w, r)
					return
				}
				fetched, err := m.lazyLookup(r.Context(), id)
				if err != nil {
					m.armLazyBackoff(id.userID)
					id.enrichmentStatus = identity.EnrichmentUnavailable
					id.enrichmentReason = "lazy lookup failed: " + err.Error()
					m.logEnrichmentDegraded(id.userID, identity.EnrichmentUnavailable, id.enrichmentReason)
					next.ServeHTTP(w, r)
					return
				}
				m.clearLazyBackoff(id.userID)
				if ShouldCacheEnrichment(fetched.EnrichmentStatus) {
					m.cache.Put(id.userID, fetched)
				}
				entry = fetched
			} else {
				id.enrichmentStatus = identity.EnrichmentUnavailable
				id.enrichmentReason = "cache miss"
				m.logEnrichmentDegraded(id.userID, identity.EnrichmentUnavailable, "cache miss")
				next.ServeHTTP(w, r)
				return
			}
		}

		id.profile = entry.Profile
		id.enrichmentStatus = entry.EnrichmentStatus
		id.enrichmentReason = entry.EnrichmentReason
		m.checkProfileLeak(id)

		switch entry.EnrichmentStatus {
		case identity.EnrichmentOK, identity.EnrichmentNotConfigured, "":
		case identity.EnrichmentUnavailable, identity.EnrichmentStale:
			m.logEnrichmentDegraded(id.userID, entry.EnrichmentStatus, entry.EnrichmentReason)
		default:
			m.logger.Warn("identity cache holds unknown enrichment status",
				"userID", id.userID,
				"enrichmentStatus", string(entry.EnrichmentStatus),
				"reason", entry.EnrichmentReason,
			)
		}

		next.ServeHTTP(w, r)
	})
}

func ShouldCacheEnrichment(s identity.EnrichmentStatus) bool {
	switch s {
	case identity.EnrichmentOK, identity.EnrichmentNotConfigured, "":
		return true
	default:
		return false
	}
}

func (m *identityMiddleware) inLazyBackoff(userID string) bool {
	if m.lazyBackoffWindow <= 0 {
		return false
	}
	m.lazyBackoffMu.Lock()
	defer m.lazyBackoffMu.Unlock()
	until, ok := m.lazyBackoffUntil[userID]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(m.lazyBackoffUntil, userID)
	return false
}

func (m *identityMiddleware) armLazyBackoff(userID string) {
	if m.lazyBackoffWindow <= 0 {
		return
	}
	m.lazyBackoffMu.Lock()
	m.lazyBackoffUntil[userID] = time.Now().Add(m.lazyBackoffWindow)
	m.lazyBackoffMu.Unlock()
}

func (m *identityMiddleware) clearLazyBackoff(userID string) {
	if m.lazyBackoffWindow <= 0 {
		return
	}
	m.lazyBackoffMu.Lock()
	delete(m.lazyBackoffUntil, userID)
	m.lazyBackoffMu.Unlock()
}

func (m *identityMiddleware) checkProfileLeak(id *Subject) {
	if id == nil || id.profile == nil {
		return
	}
	isSelf := func(v string) bool {
		return strings.EqualFold(v, id.userID) || strings.EqualFold(v, id.email)
	}
	for _, f := range []struct{ name, val string }{
		{"workEmail", id.profile.WorkEmail},
		{"profileId", id.profile.ID},
	} {
		if strings.Contains(f.val, "@") && !isSelf(f.val) {
			m.logger.Error("cross-user profile leak detected: enriched profile does not belong to the authenticated user",
				"userID", id.userID,
				"mismatchField", f.name,
				"userEmailFP", strutil.PIIFingerprint(id.email),
				"profileValueFP", strutil.PIIFingerprint(f.val),
				"profileIDFP", strutil.PIIFingerprint(id.profile.ID),
				"enrichmentStatus", string(id.enrichmentStatus),
			)
			return
		}
	}
}

func (m *identityMiddleware) logEnrichmentDegraded(userID string, status identity.EnrichmentStatus, reason string) {
	key := string(status) + "|" + reason
	now := time.Now()

	m.warnMu.Lock()
	last, seen := m.warnSeen[key]
	if seen && now.Sub(last) < m.warnWindow {
		m.suppressedCount++
		m.warnMu.Unlock()
		return
	}
	m.warnSeen[key] = now
	suppressed := m.suppressedCount
	m.suppressedCount = 0
	m.warnMu.Unlock()

	m.logger.Warn("profile enrichment degraded",
		"userID", userID,
		"enrichmentStatus", string(status),
		"reason", reason,
		"suppressedSinceLast", suppressed,
	)
}
