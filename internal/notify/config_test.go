// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import "testing"

func TestParseNotifyConfig_Full(t *testing.T) {
	cfg, err := ParseNotifyConfig(map[string]any{
		"namespace":                 "my-ns",
		"database_url":              "sqlite:///tmp/x.db",
		"retention_days":            float64(7),
		"cors_allowed_origins":      []any{"http://localhost:3000", 42, "http://x"},
		"cors_allowed_origin_regex": "http://localhost.*",
		"notify": map[string]any{
			"directory": []any{
				map[string]any{
					"user_id": "trent",
					"channels": []any{
						map[string]any{"type": "webhook", "config": map[string]any{"url": "https://x"}},
						map[string]any{"type": "email", "config": map[string]any{}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseNotifyConfig: %v", err)
	}
	if cfg.Namespace != "my-ns" || cfg.DatabaseURL != "sqlite:///tmp/x.db" || cfg.RetentionDays != 7 {
		t.Errorf("scalar fields wrong: %+v", cfg)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Errorf("cors origins = %v", cfg.CORSAllowedOrigins)
	}
	if cfg.CORSAllowedOriginRegex != "http://localhost.*" {
		t.Errorf("cors regex = %q", cfg.CORSAllowedOriginRegex)
	}
	if chs := cfg.Directory["trent"]; len(chs) != 2 || chs[0].Type != "webhook" {
		t.Errorf("directory not parsed: %+v", cfg.Directory)
	}
}

func TestParseNotifyConfig_Defaults(t *testing.T) {
	cfg, err := ParseNotifyConfig(map[string]any{})
	if err != nil {
		t.Fatalf("ParseNotifyConfig: %v", err)
	}
	if cfg.Namespace == "" || cfg.RetentionDays != 30 || len(cfg.Directory) != 0 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

func TestParseNotifyConfig_Errors(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"bad namespace", map[string]any{"namespace": "bad ns"}},
		{"entry not mapping", map[string]any{"notify": map[string]any{"directory": []any{"x"}}}},
		{"missing user_id", map[string]any{"notify": map[string]any{"directory": []any{map[string]any{"channels": []any{}}}}}},
		{"channels not list", map[string]any{"notify": map[string]any{"directory": []any{map[string]any{"user_id": "a", "channels": "x"}}}}},
		{"channel missing type", map[string]any{"notify": map[string]any{"directory": []any{map[string]any{"user_id": "a", "channels": []any{map[string]any{}}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseNotifyConfig(tc.cfg); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestParseNotifyConfig_DirectoryNotListIsLenient(t *testing.T) {
	cfg, err := ParseNotifyConfig(map[string]any{"notify": map[string]any{}})
	if err != nil || len(cfg.Directory) != 0 {
		t.Errorf("expected lenient empty directory, got %+v err=%v", cfg, err)
	}
}
