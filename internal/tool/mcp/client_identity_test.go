// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"regexp"
	"strings"
	"testing"
)

type rawEntry = map[string]any

func httpEntry() rawEntry {
	return rawEntry{
		"tool_type": "mcp",
		"connection_params": rawEntry{
			"type":               "streamable-http",
			"url":                "https://mcp.example.com/mcp",
			"headers":            rawEntry{"X-Tenant": "tenant-a"},
			"sse_read_timeout":   10,
			"max_response_bytes": 1000,
			"terminate_on_close": false,
			"ssl_config": rawEntry{
				"verify":      true,
				"ca_bundle":   "/etc/ca.pem",
				"client_cert": "/etc/client.pem",
				"client_key":  "/etc/client.key",
				"server_name": "mcp.internal",
			},
		},
		"auth": rawEntry{
			"type": "oauth2",
			"scheme": rawEntry{
				"authorization_url":          "https://idp.example.com/authorize",
				"token_url":                  "https://idp.example.com/token",
				"refresh_url":                "https://idp.example.com/refresh",
				"scopes":                     []any{"read"},
				"token_endpoint_auth_method": "client_secret_post",
				"audience":                   "aud",
				"resource":                   "res",
			},
			"credential": rawEntry{
				"client_id":     "id",
				"client_secret": "secret",
				"username":      "user",
				"password":      "pass",
				"token":         "tok",
			},
			"credential_key":       "key",
			"ca_cert_path":         "/etc/idp-ca.pem",
			"insecure_skip_verify": false,
			"display_name":         "Example",
		},
		"manifest": []any{rawEntry{"name": "t", "description": "d"}},
	}
}

func stdioEntry() rawEntry {
	return rawEntry{
		"tool_type": "mcp",
		"connection_params": rawEntry{
			"type":    "stdio",
			"command": "/usr/bin/npx",
			"args":    []any{"-y", "server-a"},
			"env":     rawEntry{"API_KEY": "key-a"},
		},
	}
}

func connParams(e rawEntry) rawEntry  { return e["connection_params"].(rawEntry) }
func authBlock(e rawEntry) rawEntry   { return e["auth"].(rawEntry) }
func schemeBlock(e rawEntry) rawEntry { return authBlock(e)["scheme"].(rawEntry) }
func credBlock(e rawEntry) rawEntry   { return authBlock(e)["credential"].(rawEntry) }
func sslBlock(e rawEntry) rawEntry    { return connParams(e)["ssl_config"].(rawEntry) }

func clientIDOf(t *testing.T, e rawEntry) string {
	t.Helper()
	cfg, err := ParseToolsetConfig(e)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	return cfg.ClientID
}

type entryChange struct {
	name   string
	change func(e rawEntry)
}

var httpConnectionChanges = []entryChange{
	{"type", func(e rawEntry) { connParams(e)["type"] = "sse" }},
	{"url scheme", func(e rawEntry) { connParams(e)["url"] = "http://mcp.example.com/mcp" }},
	{"url host", func(e rawEntry) { connParams(e)["url"] = "https://other.example.com/mcp" }},
	{"url port", func(e rawEntry) { connParams(e)["url"] = "https://mcp.example.com:8443/mcp" }},
	{"url path", func(e rawEntry) { connParams(e)["url"] = "https://mcp.example.com/other" }},
	{"url path separator", func(e rawEntry) { connParams(e)["url"] = "https://mcp.example.com/m/cp" }},
	{"url path punctuation", func(e rawEntry) { connParams(e)["url"] = "https://mcp.example.com/m-cp" }},
	{"url query", func(e rawEntry) { connParams(e)["url"] = "https://mcp.example.com/mcp?k=1" }},
	{"url userinfo", func(e rawEntry) { connParams(e)["url"] = "https://u:p@mcp.example.com/mcp" }},
	{"header value", func(e rawEntry) { connParams(e)["headers"] = rawEntry{"X-Tenant": "tenant-b"} }},
	{"header name", func(e rawEntry) { connParams(e)["headers"] = rawEntry{"X-Other": "tenant-a"} }},
	{"header added", func(e rawEntry) { connParams(e)["headers"].(rawEntry)["X-Extra"] = "1" }},
	{"header removed", func(e rawEntry) { delete(connParams(e), "headers") }},
	{"basic auth in connection params", func(e rawEntry) {
		connParams(e)["auth"] = rawEntry{"type": "basic", "username": "u", "password": "p"}
	}},
	{"sse read timeout", func(e rawEntry) { connParams(e)["sse_read_timeout"] = 20 }},
	{"max response bytes", func(e rawEntry) { connParams(e)["max_response_bytes"] = 2000 }},
	{"terminate on close", func(e rawEntry) { connParams(e)["terminate_on_close"] = true }},
	{"ssl verify", func(e rawEntry) { sslBlock(e)["verify"] = false }},
	{"ssl ca bundle", func(e rawEntry) { sslBlock(e)["ca_bundle"] = "/etc/other-ca.pem" }},
	{"ssl client cert", func(e rawEntry) { sslBlock(e)["client_cert"] = "/etc/other.pem" }},
	{"ssl client key", func(e rawEntry) { sslBlock(e)["client_key"] = "/etc/other.key" }},
	{"ssl server name", func(e rawEntry) { sslBlock(e)["server_name"] = "other.internal" }},
	{"auth removed", func(e rawEntry) { delete(e, "auth") }},
	{"auth type", func(e rawEntry) { authBlock(e)["type"] = "bearer" }},
	{"auth credential key", func(e rawEntry) { authBlock(e)["credential_key"] = "other" }},
	{"auth ca cert path", func(e rawEntry) { authBlock(e)["ca_cert_path"] = "/etc/other-idp-ca.pem" }},
	{"auth insecure skip verify", func(e rawEntry) { authBlock(e)["insecure_skip_verify"] = true }},
	{"auth authorization url", func(e rawEntry) { schemeBlock(e)["authorization_url"] = "https://idp.example.com/other" }},
	{"auth token url", func(e rawEntry) { schemeBlock(e)["token_url"] = "https://idp.example.com/other" }},
	{"auth refresh url", func(e rawEntry) { schemeBlock(e)["refresh_url"] = "https://idp.example.com/other" }},
	{"auth scopes", func(e rawEntry) { schemeBlock(e)["scopes"] = []any{"read", "write"} }},
	{"auth token endpoint auth method", func(e rawEntry) { schemeBlock(e)["token_endpoint_auth_method"] = "none" }},
	{"auth audience", func(e rawEntry) { schemeBlock(e)["audience"] = "other" }},
	{"auth resource", func(e rawEntry) { schemeBlock(e)["resource"] = "other" }},
	{"auth disable pkce", func(e rawEntry) { schemeBlock(e)["disable_pkce"] = true }},
	{"auth client id", func(e rawEntry) { credBlock(e)["client_id"] = "other" }},
	{"auth client secret", func(e rawEntry) { credBlock(e)["client_secret"] = "other" }},
	{"auth username", func(e rawEntry) { credBlock(e)["username"] = "other" }},
	{"auth password", func(e rawEntry) { credBlock(e)["password"] = "other" }},
	{"auth token", func(e rawEntry) { credBlock(e)["token"] = "other" }},
	{"manifest against live", func(e rawEntry) { delete(e, "manifest") }},
}

var stdioConnectionChanges = []entryChange{
	{"command", func(e rawEntry) { connParams(e)["command"] = "/usr/bin/uvx" }},
	{"command directory, same base name", func(e rawEntry) { connParams(e)["command"] = "/opt/tools/npx" }},
	{"argument value", func(e rawEntry) { connParams(e)["args"] = []any{"-y", "server-b"} }},
	{"argument order", func(e rawEntry) { connParams(e)["args"] = []any{"server-a", "-y"} }},
	{"argument added", func(e rawEntry) { connParams(e)["args"] = []any{"-y", "server-a", "--verbose"} }},
	{"arguments removed", func(e rawEntry) { delete(connParams(e), "args") }},
	{"env value", func(e rawEntry) { connParams(e)["env"] = rawEntry{"API_KEY": "key-b"} }},
	{"env name", func(e rawEntry) { connParams(e)["env"] = rawEntry{"OTHER_KEY": "key-a"} }},
	{"env added", func(e rawEntry) { connParams(e)["env"].(rawEntry)["EXTRA"] = "1" }},
	{"env removed", func(e rawEntry) { delete(connParams(e), "env") }},
	{"top-level environment variables", func(e rawEntry) { e["environment_variables"] = rawEntry{"EXTRA": "1"} }},
}

func TestClientIDDiffersWhenAnyConnectionSettingDiffers(t *testing.T) {
	cases := []struct {
		kind    string
		base    func() rawEntry
		changes []entryChange
	}{
		{"http", httpEntry, httpConnectionChanges},
		{"stdio", stdioEntry, stdioConnectionChanges},
	}
	for _, kind := range cases {
		baseID := clientIDOf(t, kind.base())
		for _, tc := range kind.changes {
			t.Run(kind.kind+"/"+tc.name, func(t *testing.T) {
				changed := kind.base()
				tc.change(changed)
				if got := clientIDOf(t, changed); got == baseID {
					t.Errorf("changing %s left the client id at %q: two different servers would share one client", tc.name, got)
				}
			})
		}
	}
}

func TestClientIDIgnoresSettingsThatAreNotTheConnection(t *testing.T) {
	changes := []entryChange{
		{"tool_name", func(e rawEntry) { e["tool_name"] = "t" }},
		{"tool_name_prefix", func(e rawEntry) { e["tool_name_prefix"] = "p_" }},
		{"allow_list", func(e rawEntry) { e["allow_list"] = []any{"t"} }},
		{"deny_list", func(e rawEntry) { e["deny_list"] = []any{"t"} }},
		{"tool_config", func(e rawEntry) { e["tool_config"] = rawEntry{"k": "v"} }},
		{"required_scopes", func(e rawEntry) { e["required_scopes"] = []any{"s"} }},
		{"auth display name", func(e rawEntry) { authBlock(e)["display_name"] = "Other" }},
		{"timeout", func(e rawEntry) { connParams(e)["timeout"] = 99 }},
		{"manifest content", func(e rawEntry) {
			e["manifest"] = []any{rawEntry{"name": "other", "description": "x"}, rawEntry{"name": "more", "description": "y"}}
		}},
		{"header name case", func(e rawEntry) { connParams(e)["headers"] = rawEntry{"x-tenant": "tenant-a"} }},
		{"source connector id", func(e rawEntry) { e["source_connector_id"] = "connector-1" }},
	}
	baseID := clientIDOf(t, httpEntry())
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			changed := httpEntry()
			tc.change(changed)
			if got := clientIDOf(t, changed); got != baseID {
				t.Errorf("changing %s moved the client id from %q to %q: entries for one server would no longer share a client", tc.name, baseID, got)
			}
		})
	}
}

func TestClientIDSeparatesWhatTheOldDerivationCollapsed(t *testing.T) {
	url := func(u string) rawEntry {
		return rawEntry{"tool_type": "mcp", "connection_params": rawEntry{"type": "streamable-http", "url": u}}
	}
	stdio := func(command string, args ...any) rawEntry {
		return rawEntry{"tool_type": "mcp", "connection_params": rawEntry{"type": "stdio", "command": command, "args": args}}
	}
	pairs := []struct {
		name string
		a, b rawEntry
	}{
		{"npx with different packages", stdio("npx", "-y", "server-a"), stdio("npx", "-y", "server-b")},
		{"same base name in different directories", stdio("/usr/bin/server"), stdio("/opt/server")},
		{"http against https", url("http://h/mcp"), url("https://h/mcp")},
		{"hyphen against slash", url("https://h/a-b"), url("https://h/a/b")},
		{"underscore against slash", url("https://h/a_b"), url("https://h/a/b")},
		{"dot against hyphen in host", url("https://a.b/mcp"), url("https://a-b/mcp")},
		{"stdio against http", stdio("h"), url("https://h")},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			if a, b := clientIDOf(t, tc.a), clientIDOf(t, tc.b); a == b {
				t.Errorf("both entries got client id %q", a)
			}
		})
	}
}

func TestClientIDIsStableAcrossParses(t *testing.T) {
	entry := func() rawEntry {
		e := httpEntry()
		headers := rawEntry{}
		for _, name := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
			headers["X-"+name] = "v" + name
		}
		connParams(e)["headers"] = headers
		return e
	}
	first := clientIDOf(t, entry())
	for range 30 {
		if got := clientIDOf(t, entry()); got != first {
			t.Fatalf("client id changed between parses of one config: %q then %q", first, got)
		}
	}
}

var clientIDShape = regexp.MustCompile(`^mcp_[A-Za-z0-9_]+_[0-9a-f]{16}$`)

func TestClientIDCarriesNoSecretValues(t *testing.T) {
	const canary = "CANARY-sk-live-9f3a"
	sanitized := strings.ReplaceAll(canary, "-", "_")

	secretEntries := map[string]func() rawEntry{
		"url userinfo": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://svc:" + canary + "@mcp.example.com/mcp"
			return e
		},
		"url query": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://mcp.example.com/mcp?api_key=" + canary
			return e
		},
		"url path": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://mcp.example.com/" + canary + "/mcp"
			return e
		},
		"url fragment": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://mcp.example.com#" + canary
			return e
		},
		"userinfo containing a slash": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://svc:" + canary + "/x@mcp.example.com/mcp"
			return e
		},
		"userinfo containing a question mark": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://svc:" + canary + "?x@mcp.example.com:8443/mcp"
			return e
		},
		"userinfo containing a hash": func() rawEntry {
			e := httpEntry()
			connParams(e)["url"] = "https://" + canary + "#x@mcp.example.com/mcp"
			return e
		},
		"header": func() rawEntry {
			e := httpEntry()
			connParams(e)["headers"] = rawEntry{"Authorization": "Bearer " + canary}
			return e
		},
		"basic auth": func() rawEntry {
			e := httpEntry()
			connParams(e)["auth"] = rawEntry{"type": "basic", "username": "svc", "password": canary}
			return e
		},
		"oauth client secret": func() rawEntry {
			e := httpEntry()
			credBlock(e)["client_secret"] = canary
			return e
		},
		"bearer token": func() rawEntry {
			e := httpEntry()
			credBlock(e)["token"] = canary
			return e
		},
		"stdio argument": func() rawEntry {
			e := stdioEntry()
			connParams(e)["args"] = []any{"--token", canary}
			return e
		},
		"stdio env": func() rawEntry {
			e := stdioEntry()
			connParams(e)["env"] = rawEntry{"API_KEY": canary}
			return e
		},
		"stdio command path": func() rawEntry {
			e := stdioEntry()
			connParams(e)["command"] = "/opt/" + canary + "/bin/server"
			return e
		},
	}
	for name, build := range secretEntries {
		t.Run(name, func(t *testing.T) {
			id := clientIDOf(t, build())
			if !clientIDShape.MatchString(id) {
				t.Errorf("client id %q is not <label>_<16 hex digits>", id)
			}
			if strings.Contains(id, canary) || strings.Contains(id, sanitized) {
				t.Errorf("client id %q carries the secret", id)
			}
		})
	}
}

func TestClientIDNamesTheServerReadably(t *testing.T) {
	cases := []struct {
		entry func() rawEntry
		label string
	}{
		{httpEntry, "mcp_mcp_example_com_"},
		{stdioEntry, "mcp_npx_"},
	}
	for _, tc := range cases {
		if id := clientIDOf(t, tc.entry()); !strings.HasPrefix(id, tc.label) {
			t.Errorf("client id %q does not start with %q: a log line would not say which server it is about", id, tc.label)
		}
	}
}
