// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestGatewayCore_EnrichIdentity(t *testing.T) {
	t.Parallel()

	prof := &identity.Profile{ID: "u@x.com", DisplayName: "U", WorkEmail: "u@x.com"}

	t.Run("enriches when hook set and email present", func(t *testing.T) {
		t.Parallel()
		g := &GatewayCore{}
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			return auth.CachedProfile{Profile: prof, EnrichmentStatus: identity.EnrichmentOK}, nil
		})
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"})
		g.EnrichIdentity(context.Background(), id)
		if id.Profile() != prof {
			t.Fatalf("Profile = %+v, want enriched profile", id.Profile())
		}
		if id.EnrichmentStatus() != identity.EnrichmentOK {
			t.Errorf("EnrichmentStatus = %q, want ok", id.EnrichmentStatus())
		}
	})

	t.Run("no hook is a no-op", func(t *testing.T) {
		t.Parallel()
		g := &GatewayCore{}
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"})
		g.EnrichIdentity(context.Background(), id)
		if id.Profile() != nil || id.EnrichmentStatus() != "" {
			t.Errorf("expected no-op, got Profile=%+v status=%q", id.Profile(), id.EnrichmentStatus())
		}
	})

	t.Run("no email is a no-op (can't key the directory lookup)", func(t *testing.T) {
		t.Parallel()
		called := false
		g := &GatewayCore{}
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			called = true
			return auth.CachedProfile{}, nil
		})
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "slack:T1:U1"})
		g.EnrichIdentity(context.Background(), id)
		if called {
			t.Error("lookup must not run without an email")
		}
		if id.Profile() != nil {
			t.Errorf("Profile = %+v, want nil", id.Profile())
		}
	})

	t.Run("lookup error stamps EnrichmentUnavailable, stays usable", func(t *testing.T) {
		t.Parallel()
		g := &GatewayCore{}
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			return auth.CachedProfile{}, errors.New("broker timeout")
		})
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"})
		g.EnrichIdentity(context.Background(), id)
		if id.Profile() != nil {
			t.Errorf("Profile = %+v, want nil on error", id.Profile())
		}
		if id.EnrichmentStatus() != identity.EnrichmentUnavailable {
			t.Errorf("EnrichmentStatus = %q, want unavailable", id.EnrichmentStatus())
		}
	})

	t.Run("already-enriched identity is left untouched", func(t *testing.T) {
		t.Parallel()
		called := false
		g := &GatewayCore{}
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			called = true
			return auth.CachedProfile{Profile: prof}, nil
		})
		existing := &identity.Profile{ID: "other@x.com"}
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com", Profile: existing, EnrichmentStatus: identity.EnrichmentOK})
		g.EnrichIdentity(context.Background(), id)
		if called {
			t.Error("must not re-run enrichment when a profile is already present")
		}
		if id.Profile() != existing {
			t.Errorf("Profile overwritten: %+v", id.Profile())
		}
	})

	t.Run("cache hit short-circuits the lookup hook", func(t *testing.T) {
		t.Parallel()
		cache := auth.NewProfileCache(time.Hour)
		cache.Put("u@x.com", auth.CachedProfile{Profile: prof, EnrichmentStatus: identity.EnrichmentOK})
		called := false
		g := &GatewayCore{}
		g.SetProfileCache(cache)
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			called = true
			return auth.CachedProfile{}, nil
		})
		id := auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"})
		g.EnrichIdentity(context.Background(), id)
		if called {
			t.Error("hook must not run on a cache hit")
		}
		if id.Profile() != prof || id.EnrichmentStatus() != identity.EnrichmentOK {
			t.Errorf("not stamped from cache: %+v / %q", id.Profile(), id.EnrichmentStatus())
		}
	})

	t.Run("positive lookup is written to the cache", func(t *testing.T) {
		t.Parallel()
		cache := auth.NewProfileCache(time.Hour)
		g := &GatewayCore{}
		g.SetProfileCache(cache)
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			return auth.CachedProfile{Profile: prof, EnrichmentStatus: identity.EnrichmentOK}, nil
		})
		g.EnrichIdentity(context.Background(), auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"}))
		entry, ok := cache.Get("u@x.com")
		if !ok || entry.Profile != prof {
			t.Errorf("expected positive result cached, got ok=%v entry=%+v", ok, entry)
		}
	})

	t.Run("degraded lookup is NOT cached", func(t *testing.T) {
		t.Parallel()
		cache := auth.NewProfileCache(time.Hour)
		g := &GatewayCore{}
		g.SetProfileCache(cache)
		g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
			return auth.CachedProfile{EnrichmentStatus: identity.EnrichmentUnavailable, EnrichmentReason: "blip"}, nil
		})
		g.EnrichIdentity(context.Background(), auth.NewTestSubject(auth.SubjectFields{UserID: "u@x.com", Email: "u@x.com"}))
		if _, ok := cache.Get("u@x.com"); ok {
			t.Error("degraded status must not be cached")
		}
	})
}

func TestForChatUser_EnrichesThroughTheGatewayCore(t *testing.T) {
	t.Parallel()
	prof := &identity.Profile{ID: "u@x.com", DisplayName: "U", WorkEmail: "u@x.com"}
	for name, multiParty := range map[string]bool{"direct message": false, "shared channel": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lookups := 0
			g := &GatewayCore{}
			g.SetLazyEnrichmentLookup(func(_ context.Context, _ *auth.Subject) (auth.CachedProfile, error) {
				lookups++
				return auth.CachedProfile{Profile: prof, EnrichmentStatus: identity.EnrichmentOK}, nil
			})
			real := auth.ForSlackUser("u@x.com", "u@x.com", "U1", "T1")

			submitted, swapped := auth.ForChatUser(t.Context(), real, g, auth.ChannelPolicy{IsMultiParty: multiParty, RunAs: "ops"})

			if lookups != 1 {
				t.Errorf("profile lookups = %d, want 1", lookups)
			}
			if real.Profile() != prof || real.EnrichmentStatus() != identity.EnrichmentOK {
				t.Errorf("real caller profile/status = %+v/%q, want the enriched profile", real.Profile(), real.EnrichmentStatus())
			}
			if swapped != multiParty {
				t.Fatalf("swapped = %v, want %v", swapped, multiParty)
			}
			if multiParty && submitted.Profile() != nil {
				t.Errorf("swapped identity carries profile %+v, want none on a shared channel", submitted.Profile())
			}
			if !multiParty && submitted != real {
				t.Error("a direct message must submit as the enriched real caller")
			}
		})
	}
}
