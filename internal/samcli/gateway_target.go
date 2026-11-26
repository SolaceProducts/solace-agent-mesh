// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const DesktopTargetName = "desktop"

type gatewayTargetRef struct {
	URL      string
	Target   string
	Manifest string
}

func resolveGatewayTarget(_ context.Context, ref gatewayTargetRef, defaultFallback string) (target.Target, error) {
	override, err := platformURLFromFlags(ref.URL, ref.Target)
	if err != nil {
		return target.Target{}, err
	}
	if ref.Manifest != "" {
		m, loadErr := manifest.Load(ref.Manifest)
		if loadErr != nil {
			return target.Target{}, fmt.Errorf("--manifest %q: %w", ref.Manifest, loadErr)
		}
		return resolveManifestTarget(ref.Manifest, m.Target, override)
	}
	if override != "" {
		return target.Target{URL: override}, nil
	}
	url, err := resolveImplicitGatewayURL(defaultFallback)
	return target.Target{URL: url}, err
}

func resolveManifestTarget(manifestPath string, block manifest.TargetBlock, urlOverride string) (target.Target, error) {
	tg := target.Target{
		Name: block.Name,
		URL:  urlOverride,
		Auth: target.AuthSpec{
			Type:   target.AuthType(block.Auth.Type),
			EnvVar: block.Auth.EnvVar,
		},
	}
	switch {
	case tg.URL != "":
	case block.URL != "":
		tg.URL = strings.TrimRight(block.URL, "/")
	case block.Name != "":
		url, err := lookupTargetURL(block.Name)
		if err != nil {
			return target.Target{}, fmt.Errorf("--manifest %q: target.name %q: %w", manifestPath, block.Name, err)
		}
		tg.URL = url
	default:
		return target.Target{}, fmt.Errorf("--manifest %q: %w", manifestPath, errManifestTargetURLEmpty)
	}
	return tg, nil
}

var errManifestTargetURLEmpty = errors.New("target.url is empty")

func resolveImplicitGatewayURL(defaultFallback string) (string, error) {
	if v := os.Getenv("SAM_WEBUI_URL"); v != "" {
		return normaliseBaseURL(v), nil
	}

	store, err := tokencache.NewDefault()
	if err == nil {
		names, _ := store.List()
		if len(names) == 1 {
			cached, _ := store.Load(names[0])
			if cached != nil && cached.PlatformURL != "" {
				return strings.TrimRight(cached.PlatformURL, "/"), nil
			}
		}
		if len(names) > 1 {
			return "", fmt.Errorf("multiple cached targets — pick one with --target (see `sam auth list`):\n  %s", strings.Join(names, "\n  "))
		}
	}

	if defaultFallback != "" {
		return defaultFallback, nil
	}
	return "", errors.New("no base URL: pass --url / --target / --manifest, set SAM_WEBUI_URL, or run `sam auth login` first")
}

func resolveTargetURL(name string) (string, error) {
	url, err := lookupTargetURL(name)
	if err != nil {
		return "", fmt.Errorf("--target %q: %w", name, err)
	}
	return url, nil
}

func lookupTargetURL(name string) (string, error) {
	if strings.EqualFold(name, DesktopTargetName) {
		return lookupDesktopTargetURL()
	}
	store, err := tokencache.NewDefault()
	if err != nil {
		return "", err
	}
	cached, err := store.Load(name)
	if err != nil {
		return "", err
	}
	if cached == nil {
		return "", fmt.Errorf("no cache entry (run `sam auth login %s`)", name)
	}
	return strings.TrimRight(cached.PlatformURL, "/"), nil
}

func lookupDesktopTargetURL() (string, error) {
	info, ok, err := settings.ReadDesktopInstance()
	if err != nil {
		return "", err
	}
	if !ok || info.URL == "" {
		return "", errors.New("no running SAM desktop app found; start the desktop app or pass --url")
	}
	return strings.TrimRight(info.URL, "/"), nil
}

func platformURLFromFlags(url, target string) (string, error) {
	if url != "" {
		return normaliseBaseURL(url), nil
	}
	if target != "" {
		return resolveTargetURL(target)
	}
	return "", nil
}

func resolveGatewayBearer(ctx context.Context, baseURL, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := envFirstNonEmpty("SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN"); v != "" {
		return v
	}
	return resolveGatewayToken(ctx, baseURL, "")
}

type gatewayCredential struct {
	Token        string
	Refresh      *target.Target
	FromManifest bool
}

func resolveGatewayCredential(ctx context.Context, tg target.Target) (gatewayCredential, error) {
	if v := envFirstNonEmpty("SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN"); v != "" {
		return gatewayCredential{Token: v}, nil
	}
	if tg.Auth.Type == "" {
		return cachedLoginCredential(ctx, implicitLoginCandidates(tg)...), nil
	}
	if err := namedLoginOriginErr(tg); err != nil {
		return gatewayCredential{}, err
	}
	resolver, err := defaultOAuthResolver()
	if err != nil {
		return gatewayCredential{}, err
	}
	resolved, err := tg.Resolve(ctx, resolver)
	if err != nil {
		return gatewayCredential{}, err
	}
	cred := gatewayCredential{Token: resolved.Token, FromManifest: true}
	if resolved.FromOAuthCache {
		cred.Refresh = &tg
	}
	return cred, nil
}

func implicitLoginCandidates(tg target.Target) []target.Target {
	oauthAuth := target.AuthSpec{Type: target.AuthOAuth}
	byHost := target.Target{URL: tg.URL, Auth: oauthAuth}
	named := target.Target{Name: tg.Name, URL: tg.URL, Auth: oauthAuth}
	if strings.TrimSpace(tg.Name) == "" || !loginIssuedFor(named) {
		return []target.Target{byHost}
	}
	return []target.Target{named, byHost}
}

func namedLoginOriginErr(tg target.Target) error {
	if tg.Auth.Type != target.AuthOAuth || strings.TrimSpace(tg.Name) == "" {
		return nil
	}
	issued, ok := cachedLoginOrigin(tg)
	if !ok || sameOrigin(issued, tg.URL) {
		return nil
	}
	return fmt.Errorf("target %q: its `sam auth login` was issued for %s, not %s; fix target.url or --url, or run `sam auth login --target %s --url %s`",
		tg.Name, requestOrigin(issued), requestOrigin(tg.URL), tg.CacheName(), requestOrigin(tg.URL))
}

func loginIssuedFor(tg target.Target) bool {
	issued, ok := cachedLoginOrigin(tg)
	return ok && sameOrigin(issued, tg.URL)
}

func cachedLoginOrigin(tg target.Target) (string, bool) {
	store, err := tokencache.NewDefault()
	if err != nil {
		return "", false
	}
	cached, err := store.Load(tg.CacheName())
	if err != nil || cached == nil {
		return "", false
	}
	return cached.PlatformURL, true
}

func cachedLoginCredential(ctx context.Context, candidates ...target.Target) gatewayCredential {
	resolver, err := defaultOAuthResolver()
	if err != nil {
		return gatewayCredential{}
	}
	for _, c := range candidates {
		res, err := c.Resolve(ctx, resolver)
		if err != nil || res.Token == "" {
			logSkippedNamedLogin(c, err)
			continue
		}
		cred := gatewayCredential{Token: res.Token, FromManifest: c.Name != ""}
		if res.FromOAuthCache {
			cred.Refresh = &c
		}
		return cred
	}
	return gatewayCredential{}
}

func logSkippedNamedLogin(c target.Target, err error) {
	if c.Name == "" {
		return
	}
	slog.Default().With("logger_name", "samcli").
		Debug("named login yielded no token; trying the next login candidate", "target", c.Name, "resolveFailed", err != nil)
}

func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	return requestOrigin(a) == requestOrigin(b)
}

func requestOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "the requested URL"
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = defaultPortForScheme(scheme)
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func defaultPortForScheme(scheme string) string {
	if scheme == "http" {
		return "80"
	}
	return "443"
}

func assertSafeBearerOverHTTP(fullURL, token string, insecure bool) error {
	if token == "" {
		return nil
	}
	if insecure {
		return nil
	}
	if strings.HasPrefix(strings.ToLower(fullURL), schemeHTTP) {
		return errors.New("refusing to send a bearer token over plain http:// — pass --insecure if you really mean it")
	}
	return nil
}
