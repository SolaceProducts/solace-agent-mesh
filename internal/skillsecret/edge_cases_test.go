// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func expandWith(t *testing.T, doc string, vars map[string]string) string {
	t.Helper()
	out, _ := config.ExpandVarsCollect(doc, func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	})
	return out
}

func decodeFrontMatter(t *testing.T, doc string) map[string]any {
	t.Helper()
	s, ok := splitFrontMatter([]byte(doc))
	if !ok {
		t.Fatal("no front matter")
	}
	var m map[string]any
	if err := yaml.Unmarshal(s.yaml, &m); err != nil {
		t.Fatalf("expanded front matter does not parse: %v\n%s", err, doc)
	}
	return m
}

func TestPlaceholders_AreSingleQuotedAndExpandToTheOriginalValue(t *testing.T) {
	values := map[string]string{
		"VAR_RETRIES": "3",
		"VAR_FLAG":    "true",
		"VAR_SPACED":  "p@ss #word",
		"VAR_OCTAL":   "0123",
		"VAR_AT":      "@tok",
		"VAR_STAR":    "*abc",
		"VAR_COLON":   "a: b",
		"VAR_NEWLINE": `line\nbreak`,
		"VAR_FILE":    `C:\path\d`,
		"VAR_DQUOTE":  `a"b`,
	}
	var original strings.Builder
	original.WriteString("---\nname: web\ntools:\n  - tool_name: srv\n    tool_type: mcp\n    environment_variables:\n")
	for k, v := range values {
		original.WriteString("      " + k + ": " + yamlQuote(t, v) + "\n")
	}
	original.WriteString("---\nbody\n")

	redacted, paths := mustRedact(t, original.String())
	if len(paths) != len(values) {
		t.Fatalf("redacted %d of %d values", len(paths), len(values))
	}
	withPlaceholders, subs, err := Placeholders([]byte(redacted), "web")
	if err != nil || len(subs) != len(values) {
		t.Fatalf("subs=%v err=%v", subs, err)
	}

	vars := map[string]string{}
	for _, sub := range subs {
		leaf := sub.FieldPath[strings.LastIndex(sub.FieldPath, ".")+1:]
		vars[strings.Trim(sub.Placeholder, "${}")] = values[leaf]
	}
	expanded := decodeFrontMatter(t, expandWith(t, string(withPlaceholders), vars))
	env := expanded["tools"].([]any)[0].(map[string]any)["environment_variables"].(map[string]any)
	for k, want := range values {
		if got, ok := env[k].(string); !ok || got != want {
			t.Errorf("%s = %#v, want string %q", k, env[k], want)
		}
	}
}

func yamlQuote(t *testing.T, v string) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestPlaceholders_KnownLimitationSingleQuoteInValue(t *testing.T) {
	doc := "---\nname: web\ntools:\n  - tool_name: srv\n    environment_variables:\n      VAR_Q: <REDACTED>\n---\n"
	withPlaceholders, subs, err := Placeholders([]byte(doc), "web")
	if err != nil || len(subs) != 1 {
		t.Fatalf("subs=%v err=%v", subs, err)
	}
	expanded := expandWith(t, string(withPlaceholders), map[string]string{strings.Trim(subs[0].Placeholder, "${}"): `it's`})
	s, _ := splitFrontMatter([]byte(expanded))
	var m map[string]any
	if err := yaml.Unmarshal(s.yaml, &m); err == nil {
		t.Fatal("known limitation changed: a value containing a single quote used to break the expanded front matter")
	}
}

func TestRedact_NumericCredentials(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_type: mcp
    auth:
      token: 123456789012345
    headers:
      X-Api-Key: 987654321
    tool_config:
      api_key: 12345678
      max_token: 512
      token_limit: 4096
      port: 8080
      timeout: 30
      max_tokens: 100
    environment_variables:
      ACCOUNT_KEY: 987654321
      MAX_RETRIES: 3
    connection_params:
      args: [--password, 123456, --port, 8080]
`)
	want := []string{
		"tools[t].auth.token",
		"tools[t].headers.X-Api-Key",
		"tools[t].tool_config.api_key",
		"tools[t].environment_variables.ACCOUNT_KEY",
		"tools[t].connection_params.args[1]",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ReferencesAreWholeValueOnly(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      password: ${VAR}
      secret: ${VAR}
      token: ${VAR:-}
      region: ${VAR, def}
      api_key: vault://kv/x#k
      auth_header: Bearer ${TOKEN}
      dsn_ok: Server=db;Password=${DBPW}
      dsn_literal: Server=db;Password=hunter2;Database=${DB_NAME}
      url_literal: https://admin:hunter2@api.example.com/${TENANT}
      malformed_one: "p${w"
      password_two: "p${w"
    environment_variables:
      DB_PASSWORD: "abc${x"
      FROM_REF: ${REF}
`)
	want := []string{
		"tools[t].tool_config.dsn_literal",
		"tools[t].tool_config.url_literal",
		"tools[t].tool_config.password_two",
		"tools[t].environment_variables.DB_PASSWORD",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ValueShapesUnderExemptKeys(t *testing.T) {
	const pem = "-----BEGIN PRIVATE KEY-----\\nMIIFAKE\\n-----END PRIVATE KEY-----"
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      service_account_credentials: '{"type":"service_account","private_key":"`+pem+`"}'
      ca_note: "`+pem+`"
      session: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWtlIn0.c2lnbmF0dXJlRkFLRQ
      config_blob: '{"timeout": 5}'
    environment_variables:
      GOOGLE_CREDENTIALS: '{"type":"service_account","client_secret":"FAKE"}'
      GOOGLE_APPLICATION_CREDENTIALS: /etc/gcp.json
`)
	want := []string{
		"tools[t].tool_config.service_account_credentials",
		"tools[t].tool_config.ca_note",
		"tools[t].tool_config.session",
		"tools[t].environment_variables.GOOGLE_CREDENTIALS",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_TokenLikeUsernameInURL(t *testing.T) {
	const (
		fakeGitHubToken = "ghp_" + "FAKETOKEN1234567890abcd"
		fakeStripeKey   = "sk_live_" + "FAKE1234567890abcdef"
	)
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      a: https://`+fakeGitHubToken+`@github.com/x
      b: https://`+fakeStripeKey+`@api.example.com
      c: https://AKIAFAKEFAKEFAKE1234@s3.example.com
      d: https://abcdefghij0123456789xyz@sentry.example.com/1
      e: https://user@example.com/spec
      f: https://git@example.com/repo
      g: https://admin@example.com/x
      h: https://first.last%40corp.com@example.com/x
`)
	want := []string{"tools[t].tool_config.a", "tools[t].tool_config.b", "tools[t].tool_config.c", "tools[t].tool_config.d"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ArgsEnvAndHeaderForms(t *testing.T) {
	in := `---
name: s
tools:
  - tool_name: srv
    tool_type: mcp
    connection_params:
      args:
        - -e
        - GITHUB_PERSONAL_ACCESS_TOKEN=ghp_fake
        - --env
        - API_KEY=abc
        - -e
        - LOG_LEVEL=debug
        - --header
        - "Authorization: Bearer fakeval"
        - --header
        - "X-API-Key: abc"
        - --header
        - "Content-Type: application/json"
        - --api-key=abc123
---
`
	out, paths := mustRedact(t, in)
	if len(paths) != 5 {
		t.Fatalf("redacted = %v\n%s", paths, out)
	}
	for _, want := range []string{
		`"GITHUB_PERSONAL_ACCESS_TOKEN=<REDACTED>"`, `"API_KEY=<REDACTED>"`,
		`"Authorization: <REDACTED>"`, `"X-API-Key: <REDACTED>"`, `"--api-key=<REDACTED>"`,
		"LOG_LEVEL=debug", "Content-Type: application/json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	for _, leak := range []string{"ghp_fake", "abc123", "fakeval", "API_KEY=abc"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q:\n%s", leak, out)
		}
	}
}

func TestPrefixPreservingSentinel_RoundTripAndPlaceholders(t *testing.T) {
	stored := "---\nname: s\ntools:\n  - tool_name: srv\n    tool_type: mcp\n    connection_params:\n      args:\n        - --api-key=abc123\n---\n"
	redacted, _ := mustRedact(t, stored)
	if !strings.Contains(redacted, `"--api-key=<REDACTED>"`) {
		t.Fatalf("redacted:\n%s", redacted)
	}

	restored, unresolved, err := Restore([]byte(redacted), []byte(stored))
	if err != nil || len(unresolved) != 0 || !strings.Contains(string(restored), "--api-key=abc123") {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, restored)
	}

	other := strings.Replace(stored, "--api-key=abc123", "--other=abc", 1)
	_, unresolved, err = Restore([]byte(redacted), []byte(other))
	if err != nil || len(unresolved) != 1 {
		t.Fatalf("a different stored flag must not restore: unresolved=%v err=%v", unresolved, err)
	}

	withPlaceholders, subs, err := Placeholders([]byte(redacted), "my-skill")
	if err != nil || len(subs) != 1 {
		t.Fatalf("subs=%v err=%v", subs, err)
	}
	if subs[0].Placeholder != "${SKILL_MY_SKILL_SRV_API_KEY}" || !strings.Contains(string(withPlaceholders), `'--api-key=${SKILL_MY_SKILL_SRV_API_KEY}'`) {
		t.Fatalf("subs=%v\n%s", subs, withPlaceholders)
	}
	expanded := expandWith(t, string(withPlaceholders), map[string]string{"SKILL_MY_SKILL_SRV_API_KEY": "abc123"})
	args := decodeFrontMatter(t, expanded)["tools"].([]any)[0].(map[string]any)["connection_params"].(map[string]any)["args"].([]any)
	if args[0] != "--api-key=abc123" || !strings.Contains(string(withPlaceholders), `'--api-key=${SKILL_MY_SKILL_SRV_API_KEY}'`) {
		t.Fatalf("args = %v", args)
	}
}

func TestRestore_PrefixedSentinelCannotConfirmGuesses(t *testing.T) {
	stored := "---\nname: s\ntools:\n  - tool_name: db\n    tool_type: mcp\n    tool_config:\n      dsn: Server=h;User=sa;Password=hunter2;Db=x\n      signing: Zm9vYmFy==\n---\n"
	for name, guess := range map[string]string{
		"right guess": "Server=h;User=sa;Password=hunter2;Db=<REDACTED>",
		"wrong guess": "Server=h;User=sa;Password=wrong;Db=<REDACTED>",
		"base64 tail": "Zm9vYmFy==<REDACTED>",
	} {
		t.Run(name, func(t *testing.T) {
			incoming := "---\nname: s\ntools:\n  - tool_name: db\n    tool_type: mcp\n    tool_config:\n      dsn: \"" + guess + "\"\n      signing: Zm9vYmFy==\n---\n"
			if name == "base64 tail" {
				incoming = strings.Replace(stored, "signing: Zm9vYmFy==", `signing: "`+guess+`"`, 1)
			}
			out, unresolved, err := Restore([]byte(incoming), []byte(stored))
			if err != nil || string(out) != incoming {
				t.Fatalf("a guess must never restore (it is ordinary text outside args): unresolved=%v err=%v\n%s", unresolved, err, out)
			}
		})
	}
}

func TestRestore_SentinelOnlyWhereRedactWouldEmitIt(t *testing.T) {
	stored := "---\nname: s\ntools:\n  - tool_name: a\n    tool_config:\n      note: plain text\n  - tool_name: b\n    tool_config:\n      api_key: FAKE\n---\n"
	incoming := "---\nname: s\ntools:\n  - tool_name: a\n    tool_config:\n      note: <REDACTED>\n  - tool_name: b\n    tool_config:\n      api_key: <REDACTED>\n---\n"
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	if err != nil || !slices.Equal(unresolved, []string{"tools[a].tool_config.note"}) || strings.Contains(string(out), "plain text") {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, out)
	}
	if !strings.Contains(string(out), "api_key: FAKE") {
		t.Fatalf("a redacted secret must still restore:\n%s", out)
	}
}

func TestRedact_StoredLiteralEndingInSentinelIsStillRedacted(t *testing.T) {
	in := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      password: 'SEKRET8=<REDACTED>'\n---\n"
	out, redacted := mustRedact(t, in)
	if len(redacted) != 1 || strings.Contains(out, "SEKRET8") {
		t.Fatalf("redacted=%v\n%s", redacted, out)
	}
}

func TestRedact_ExpiryAndVersionNumbersAreNotCredentials(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      token_ttl: 3600
      secret_version: 3
      token_expiry_seconds: 900
      api_key_version: 2
      password_expiry_days: 90
      token_refresh_interval: 60
      api_key: 12345678
    auth:
      expires_in: 3600
      token: 123456789012345
`)
	want := []string{"tools[t].tool_config.api_key", "tools[t].auth.token"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_WrappedArgFormsKeepTheirPrefix(t *testing.T) {
	in := `---
name: s
tools:
  - tool_name: srv
    tool_type: mcp
    connection_params:
      args:
        - "--header=Authorization: Bearer faketok"
        - --env=GITHUB_TOKEN=faketok
        - --env=LOG_LEVEL=debug
---
`
	out, paths := mustRedact(t, in)
	if len(paths) != 2 || strings.Contains(out, "faketok") ||
		!strings.Contains(out, `"--header=Authorization: <REDACTED>"`) || !strings.Contains(out, `"--env=GITHUB_TOKEN=<REDACTED>"`) ||
		!strings.Contains(out, "--env=LOG_LEVEL=debug") {
		t.Fatalf("paths=%v\n%s", paths, out)
	}
	restored, unresolved, err := Restore([]byte(out), []byte(in))
	if err != nil || len(unresolved) != 0 || !strings.Contains(string(restored), "faketok") {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, restored)
	}
	withPlaceholders, subs, err := Placeholders([]byte(out), "s")
	if err != nil || len(subs) != 2 || strings.Contains(string(withPlaceholders), "<REDACTED>") {
		t.Fatalf("subs=%v err=%v\n%s", subs, err, withPlaceholders)
	}
}

func TestRedact_CredentialsKeyExemptOnlyForPathValues(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      service_credentials: aQ5opaqueSecretValue
      fallback_credentials: "${SVC:-aQ5opaqueSecretValue}"
      key_file_credentials: "${GAC:-/etc/gcp.json}"
    environment_variables:
      REGISTRY_CREDENTIALS: user:pass
      PIN_CREDENTIALS: 98765432
      GOOGLE_APPLICATION_CREDENTIALS: /etc/gcp.json
      RELATIVE_CREDENTIALS: ./creds/gcp.json
      PARENT_CREDENTIALS: ../gcp.json
      HOME_CREDENTIALS: ~/gcp.json
      WINDOWS_CREDENTIALS: 'C:\keys\gcp.json'
`)
	want := []string{
		"tools[t].tool_config.service_credentials",
		"tools[t].tool_config.fallback_credentials",
		"tools[t].environment_variables.REGISTRY_CREDENTIALS",
		"tools[t].environment_variables.PIN_CREDENTIALS",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}
