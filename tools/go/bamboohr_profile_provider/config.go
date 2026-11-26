// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultLookupKey      = "email"
	defaultCacheTTL       = time.Hour
	defaultRequestTimeout = 30 * time.Second
)

type bamboohrConfig struct {
	Subdomain      string
	APIKey         string
	LookupKey      string
	CacheTTL       time.Duration
	RequestTimeout time.Duration
	BaseURL        string
}

func loadConfig(tc *sdk.ToolContext) (*bamboohrConfig, string) {
	cfg := &bamboohrConfig{
		Subdomain:      strings.TrimSpace(tc.GetConfigString("bamboohr_subdomain", "")),
		APIKey:         strings.TrimSpace(tc.GetConfigString("bamboohr_api_key", "")),
		LookupKey:      tc.GetConfigString("lookup_key", defaultLookupKey),
		CacheTTL:       defaultCacheTTL,
		RequestTimeout: defaultRequestTimeout,
		BaseURL:        tc.GetConfigString("base_url", ""),
	}
	if v, ok := tc.GetConfig("cache_ttl_seconds"); ok {
		if n := asInt(v); n > 0 {
			cfg.CacheTTL = time.Duration(n) * time.Second
		}
	}
	if v, ok := tc.GetConfig("request_timeout_seconds"); ok {
		if n := asInt(v); n > 0 {
			cfg.RequestTimeout = time.Duration(n) * time.Second
		}
	}
	if cfg.Subdomain == "" {
		return nil, "bamboohr_subdomain is required in tool config."
	}
	if cfg.APIKey == "" {
		return nil, "bamboohr_api_key is required in tool config."
	}
	if cfg.CacheTTL < time.Minute {
		return nil, fmt.Sprintf("cache_ttl_seconds must be >= 60 (got %s).", cfg.CacheTTL)
	}
	return cfg, ""
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

var configSchema = []sdk.ConfigSchemaField{
	{Key: "bamboohr_subdomain", Type: "string", Description: "BambooHR subdomain (e.g. \"mycompany\").", Required: true},
	{Key: "bamboohr_api_key", Type: "string", Description: "BambooHR API key.", Required: true, Secret: true},
	{Key: "lookup_key", Type: "string", Description: "JWT claim used as the lookup value passed to enrich_authenticated_user.", Default: defaultLookupKey},
	{Key: "cache_ttl_seconds", Type: "integer", Description: "Directory snapshot cache TTL in seconds (>=60).", Default: 3600},
	{Key: "request_timeout_seconds", Type: "integer", Description: "Per-HTTP-request timeout in seconds.", Default: 30},
	{Key: "base_url", Type: "string", Description: "Override the BambooHR API base URL (test hook)."},
}
