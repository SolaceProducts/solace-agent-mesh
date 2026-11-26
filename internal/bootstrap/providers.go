// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
)

var defaultProviderScopes = []string{"openid", "email", "profile", "offline_access"}

func LoadProvidersCatalog(cfg config.Config, logger *slog.Logger) (map[string]*oidcauth.OIDCConfig, error) {
	logger = logger.With("logger_name", "oidc-providers-catalog")

	if !cfg.Has("providers") {
		logger.Info("providers catalog not configured")
		return nil, nil
	}

	raw := cfg.GetRaw("providers")
	asMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("oidc providers: must be a map of provider-name -> config; got %T", raw)
	}

	if len(asMap) == 0 {
		logger.Warn("providers catalog is empty; no OIDC providers will be available")
		return nil, nil
	}

	catalog := make(map[string]*oidcauth.OIDCConfig, len(asMap))
	for name, rawEntry := range asMap {
		entry, err := parseProviderEntry(name, rawEntry, logger)
		if err != nil {
			return nil, fmt.Errorf("oidc providers: entry %q: %w", name, err)
		}
		if entry == nil {
			continue
		}
		catalog[name] = entry
	}

	if len(catalog) == 0 {
		logger.Warn("providers catalog loaded but all entries were invalid; no OIDC providers will be available")
		return nil, nil
	}

	names := make([]string, 0, len(catalog))
	for k := range catalog {
		names = append(names, k)
	}
	slices.Sort(names)
	logger.Info("providers catalog loaded", "providerCount", len(catalog), "providerNames", names)

	return catalog, nil
}

func parseProviderEntry(name string, raw any, logger *slog.Logger) (*oidcauth.OIDCConfig, error) {
	entryMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be a map; got %T", raw)
	}

	issuer := stringField(entryMap, "issuer")
	clientID := stringField(entryMap, "client_id")

	switch {
	case issuer == "":
		logger.Warn("providers catalog entry invalid; skipping",
			"providerName", name, "reason", "missing_issuer")
		return nil, nil
	case clientID == "":
		logger.Warn("providers catalog entry invalid; skipping",
			"providerName", name, "reason", "missing_client_id")
		return nil, nil
	}

	cfg := &oidcauth.OIDCConfig{
		Issuer:             issuer,
		ClientID:           clientID,
		ClientSecret:       stringField(entryMap, "client_secret"),
		RedirectURI:        stringField(entryMap, "redirect_uri"),
		DevMode:            boolField(entryMap, "dev_mode"),
		Scopes:             scopesField(entryMap, "scopes", name, logger),
		CACertPath:         stringField(entryMap, "ca_cert_path"),
		InsecureSkipVerify: boolField(entryMap, "insecure_skip_verify"),
	}
	return cfg, nil
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func scopesField(m map[string]any, key, providerName string, logger *slog.Logger) []string {
	scopes := parseScopes(m, key, providerName, logger)
	if !slices.Contains(scopes, openIDScope) {
		logger.Warn("providers catalog entry scopes omit openid; login may fail at userinfo or lose id_token claims such as groups",
			"providerName", providerName,
			"scopes", scopes,
			"hint", "add openid to scopes or OIDC_SCOPES",
		)
	}
	return scopes
}

const openIDScope = "openid"

func parseScopes(m map[string]any, key, providerName string, logger *slog.Logger) []string {
	v, ok := m[key]
	if !ok {
		return append([]string(nil), defaultProviderScopes...)
	}
	var out []string
	switch typed := v.(type) {
	case string:
		out = splitScopeString(typed)
	case []any:
		out = scopeListStrings(typed, providerName, logger)
	default:
		logger.Warn("providers catalog entry has malformed scopes; using defaults",
			"providerName", providerName,
			"reason", "scopes_not_a_list_or_string",
			"gotType", fmt.Sprintf("%T", v),
		)
		return append([]string(nil), defaultProviderScopes...)
	}
	if len(out) == 0 {
		logger.Warn("providers catalog entry scopes is empty after filtering; using defaults",
			"providerName", providerName,
		)
		return append([]string(nil), defaultProviderScopes...)
	}
	return out
}

func splitScopeString(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

func scopeListStrings(list []any, providerName string, logger *slog.Logger) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			logger.Warn("providers catalog entry scopes contains non-string; skipping element",
				"providerName", providerName,
				"gotType", fmt.Sprintf("%T", item),
			)
			continue
		}
		out = append(out, strings.Fields(s)...)
	}
	return out
}
