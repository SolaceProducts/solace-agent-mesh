// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokencache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
)

var refreshSkew = 5 * time.Minute

type RefreshClient interface {
	Refresh(ctx context.Context, cfg oauth.Config, refreshToken, samAccessToken string) (oauth.Tokens, error)
}

type Resolver struct {
	store *Store
	rc    RefreshClient
}

func NewResolver(store *Store, rc RefreshClient) *Resolver {
	return &Resolver{store: store, rc: rc}
}

func (r *Resolver) ResolveSAMToken(ctx context.Context, t target.Target) (string, error) {
	name := t.CacheName()
	cached, err := r.store.Load(name)
	if err != nil {
		return "", err
	}
	if cached == nil {
		return "", fmt.Errorf("%w (target %q)", target.ErrNotLoggedIn, name)
	}

	if time.Until(cached.ExpiresAt) > refreshSkew || cached.RefreshToken == "" || r.rc == nil {
		if cached.SAMAccessToken == "" {
			return "", fmt.Errorf("%w (target %q: cached SAM token is empty)", target.ErrNotLoggedIn, name)
		}
		return cached.SAMAccessToken, nil
	}

	return r.doRefresh(ctx, name, cached)
}

func (r *Resolver) ForceRefresh(ctx context.Context, t target.Target) (string, error) {
	name := t.CacheName()
	cached, err := r.store.Load(name)
	if err != nil {
		return "", err
	}
	if cached == nil || cached.RefreshToken == "" || r.rc == nil {
		return "", fmt.Errorf("%w (target %q: no refresh token cached)", target.ErrNotLoggedIn, name)
	}
	return r.doRefresh(ctx, name, cached)
}

func (r *Resolver) doRefresh(ctx context.Context, name string, cached *Cached) (string, error) {
	cfg := oauth.Config{
		PlatformURL: cached.PlatformURL,
		Issuer:      cached.Issuer,
	}
	fresh, err := r.rc.Refresh(ctx, cfg, cached.RefreshToken, cached.SAMAccessToken)
	if err != nil {
		if errors.Is(err, oauth.ErrRefreshDenied) {
			_ = r.store.Delete(name)
			return "", fmt.Errorf("%w (target %q: refresh denied — run `sam auth login --target %s`)", target.ErrNotLoggedIn, name, name)
		}
		return "", fmt.Errorf("tokencache: refresh target %q: %w", name, err)
	}

	fresh.RefreshToken = keepRefreshTokenForIDPsThatDoNotRotate(fresh.RefreshToken, cached.RefreshToken)

	updated := Cached{
		PlatformURL:    cached.PlatformURL,
		Issuer:         cached.Issuer,
		AccessToken:    fresh.AccessToken,
		SAMAccessToken: fresh.SAMAccessToken,
		RefreshToken:   fresh.RefreshToken,
		ExpiresAt:      fresh.ExpiresAt,
		ObtainedAt:     fresh.ObtainedAt,
	}
	_ = r.store.Save(name, updated)
	return updated.SAMAccessToken, nil
}

func keepRefreshTokenForIDPsThatDoNotRotate(refreshed, previous string) string {
	if refreshed == "" {
		return previous
	}
	return refreshed
}
