// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type NotifyConfig struct {
	Namespace string

	DatabaseURL string

	RetentionDays int

	Directory map[string][]Channel

	CORSAllowedOrigins     []string
	CORSAllowedOriginRegex string

	ConfirmBaseURL string
}

func ParseNotifyConfig(appConfig map[string]any) (*NotifyConfig, error) {
	cfg := &NotifyConfig{
		Namespace:     config.DefaultNamespace,
		Directory:     map[string][]Channel{},
		RetentionDays: 30,
	}

	if ns, ok := appConfig["namespace"].(string); ok && ns != "" {
		cfg.Namespace = ns
	}
	if dbURL, ok := appConfig["database_url"].(string); ok {
		cfg.DatabaseURL = dbURL
	}
	if rd, ok := toInt(appConfig["retention_days"]); ok {
		cfg.RetentionDays = rd
	}
	if err := a2a.ValidateNamespace(cfg.Namespace); err != nil {
		return nil, fmt.Errorf("invalid namespace: %w", err)
	}

	if origins, ok := appConfig["cors_allowed_origins"].([]any); ok {
		for _, o := range origins {
			if s, ok := o.(string); ok {
				cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, s)
			}
		}
	}
	if rx, ok := appConfig["cors_allowed_origin_regex"].(string); ok {
		cfg.CORSAllowedOriginRegex = rx
	}

	notifySection, ok := appConfig["notify"].(map[string]any)
	if !ok {
		return cfg, nil
	}

	if raw, present := notifySection["confirm_base_url"]; present && raw != nil {
		base, isString := raw.(string)
		if !isString {
			return nil, fmt.Errorf("notify.confirm_base_url: must be a string, got %T", raw)
		}
		if err := validateConfirmBaseURL(base); err != nil {
			return nil, fmt.Errorf("notify.confirm_base_url: %w", err)
		}
		cfg.ConfirmBaseURL = base
	}

	dirEntries, ok := notifySection["directory"].([]any)
	if !ok {
		return cfg, nil
	}

	for i, raw := range dirEntries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("notify.directory[%d] is not a mapping", i)
		}
		userID, _ := entry["user_id"].(string)
		if userID == "" {
			return nil, fmt.Errorf("notify.directory[%d] missing user_id", i)
		}
		channels, err := parseChannels(entry["channels"])
		if err != nil {
			return nil, fmt.Errorf("notify.directory[%d] (user_id=%s): %w", i, userID, err)
		}
		cfg.Directory[userID] = append(cfg.Directory[userID], channels...)
	}

	return cfg, nil
}

func validateConfirmBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("must be an absolute http(s) URL, got %q", raw)
	}
	if u.RawQuery != "" || strings.Contains(u.Fragment, "?") {
		return fmt.Errorf("must not contain a query")
	}
	if !strings.HasPrefix(u.Fragment, "/") {
		return fmt.Errorf("must end in the web UI hash route, e.g. https://sam.example.com/#/notifications/confirm")
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func parseChannels(raw any) ([]Channel, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("channels must be a list")
	}
	out := make([]Channel, 0, len(list))
	for j, c := range list {
		cm, ok := c.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("channels[%d] is not a mapping", j)
		}
		ctype, _ := cm["type"].(string)
		if ctype == "" {
			return nil, fmt.Errorf("channels[%d] missing type", j)
		}
		cconfig, _ := cm["config"].(map[string]any)
		out = append(out, Channel{Type: ctype, Config: cconfig})
	}
	return out, nil
}
