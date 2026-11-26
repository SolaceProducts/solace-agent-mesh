// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func loadYAML(t *testing.T, yaml string) config.Config {
	t.Helper()
	loader := &config.YAMLLoader{}
	cfg, err := loader.LoadBytes(t.Context(), []byte(yaml))
	if err != nil {
		t.Fatalf("parsing YAML: %v", err)
	}
	return cfg
}

func TestLoadProvidersCatalog_TwoEntries(t *testing.T) {
	yaml := `
providers:
  azure:
    issuer: "https://login.microsoftonline.com/t/v2.0"
    client_id: "az-id"
    client_secret: "az-secret"
    scopes: ["openid","email","profile"]
  keycloak:
    issuer: "https://kc/realms/sam"
    client_id: "kc-id"
    client_secret: "kc-secret"
`
	cfg := loadYAML(t, yaml)

	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog returned error: %v", err)
	}
	if got, want := len(catalog), 2; got != want {
		t.Fatalf("len(catalog) = %d, want %d", got, want)
	}

	azure, ok := catalog["azure"]
	if !ok {
		t.Fatalf("catalog missing azure entry; keys: %v", keysOf(catalog))
	}
	if got, want := azure.Issuer, "https://login.microsoftonline.com/t/v2.0"; got != want {
		t.Errorf("azure.Issuer = %q, want %q", got, want)
	}
	if got, want := azure.ClientID, "az-id"; got != want {
		t.Errorf("azure.ClientID = %q, want %q", got, want)
	}
	if got, want := azure.ClientSecret, "az-secret"; got != want {
		t.Errorf("azure.ClientSecret = %q, want %q", got, want)
	}
	wantScopes := []string{"openid", "email", "profile"}
	if !stringSlicesEqual(azure.Scopes, wantScopes) {
		t.Errorf("azure.Scopes = %v, want %v", azure.Scopes, wantScopes)
	}

	keycloak, ok := catalog["keycloak"]
	if !ok {
		t.Fatalf("catalog missing keycloak entry; keys: %v", keysOf(catalog))
	}
	if got, want := keycloak.ClientID, "kc-id"; got != want {
		t.Errorf("keycloak.ClientID = %q, want %q", got, want)
	}
	if got, want := keycloak.Issuer, "https://kc/realms/sam"; got != want {
		t.Errorf("keycloak.Issuer = %q, want %q", got, want)
	}
	if got, want := keycloak.ClientSecret, "kc-secret"; got != want {
		t.Errorf("keycloak.ClientSecret = %q, want %q", got, want)
	}
	wantDefaults := []string{"openid", "email", "profile", "offline_access"}
	if !stringSlicesEqual(keycloak.Scopes, wantDefaults) {
		t.Errorf("keycloak.Scopes = %v, want defaults %v", keycloak.Scopes, wantDefaults)
	}
}

func TestLoadProvidersCatalog_Absent(t *testing.T) {
	yaml := `
apps: []
`
	cfg := loadYAML(t, yaml)

	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog returned error: %v", err)
	}
	if catalog != nil {
		t.Fatalf("catalog = %v, want nil", catalog)
	}
}

func TestLoadProvidersCatalog_EmptyMap(t *testing.T) {
	yaml := `
providers: {}
`
	cfg := loadYAML(t, yaml)

	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog returned error: %v", err)
	}
	if catalog != nil {
		t.Fatalf("catalog = %v, want nil", catalog)
	}
}

func TestLoadProvidersCatalog_NotAMap(t *testing.T) {
	yaml := `
providers: [a, b]
`
	cfg := loadYAML(t, yaml)

	_, err := LoadProvidersCatalog(cfg, discardLogger())
	if err == nil {
		t.Fatalf("LoadProvidersCatalog returned nil error, want error about expected shape")
	}
	if !strings.Contains(err.Error(), "map of provider-name") {
		t.Fatalf("error message = %q, want it to contain %q",
			err.Error(), "map of provider-name")
	}
}

func TestLoadProvidersCatalog_DropsInvalidEntries(t *testing.T) {
	yaml := `
providers:
  valid:
    issuer: "https://good/"
    client_id: "good-id"
  bad_no_issuer:
    client_id: "x"
  bad_no_client_id:
    issuer: "https://also-good/"
`
	cfg := loadYAML(t, yaml)

	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog returned error: %v", err)
	}
	if got, want := len(catalog), 1; got != want {
		t.Fatalf("len(catalog) = %d, want %d; keys: %v",
			got, want, keysOf(catalog))
	}
	if _, ok := catalog["valid"]; !ok {
		t.Fatalf("catalog missing 'valid' entry; keys: %v", keysOf(catalog))
	}
}

func captureLogger(t *testing.T) (*slog.Logger, *[]slog.Record) {
	t.Helper()
	var records []slog.Record
	h := slogRecordHandler{recs: &records}
	return slog.New(h), &records
}

type slogRecordHandler struct{ recs *[]slog.Record }

func (h slogRecordHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h slogRecordHandler) Handle(_ context.Context, r slog.Record) error {
	*h.recs = append(*h.recs, r)
	return nil
}
func (h slogRecordHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h slogRecordHandler) WithGroup(_ string) slog.Handler      { return h }

func TestLoadProvidersCatalog_TLSFields(t *testing.T) {
	yaml := `
providers:
  custom-ca:
    issuer: "https://self-signed.example.com/"
    client_id: "c"
    client_secret: "s"
    ca_cert_path: "/etc/pki/custom-ca.pem"
  skip-verify:
    issuer: "https://lab.example.com/"
    client_id: "c"
    client_secret: "s"
    insecure_skip_verify: true
`
	cfg := loadYAML(t, yaml)
	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog: %v", err)
	}
	if got := catalog["custom-ca"].CACertPath; got != "/etc/pki/custom-ca.pem" {
		t.Errorf("custom-ca CACertPath=%q want /etc/pki/custom-ca.pem", got)
	}
	if catalog["custom-ca"].InsecureSkipVerify {
		t.Errorf("custom-ca InsecureSkipVerify should be false by default")
	}
	if !catalog["skip-verify"].InsecureSkipVerify {
		t.Errorf("skip-verify InsecureSkipVerify should be true")
	}
	if catalog["skip-verify"].CACertPath != "" {
		t.Errorf("skip-verify CACertPath should default to empty")
	}
}

func TestShippedGWEConfig_ParsesWithProvidersCatalog(t *testing.T) {
	loader := &config.YAMLLoader{}
	path := filepath.Join("..", "..", "configs", "gwe", "gwe.yaml")
	t.Setenv("OIDC_ISSUER", "https://login.microsoftonline.com/common/v2.0")
	t.Setenv("OIDC_CLIENT_ID", "test-id")
	t.Setenv("OIDC_CLIENT_SECRET", "test-secret")
	cfg, err := loader.Load(t.Context(), path)
	if err != nil {
		t.Fatalf("load shipped gwe.yaml: %v", err)
	}
	catalog, err := LoadProvidersCatalog(cfg, discardLogger())
	if err != nil {
		t.Fatalf("LoadProvidersCatalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatalf("expected at least 1 provider in shipped catalog; got 0")
	}
	if _, ok := catalog["azure"]; !ok {
		t.Fatalf("expected 'azure' in shipped catalog; got %v", keysOf(catalog))
	}
}

func TestShippedCatalogs_ScopesFromEnv(t *testing.T) {
	defaults := []string{"openid", "email", "profile", "offline_access"}
	sources := []struct {
		name     string
		provider string
		load     func(t *testing.T, loader *config.YAMLLoader) (config.Config, error)
	}{
		{
			name:     "configs/gwe",
			provider: "azure",
			load: func(t *testing.T, loader *config.YAMLLoader) (config.Config, error) {
				return loader.Load(t.Context(), filepath.Join("..", "..", "configs", "gwe", "gwe.yaml"))
			},
		},
		{
			name:     "embedded default gateway",
			provider: "default",
			load: func(t *testing.T, loader *config.YAMLLoader) (config.Config, error) {
				return loader.LoadBytes(t.Context(), settings.DefaultConfigYAML()["default_gateway.yaml"])
			},
		},
	}
	cases := []struct {
		name       string
		scopesEnv  *string
		wantScopes []string
	}{
		{name: "unset uses defaults", wantScopes: defaults},
		{
			name:       "space separated adds a groups scope",
			scopesEnv:  new("openid email profile offline_access groups"),
			wantScopes: []string{"openid", "email", "profile", "offline_access", "groups"},
		},
		{name: "set empty uses defaults", scopesEnv: new(""), wantScopes: defaults},
	}
	for _, src := range sources {
		for _, tc := range cases {
			t.Run(src.name+"/"+tc.name, func(t *testing.T) {
				env := map[string]string{
					"OIDC_ISSUER":        "https://idp.example.com/",
					"OIDC_CLIENT_ID":     "test-id",
					"OIDC_CLIENT_SECRET": "test-secret",
				}
				if tc.scopesEnv != nil {
					env["OIDC_SCOPES"] = *tc.scopesEnv
				}
				loader := &config.YAMLLoader{Lookup: func(key string) (string, bool) {
					v, ok := env[key]
					return v, ok
				}}
				cfg, err := src.load(t, loader)
				if err != nil {
					t.Fatalf("load %s: %v", src.name, err)
				}
				logger, records := captureLogger(t)
				catalog, err := LoadProvidersCatalog(cfg, logger)
				if err != nil {
					t.Fatalf("LoadProvidersCatalog: %v", err)
				}
				entry, ok := catalog[src.provider]
				if !ok {
					t.Fatalf("catalog missing %q; keys: %v", src.provider, keysOf(catalog))
				}
				if !stringSlicesEqual(entry.Scopes, tc.wantScopes) {
					t.Errorf("%s.Scopes = %v, want %v", src.provider, entry.Scopes, tc.wantScopes)
				}
				if warns := warnMessages(*records); len(warns) != 0 {
					t.Errorf("unexpected WARNs: %v", warns)
				}
			})
		}
	}
}

func warnMessages(records []slog.Record) []string {
	var out []string
	for _, r := range records {
		if r.Level == slog.LevelWarn {
			out = append(out, r.Message)
		}
	}
	return out
}

const (
	omitsOpenIDWarn = "providers catalog entry scopes omit openid; login may fail at userinfo or lose id_token claims such as groups"
	emptyScopesWarn = "providers catalog entry scopes is empty after filtering; using defaults"
)

func TestLoadProvidersCatalog_ScopesWarnings(t *testing.T) {
	defaults := []string{"openid", "email", "profile", "offline_access"}
	cases := []struct {
		name       string
		scopes     string
		wantScopes []string
		wantWarn   string
	}{
		{
			name:       "blank string uses defaults",
			scopes:     `"  "`,
			wantScopes: defaults,
			wantWarn:   emptyScopesWarn,
		},
		{
			name:       "list element holding several scopes is split",
			scopes:     `["openid email", "groups"]`,
			wantScopes: []string{"openid", "email", "groups"},
		},
		{
			name:       "empty list element uses defaults",
			scopes:     `[""]`,
			wantScopes: defaults,
			wantWarn:   emptyScopesWarn,
		},
		{
			name:       "list keeps a scope token containing a comma",
			scopes:     `["openid", "api:read,write"]`,
			wantScopes: []string{"openid", "api:read,write"},
		},
		{
			name:       "string without openid",
			scopes:     `"email profile groups"`,
			wantScopes: []string{"email", "profile", "groups"},
			wantWarn:   omitsOpenIDWarn,
		},
		{
			name:       "list without openid",
			scopes:     `["groups"]`,
			wantScopes: []string{"groups"},
			wantWarn:   omitsOpenIDWarn,
		},
		{
			name:       "empty list uses defaults",
			scopes:     `[]`,
			wantScopes: defaults,
			wantWarn:   emptyScopesWarn,
		},
		{
			name:       "non-string elements only uses defaults",
			scopes:     `[1, 2]`,
			wantScopes: defaults,
			wantWarn:   emptyScopesWarn,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadYAML(t, `
providers:
  azure:
    issuer: "https://azure.example.com/"
    client_id: "az-id"
    scopes: `+tc.scopes+`
`)
			logger, records := captureLogger(t)
			catalog, err := LoadProvidersCatalog(cfg, logger)
			if err != nil {
				t.Fatalf("LoadProvidersCatalog: %v", err)
			}
			if got := catalog["azure"].Scopes; !stringSlicesEqual(got, tc.wantScopes) {
				t.Errorf("azure.Scopes = %v, want %v", got, tc.wantScopes)
			}
			warns := warnMessages(*records)
			switch {
			case tc.wantWarn == "" && len(warns) != 0:
				t.Errorf("unexpected WARNs: %v", warns)
			case tc.wantWarn != "" && !slices.Contains(warns, tc.wantWarn):
				t.Errorf("WARNs = %v, want one with message %q", warns, tc.wantWarn)
			}
		})
	}
}

func TestLoadProvidersCatalog_ScopesString(t *testing.T) {
	cases := []struct {
		name       string
		scopes     string
		wantScopes []string
	}{
		{
			name:       "space separated",
			scopes:     `"openid email profile groups"`,
			wantScopes: []string{"openid", "email", "profile", "groups"},
		},
		{
			name:       "comma separated",
			scopes:     `"openid,email,groups"`,
			wantScopes: []string{"openid", "email", "groups"},
		},
		{
			name:       "mixed separators and extra whitespace",
			scopes:     `"  openid,  email\tgroups ,"`,
			wantScopes: []string{"openid", "email", "groups"},
		},
		{
			name:       "blank string uses defaults",
			scopes:     `"  "`,
			wantScopes: []string{"openid", "email", "profile", "offline_access"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadYAML(t, `
providers:
  azure:
    issuer: "https://azure.example.com/"
    client_id: "az-id"
    scopes: `+tc.scopes+`
`)
			catalog, err := LoadProvidersCatalog(cfg, discardLogger())
			if err != nil {
				t.Fatalf("LoadProvidersCatalog: %v", err)
			}
			if got := catalog["azure"].Scopes; !stringSlicesEqual(got, tc.wantScopes) {
				t.Errorf("azure.Scopes = %v, want %v", got, tc.wantScopes)
			}
		})
	}
}

func TestLoadProvidersCatalog_WARN_OnMalformedScopes(t *testing.T) {
	yaml := `
providers:
  azure:
    issuer: "https://azure.example.com/"
    client_id: "az-id"
    client_secret: "az-secret"
    scopes: 42
`
	cfg := loadYAML(t, yaml)

	logger, records := captureLogger(t)
	catalog, err := LoadProvidersCatalog(cfg, logger)
	if err != nil {
		t.Fatalf("LoadProvidersCatalog: %v", err)
	}

	azure, ok := catalog["azure"]
	if !ok {
		t.Fatalf("catalog missing 'azure'; keys: %v", keysOf(catalog))
	}
	wantDefaults := []string{"openid", "email", "profile", "offline_access"}
	if !stringSlicesEqual(azure.Scopes, wantDefaults) {
		t.Errorf("azure.Scopes = %v, want defaults %v", azure.Scopes, wantDefaults)
	}

	const wantMsg = "providers catalog entry has malformed scopes; using defaults"
	var found bool
	for _, r := range *records {
		if r.Level != slog.LevelWarn || r.Message != wantMsg {
			continue
		}
		found = true
		var nameOK, reasonOK bool
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "providerName":
				if got := a.Value.String(); got != "azure" {
					t.Errorf("providerName = %q, want %q", got, "azure")
				}
				nameOK = true
			case "reason":
				if got := a.Value.String(); got != "scopes_not_a_list_or_string" {
					t.Errorf("reason = %q, want %q", got, "scopes_not_a_list_or_string")
				}
				reasonOK = true
			}
			return true
		})
		if !nameOK {
			t.Error("WARN record missing providerName attribute")
		}
		if !reasonOK {
			t.Error("WARN record missing reason attribute")
		}
	}
	if !found {
		t.Fatalf("no WARN record with message %q; got %d records", wantMsg, len(*records))
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
