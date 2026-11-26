// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

func identityTags() []string {
	fields := reflect.TypeFor[connectionIdentity]()
	tags := make([]string, 0, fields.NumField())
	for i := range fields.NumField() {
		tags = append(tags, fields.Field(i).Tag.Get("conn"))
	}
	return tags
}

func baseConnectionConfig(t *testing.T) *ToolsetConfig {
	t.Helper()
	t.Setenv(EnvMCPConnectorTLSVerify, "")
	return &ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "http://h/mcp", ClientID: "c"}
}

func ptr[T any](v T) *T { return &v }

func TestConnectionDifferencesReportsEachSettingAlone(t *testing.T) {
	withAuth := func(c *ToolsetConfig, edit func(*toolauth.AuthConfig)) {
		c.AuthConfig = &toolauth.AuthConfig{Type: "oauth2"}
		edit(c.AuthConfig)
	}
	cases := []struct {
		name   string
		change func(c *ToolsetConfig)
		want   string
	}{
		{"type", func(c *ToolsetConfig) { c.ConnectionType = connTypeSSE }, "type"},
		{"url", func(c *ToolsetConfig) { c.URL = "http://other/mcp" }, "url"},
		{"command", func(c *ToolsetConfig) { c.Command = "/opt/npx" }, "command"},
		{"args", func(c *ToolsetConfig) { c.CommandArgs = []string{"-y", "b"} }, "args"},
		{"env", func(c *ToolsetConfig) { c.EnvVars = map[string]string{"A": "b"} }, "env"},
		{"sse read timeout", func(c *ToolsetConfig) { c.SSEReadTimeout = 5 }, "sse_read_timeout"},
		{"sse read timeout uncapped", func(c *ToolsetConfig) { c.SSEReadTimeout = -1 }, "sse_read_timeout"},
		{"max response bytes", func(c *ToolsetConfig) { c.MaxResponseBytes = 10 }, "max_response_bytes"},
		{"max response bytes uncapped", func(c *ToolsetConfig) { c.MaxResponseBytes = -1 }, "max_response_bytes"},
		{"terminate on close", func(c *ToolsetConfig) { c.TerminateOnClose = ptr(false) }, "terminate_on_close"},
		{"ssl verify", func(c *ToolsetConfig) { c.SSLVerify = ptr(false) }, "ssl_config.verify"},
		{"ssl ca bundle", func(c *ToolsetConfig) { c.SSLCABundle = "/ca.pem" }, "ssl_config.ca_bundle"},
		{"ssl client cert", func(c *ToolsetConfig) { c.SSLClientCert = "/c.pem" }, "ssl_config.client_cert"},
		{"ssl client key", func(c *ToolsetConfig) { c.SSLClientKey = "/k.pem" }, "ssl_config.client_key"},
		{"ssl server name", func(c *ToolsetConfig) { c.SSLServerName = "sni" }, "ssl_config.server_name"},
		{"headers", func(c *ToolsetConfig) { c.Headers = map[string]string{"X": "y"} }, "headers"},
		{"manifest", func(c *ToolsetConfig) { c.Manifest = []ManifestTool{{Name: "t"}} }, "manifest"},
		{"auth block present", func(c *ToolsetConfig) { withAuth(c, func(*toolauth.AuthConfig) {}) }, "auth"},
		{"auth entry type", func(c *ToolsetConfig) { c.AuthType = "oauth2" }, "auth"},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := baseConnectionConfig(t)
			tc.change(changed)
			if got := connectionDifferences(baseConnectionConfig(t), changed); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("differences = %v, want only [%s]", got, tc.want)
			}
			if got := connectionDifferences(changed, baseConnectionConfig(t)); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("reversed differences = %v, want only [%s]", got, tc.want)
			}
		})
		covered[tc.want] = true
	}
	for _, tag := range identityTags() {
		if !covered[tag] {
			t.Errorf("no case changes %q alone: its comparison is unpinned", tag)
		}
	}
}

func authenticatedConfig(t *testing.T, edit func(*ToolsetConfig, *toolauth.AuthConfig)) *ToolsetConfig {
	t.Helper()
	cfg := baseConnectionConfig(t)
	cfg.AuthType = "oauth2"
	cfg.AuthConfig = &toolauth.AuthConfig{
		Type:          "oauth2",
		Scheme:        &toolauth.OAuthScheme{TokenURL: "http://t", Scopes: []string{"read"}},
		Credential:    &toolauth.AuthCredential{ClientID: "id"},
		CredentialKey: "key",
		ResourceURL:   "http://r",
		CACertPath:    "/ca.pem",
	}
	if edit != nil {
		edit(cfg, cfg.AuthConfig)
	}
	return cfg
}

func TestConnectionDifferencesReportsEachAuthSettingAlone(t *testing.T) {
	changes := map[string]func(*ToolsetConfig, *toolauth.AuthConfig){
		"Type": func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.Type = "bearer" },
		"Scheme": func(_ *ToolsetConfig, a *toolauth.AuthConfig) {
			a.Scheme = &toolauth.OAuthScheme{TokenURL: "http://other"}
		},
		"Scheme.Scopes": func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.Scheme.Scopes = []string{"write"} },
		"Credential": func(_ *ToolsetConfig, a *toolauth.AuthConfig) {
			a.Credential = &toolauth.AuthCredential{ClientID: "other"}
		},
		"CredentialKey":          func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.CredentialKey = "other" },
		"ResourceURL":            func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.ResourceURL = "http://other" },
		"CACertPath":             func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.CACertPath = "/other.pem" },
		"InsecureSkipVerify":     func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.InsecureSkipVerify = true },
		"DisablePKCE":            func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.DisablePKCE = true },
		"DisableConsentPrompt":   func(_ *ToolsetConfig, a *toolauth.AuthConfig) { a.DisableConsentPrompt = true },
		"ToolsetConfig.AuthType": func(c *ToolsetConfig, _ *toolauth.AuthConfig) { c.AuthType = "basic" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			got := connectionDifferences(authenticatedConfig(t, nil), authenticatedConfig(t, change))
			if !slices.Equal(got, []string{"auth"}) {
				t.Errorf("differences = %v, want only [auth]", got)
			}
		})
	}

	ignored := map[string]bool{"DisplayName": true}
	for i := range reflect.TypeFor[toolauth.AuthConfig]().NumField() {
		field := reflect.TypeFor[toolauth.AuthConfig]().Field(i).Name
		if _, ok := changes[field]; !ok && !ignored[field] {
			t.Errorf("AuthConfig.%s has no case in this table: its comparison is unpinned", field)
		}
	}
	for i := range reflect.TypeFor[toolauth.OAuthScheme]().NumField() {
		field := reflect.TypeFor[toolauth.OAuthScheme]().Field(i).Name
		if field == "Scopes" {
			continue
		}
		scheme := func(c *ToolsetConfig, a *toolauth.AuthConfig) {
			reflect.ValueOf(a.Scheme).Elem().FieldByName(field).SetString("changed")
		}
		if got := connectionDifferences(authenticatedConfig(t, nil), authenticatedConfig(t, scheme)); !slices.Equal(got, []string{"auth"}) {
			t.Errorf("OAuthScheme.%s: differences = %v, want only [auth]", field, got)
		}
	}
	for i := range reflect.TypeFor[toolauth.AuthCredential]().NumField() {
		field := reflect.TypeFor[toolauth.AuthCredential]().Field(i).Name
		credential := func(c *ToolsetConfig, a *toolauth.AuthConfig) {
			reflect.ValueOf(a.Credential).Elem().FieldByName(field).SetString("changed")
		}
		if got := connectionDifferences(authenticatedConfig(t, nil), authenticatedConfig(t, credential)); !slices.Equal(got, []string{"auth"}) {
			t.Errorf("AuthCredential.%s: differences = %v, want only [auth]", field, got)
		}
	}
}

func TestConnectionDifferencesIgnoresWhatDoesNotChangeTheConnection(t *testing.T) {
	cases := []struct {
		name   string
		change func(c *ToolsetConfig)
	}{
		{"terminate_on_close true against unset", func(c *ToolsetConfig) { c.TerminateOnClose = ptr(true) }},
		{"sse_read_timeout 300 against unset", func(c *ToolsetConfig) { c.SSEReadTimeout = MCPDefaultSSEReadTimeoutSeconds }},
		{"max_response_bytes default against unset", func(c *ToolsetConfig) { c.MaxResponseBytes = MCPDefaultMaxResponseBytes }},
		{"ssl verify true against unset", func(c *ToolsetConfig) { c.SSLVerify = ptr(true) }},
		{"empty headers against nil", func(c *ToolsetConfig) { c.Headers = map[string]string{} }},
		{"empty env against nil", func(c *ToolsetConfig) { c.EnvVars = map[string]string{} }},
		{"empty args against nil", func(c *ToolsetConfig) { c.CommandArgs = []string{} }},
		{"timeout, which nothing reads", func(c *ToolsetConfig) { c.Timeout = 99 }},
		{"tool filter, prefix, tool_config and scopes", func(c *ToolsetConfig) {
			c.ToolName, c.ToolNamePrefix, c.AllowList, c.DenyList = "x", "p_", []string{"y"}, []string{"z"}
			c.ToolConfig = map[string]any{"k": "v"}
			c.RequiredScopes = []string{"s"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := baseConnectionConfig(t)
			tc.change(changed)
			if got := connectionDifferences(baseConnectionConfig(t), changed); len(got) != 0 {
				t.Errorf("reported %v for a change that does not alter the connection", got)
			}
		})
	}
}

func TestConnectionDifferencesIgnoresEquivalentAuthSpellings(t *testing.T) {
	plain := func(edit func(*toolauth.AuthConfig)) *ToolsetConfig {
		c := baseConnectionConfig(t)
		c.AuthType = "oauth2"
		c.AuthConfig = &toolauth.AuthConfig{Type: "oauth2", CredentialKey: "k"}
		edit(c.AuthConfig)
		return c
	}
	base := plain(func(*toolauth.AuthConfig) {})
	for name, edit := range map[string]func(*toolauth.AuthConfig){
		"display name":      func(a *toolauth.AuthConfig) { a.DisplayName = "Atlassian" },
		"empty credential":  func(a *toolauth.AuthConfig) { a.Credential = &toolauth.AuthCredential{} },
		"empty scheme":      func(a *toolauth.AuthConfig) { a.Scheme = &toolauth.OAuthScheme{} },
		"empty scope slice": func(a *toolauth.AuthConfig) { a.Scheme = &toolauth.OAuthScheme{Scopes: []string{}} },
	} {
		if got := connectionDifferences(base, plain(edit)); len(got) != 0 {
			t.Errorf("%s: reported %v", name, got)
		}
	}
}

func TestConnectionIdentityClassifiesEveryField(t *testing.T) {
	const entryLevel = ""
	toolsetFields := map[string]string{
		"ConnectionType": "type", "URL": "url", "Command": "command", "CommandArgs": "args", "EnvVars": "env",
		"SSEReadTimeout": "sse_read_timeout", "MaxResponseBytes": "max_response_bytes",
		"TerminateOnClose": "terminate_on_close", "SSLVerify": "ssl_config.verify",
		"SSLCABundle": "ssl_config.ca_bundle", "SSLClientCert": "ssl_config.client_cert",
		"SSLClientKey": "ssl_config.client_key", "SSLServerName": "ssl_config.server_name",
		"Headers": "headers", "AuthType": "auth", "AuthConfig": "auth", "Manifest": "manifest",

		"Timeout":        entryLevel,
		"ToolName":       entryLevel,
		"ToolNamePrefix": entryLevel,
		"AllowList":      entryLevel,
		"DenyList":       entryLevel,
		"ToolConfig":     entryLevel,
		"RequiredScopes": entryLevel,
		"ClientID":       entryLevel,

		"TrustAnnotations": entryLevel,
	}
	authFields := map[string]string{
		"Type": "auth", "Scheme": "auth", "Credential": "auth", "CredentialKey": "auth", "ResourceURL": "auth",
		"CACertPath": "auth", "InsecureSkipVerify": "auth", "DisablePKCE": "auth", "DisableConsentPrompt": "auth",
		"DisplayName": entryLevel,
	}

	tags := identityTags()
	check := func(typ reflect.Type, classified map[string]string) {
		seen := map[string]bool{}
		for i := range typ.NumField() {
			name := typ.Field(i).Name
			seen[name] = true
			tag, ok := classified[name]
			if !ok {
				t.Errorf("%s.%s is not classified: add it to connectionIdentity (and the table above) if it shapes the connection, or list it as entry-level here", typ.Name(), name)
				continue
			}
			if tag != entryLevel && !slices.Contains(tags, tag) {
				t.Errorf("%s.%s is classified as %q, which connectionIdentity does not declare", typ.Name(), name, tag)
			}
		}
		for name := range classified {
			if !seen[name] {
				t.Errorf("%s no longer has field %s; drop it from the classification", typ.Name(), name)
			}
		}
	}
	check(reflect.TypeFor[ToolsetConfig](), toolsetFields)
	check(reflect.TypeFor[toolauth.AuthConfig](), authFields)

	for i := range reflect.TypeFor[connectionIdentity]().NumField() {
		if reflect.TypeFor[connectionIdentity]().Field(i).Tag.Get("conn") == "" {
			t.Errorf("connectionIdentity.%s has no conn tag, so a difference in it could not be named", reflect.TypeFor[connectionIdentity]().Field(i).Name)
		}
	}
}

type identityField struct {
	name     string
	index    []int
	presence bool
}

func identityFields(typ reflect.Type, prefix []int, name string) []identityField {
	var out []identityField
	for i := range typ.NumField() {
		field := typ.Field(i)
		index := append(slices.Clone(prefix), i)
		fieldName := name + "." + field.Name
		switch {
		case field.Type.Kind() == reflect.Pointer && field.Type.Elem().Kind() == reflect.Struct:
			out = append(out, identityField{name: fieldName + " (present)", index: index, presence: true})
			out = append(out, identityFields(field.Type.Elem(), index, fieldName)...)
		case field.Type.Kind() == reflect.Struct:
			out = append(out, identityFields(field.Type, index, fieldName)...)
		default:
			out = append(out, identityField{name: fieldName, index: index})
		}
	}
	return out
}

func (f identityField) setIn(t *testing.T, root reflect.Value) {
	t.Helper()
	v := root
	for _, i := range f.index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	if f.presence {
		v.Set(reflect.New(v.Type().Elem()))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Slice:
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), reflect.ValueOf("x")))
	case reflect.Map:
		v.Set(reflect.ValueOf(map[string]string{"k": "v"}))
	default:
		t.Fatalf("%s has kind %s, which this test cannot populate", f.name, v.Kind())
	}
}

func TestConnectionIdentityDigestCoversEveryField(t *testing.T) {
	baseDigest := mustDigest(t, connectionIdentity{})
	fields := identityFields(reflect.TypeFor[connectionIdentity](), nil, "")
	if len(fields) < 20 {
		t.Fatalf("found only %d identity fields; the walk is not reaching the auth block", len(fields))
	}
	for _, f := range fields {
		t.Run(strings.TrimPrefix(f.name, "."), func(t *testing.T) {
			var id connectionIdentity
			f.setIn(t, reflect.ValueOf(&id).Elem())
			if mustDigest(t, id) == baseDigest {
				t.Errorf("%s does not change the digest: entries differing in it would share a client", f.name)
			}
		})
	}
}
