// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const fakeGoogleKey = "AIza" + "FAKEKEY0000000000000000000000000000"

const webSkill = `---
name: web
description: Search the web
tools:
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: "` + fakeGoogleKey + `"
      google_cse_id: "e11bfake0000"
  - tool_type: builtin
    tool_name: web_search_other
    tool_config:
      api_key: fake-other-key
---
# Web

Body text with <REDACTED> and trailing spaces  
`

func parseFM(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	s, ok := splitFrontMatter(doc)
	if !ok {
		t.Fatalf("no front matter")
	}
	var m map[string]any
	if err := yaml.Unmarshal(s.yaml, &m); err != nil {
		t.Fatalf("unmarshal front matter: %v", err)
	}
	return m
}

func bodyOf(t *testing.T, doc []byte) []byte {
	t.Helper()
	s, ok := splitFrontMatter(doc)
	if !ok {
		t.Fatalf("no front matter")
	}
	return s.tail
}

func mustRedact(t *testing.T, in string) (string, []string) {
	t.Helper()
	out, redacted, err := Redact([]byte(in))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	return string(out), redacted
}

func TestRedact_WebSkill(t *testing.T) {
	out, redacted := mustRedact(t, webSkill)

	wantPaths := []string{
		"tools[web_search_google].tool_config.google_search_api_key",
		"tools[web_search_other].tool_config.api_key",
	}
	if !slices.Equal(redacted, wantPaths) {
		t.Fatalf("redacted = %v, want %v", redacted, wantPaths)
	}
	if strings.Contains(out, fakeGoogleKey) || strings.Contains(out, "fake-other-key") {
		t.Fatalf("secret leaked in output:\n%s", out)
	}
	if !strings.Contains(out, "google_cse_id: e11bfake0000") && !strings.Contains(out, `google_cse_id: "e11bfake0000"`) {
		t.Fatalf("google_cse_id was altered:\n%s", out)
	}
	if !bytes.Equal(bodyOf(t, []byte(out)), bodyOf(t, []byte(webSkill))) {
		t.Fatalf("body changed")
	}
	if !strings.HasSuffix(out, "# Web\n\nBody text with <REDACTED> and trailing spaces  \n") {
		t.Fatalf("body bytes not preserved:\n%q", out)
	}
}

func TestRedact_Table(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
		keep []string
	}{
		{
			name: "dollar var and vault reference kept",
			yaml: `tools:
  - tool_name: t
    tool_type: builtin
    tool_config:
      api_key: ${KEY}
      password: vault://kv/data/x#pw
`,
			keep: []string{"${KEY}", "vault://kv/data/x#pw"},
		},
		{
			name: "mixed reference kept",
			yaml: `tools:
  - tool_name: t
    headers:
      Authorization: Bearer ${TOKEN}
`,
			keep: []string{"Bearer ${TOKEN}"},
		},
		{
			name: "literal authorization header",
			yaml: `tools:
  - tool_name: t
    headers:
      Authorization: Bearer abc
      Content-Type: application/json
      Cookie: sid=abc
      X-API-Key: literal-key
`,
			want: []string{"tools[t].headers.Authorization", "tools[t].headers.Cookie", "tools[t].headers.X-API-Key"},
			keep: []string{"application/json"},
		},
		{
			name: "mcp environment variables",
			yaml: `tools:
  - tool_type: mcp
    tool_name: gh
    environment_variables:
      GITHUB_TOKEN: ghp_fake
      LOG_LEVEL: debug
`,
			want: []string{"tools[gh].environment_variables.GITHUB_TOKEN"},
			keep: []string{"debug"},
		},
		{
			name: "url with api_key query",
			yaml: `tools:
  - tool_name: t
    specification_url: https://example.com/spec?api_key=fake&v=1
    base_url: https://example.com/v1
`,
			want: []string{"tools[t].specification_url"},
			keep: []string{"https://example.com/v1"},
		},
		{
			name: "url with userinfo password",
			yaml: `tools:
  - tool_name: t
    base_url: https://user:pw@example.com/v1
    specification_url: https://user@example.com/spec
`,
			want: []string{"tools[t].base_url"},
			keep: []string{"https://user@example.com/spec"},
		},
		{
			name: "sequence under secret key",
			yaml: `tools:
  - tool_name: t
    tool_config:
      tokens: [a, b]
      secrets:
        - s1
        - s2
`,
			want: []string{"tools[t].tool_config.secrets[0]", "tools[t].tool_config.secrets[1]"},
			keep: []string{"tokens: [a, b]"},
		},
		{
			name: "numbers under credential keys redacted, counts, bool, null and empty untouched",
			yaml: `tools:
  - tool_name: t
    tool_config:
      token: 12345
      max_token: 512
      password: 12345678
      pin: 1234
      secret: null
      api_key: ""
      password_enabled: true
`,
			want: []string{"tools[t].tool_config.token", "tools[t].tool_config.password", "tools[t].tool_config.pin"},
		},
		{
			name: "outside tool config keys untouched",
			yaml: `tools:
  - tool_name: t
    description: password is in the vault
    password: not-in-config-subtree
`,
		},
		{
			name: "already sentinel",
			yaml: `tools:
  - tool_name: t
    tool_config:
      api_key: <REDACTED>
`,
		},
		{
			name: "tool id falls back to index",
			yaml: `tools:
  - tool_type: builtin
    tool_config:
      token: fake
`,
			want: []string{"tools[0].tool_config.token"},
		},
		{
			name: "tool id uses name then group_name",
			yaml: `tools:
  - name: named
    tool_config:
      token: fake
  - group_name: grouped
    tool_config:
      token: fake
`,
			want: []string{"tools[named].tool_config.token", "tools[grouped].tool_config.token"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := "---\nname: s\n" + tc.yaml + "---\nbody\n"
			out, redacted := mustRedact(t, in)
			if !slices.Equal(redacted, tc.want) {
				t.Fatalf("redacted = %v, want %v\n%s", redacted, tc.want, out)
			}
			for _, k := range tc.keep {
				if !strings.Contains(out, k) {
					t.Fatalf("expected %q to be kept:\n%s", k, out)
				}
			}
			if len(tc.want) == 0 && out != in {
				t.Fatalf("clean input must be returned byte-identical:\n%s", out)
			}
			if len(tc.want) > 0 && strings.Count(out, RedactedSentinel) != len(tc.want) {
				t.Fatalf("sentinel count mismatch:\n%s", out)
			}
		})
	}
}

func TestRedact_NoFrontMatterUnchanged(t *testing.T) {
	in := []byte("# just a body\napi_key: abc\n")
	out, redacted, err := Redact(in)
	if err != nil || redacted != nil || !bytes.Equal(out, in) {
		t.Fatalf("out=%q redacted=%v err=%v", out, redacted, err)
	}
}

func TestRedact_CleanSkillIdenticalBytes(t *testing.T) {
	in := []byte("---\nname: clean   # keep spacing\ndescription:    odd   spacing\ntools:\n    - tool_name: t\n      tool_config:\n          api_key: ${K}\n---\nbody\n")
	out, redacted, err := Redact(in)
	if err != nil || len(redacted) != 0 {
		t.Fatalf("redacted=%v err=%v", redacted, err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("clean skill changed:\n%s", out)
	}
}

func TestRedact_MalformedYAMLErrors(t *testing.T) {
	_, _, err := Redact([]byte("---\nname: [unclosed\ntools: {\n---\nbody\n"))
	if err == nil {
		t.Fatal("expected error for malformed front matter")
	}
}

func TestRedact_BOMAndCRLF(t *testing.T) {
	in := "\xEF\xBB\xBF---\r\nname: s\r\ntools:\r\n  - tool_name: t\r\n    tool_config:\r\n      api_key: fake\r\n---\r\nbody line\r\n"
	out, redacted := mustRedact(t, in)
	if len(redacted) != 1 {
		t.Fatalf("redacted = %v", redacted)
	}
	if !strings.HasPrefix(out, "\xEF\xBB\xBF---\r\n") {
		t.Fatalf("BOM/CRLF head not preserved: %q", out)
	}
	if strings.Contains(out, "fake") {
		t.Fatalf("secret leaked: %q", out)
	}
	if strings.Contains(strings.ReplaceAll(out, "\r\n", ""), "\n") {
		t.Fatalf("mixed line endings in output: %q", out)
	}
	if !strings.HasSuffix(out, "---\r\nbody line\r\n") {
		t.Fatalf("tail not preserved: %q", out)
	}
}

func TestRestore_RoundTrip(t *testing.T) {
	redactedDoc, _, err := Redact([]byte(webSkill))
	if err != nil {
		t.Fatal(err)
	}
	restored, unresolved, err := Restore(redactedDoc, []byte(webSkill))
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	got, want := parseFM(t, restored), parseFM(t, []byte(webSkill))
	if !equalMaps(got, want) {
		t.Fatalf("round trip differs:\n got %v\nwant %v", got, want)
	}
	if !bytes.Equal(bodyOf(t, restored), bodyOf(t, []byte(webSkill))) {
		t.Fatal("body changed")
	}
}

func equalMaps(a, b map[string]any) bool {
	x, _ := yaml.Marshal(a)
	y, _ := yaml.Marshal(b)
	return bytes.Equal(x, y)
}

func TestRestore_ReorderedTools(t *testing.T) {
	stored := []byte(webSkill)
	reordered := `---
name: web
tools:
  - tool_type: builtin
    tool_name: web_search_other
    tool_config:
      api_key: <REDACTED>
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: <REDACTED>
      google_cse_id: e11bfake0000
---
body
`
	out, unresolved, err := Restore([]byte(reordered), stored)
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	if !strings.Contains(string(out), fakeGoogleKey) || !strings.Contains(string(out), "fake-other-key") {
		t.Fatalf("secrets not restored by tool id:\n%s", out)
	}
}

func TestRestore_NoStoredValueUnresolved(t *testing.T) {
	incoming := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      api_key: <REDACTED>\n---\nbody\n"
	for name, stored := range map[string][]byte{"nil": nil, "different tool": []byte(webSkill)} {
		t.Run(name, func(t *testing.T) {
			out, unresolved, err := Restore([]byte(incoming), stored)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(unresolved, []string{"tools[t].tool_config.api_key"}) {
				t.Fatalf("unresolved = %v", unresolved)
			}
			if string(out) != incoming {
				t.Fatalf("nothing restorable, input should be unchanged:\n%s", out)
			}
		})
	}
}

func TestRestore_StoredSentinelIsNotAValue(t *testing.T) {
	doc := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      api_key: <REDACTED>\n---\n"
	_, unresolved, err := Restore([]byte(doc), []byte(doc))
	if err != nil || len(unresolved) != 1 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
}

func TestRestore_FastPathNoSentinel(t *testing.T) {
	in := []byte("---\nname: s\n---\nbody\n")
	out, unresolved, err := Restore(in, nil)
	if err != nil || unresolved != nil || !bytes.Equal(out, in) {
		t.Fatalf("out=%q unresolved=%v err=%v", out, unresolved, err)
	}
}

func TestRestore_SentinelOutsideToolsNeverRestored(t *testing.T) {
	stored := "---\nname: s\nmetadata:\n  owner: alice\n---\n"
	incoming := "---\nname: s\nmetadata:\n  owner: <REDACTED>\n---\n"
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	if err != nil || !slices.Equal(unresolved, []string{"metadata.owner"}) || string(out) != incoming {
		t.Fatalf("out=%s unresolved=%v err=%v", out, unresolved, err)
	}
}

func TestPlaceholders(t *testing.T) {
	redactedDoc, _, err := Redact([]byte(webSkill))
	if err != nil {
		t.Fatal(err)
	}
	out, subs, err := Placeholders(redactedDoc, "web")
	if err != nil {
		t.Fatal(err)
	}
	want := []Substitution{
		{"tools[web_search_google].tool_config.google_search_api_key", "${SKILL_WEB_WEB_SEARCH_GOOGLE_GOOGLE_SEARCH_API_KEY}"},
		{"tools[web_search_other].tool_config.api_key", "${SKILL_WEB_WEB_SEARCH_OTHER_API_KEY}"},
	}
	if !slices.Equal(subs, want) {
		t.Fatalf("subs = %v, want %v", subs, want)
	}
	for _, s := range want {
		if !strings.Contains(string(out), s.Placeholder) {
			t.Fatalf("placeholder %s missing:\n%s", s.Placeholder, out)
		}
	}
	if bytes.Contains(out[:len(out)-len(bodyOf(t, out))], []byte(RedactedSentinel)) {
		t.Fatalf("sentinel left in front matter:\n%s", out)
	}
	if !bytes.Equal(bodyOf(t, out), bodyOf(t, []byte(webSkill))) {
		t.Fatal("body changed")
	}
}

func TestPlaceholders_SequenceAndCollisions(t *testing.T) {
	in := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      secrets:\n        - <REDACTED>\n        - <REDACTED>\n---\n"
	_, subs, err := Placeholders([]byte(in), "my-skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 || subs[0].Placeholder == subs[1].Placeholder {
		t.Fatalf("subs = %v", subs)
	}
	if subs[0].Placeholder != "${SKILL_MY_SKILL_T_SECRETS_0}" {
		t.Fatalf("placeholder = %s", subs[0].Placeholder)
	}
}

func TestPlaceholders_NoSentinelUnchanged(t *testing.T) {
	in := []byte(webSkill)
	out, subs, err := Placeholders(in, "web")
	if err != nil || subs != nil || !bytes.Equal(out, in) {
		t.Fatalf("subs=%v err=%v", subs, err)
	}
}

func TestToolConfigKeysReturnsACopy(t *testing.T) {
	keys := ToolConfigKeys()
	keys[0] = "mutated"
	if ToolConfigKeys()[0] == "mutated" {
		t.Fatal("ToolConfigKeys exposes its backing slice")
	}
}

func TestToolConfigKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ToolConfigKeys() {
		if seen[k] {
			t.Fatalf("duplicate key %q", k)
		}
		seen[k] = true
	}
}

func redactedPaths(t *testing.T, yamlBody string) []string {
	t.Helper()
	_, redacted := mustRedact(t, "---\nname: s\n"+yamlBody+"---\nbody\n")
	return redacted
}

func TestRedact_CredentialMapsRedactEverythingExceptSafeKeys(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: api
    tool_type: openapi
    auth:
      type: apikey
      in: header
      name: X-API-Key
      value: FAKEVALUE
      service_account_json: '{"private_key":"FAKE"}'
      client_id: cid
      scopes: [read, write]
      token_url: https://idp.example.com/token
      token_endpoint_auth_method: client_secret_post
    headers:
      Ocp-Apim-Subscription-Key: fake
      x-functions-key: fake
      Content-Type: application/json
      accept: text/plain
  - tool_name: m
    tool_type: mcp
    connection_params:
      url: https://mcp.example.com
      auth:
        value: FAKEVALUE
        username: bob
      headers:
        X-Custom: fake
`)
	want := []string{
		"tools[api].auth.value",
		"tools[api].auth.service_account_json",
		"tools[api].headers.Ocp-Apim-Subscription-Key",
		"tools[api].headers.x-functions-key",
		"tools[m].connection_params.auth.value",
		"tools[m].connection_params.headers.X-Custom",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_EnvVarStyleNames(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: pg
    tool_type: mcp
    environment_variables:
      PGPASSWORD: fake
      DB_PASSWD: fake
      DB_PWD: fake
      PRIVATE_KEY: fake
      SIGNING_PRIVATE_BLOB: fake
      FUNCTIONS_KEY: fake
      APIKEY: fake
      ACCESS_KEY_ID: AKIAFAKEID
      LOG_LEVEL: debug
      SOME_URL: https://example.com
`)
	want := []string{
		"tools[pg].environment_variables.PGPASSWORD",
		"tools[pg].environment_variables.DB_PASSWD",
		"tools[pg].environment_variables.DB_PWD",
		"tools[pg].environment_variables.PRIVATE_KEY",
		"tools[pg].environment_variables.SIGNING_PRIVATE_BLOB",
		"tools[pg].environment_variables.FUNCTIONS_KEY",
		"tools[pg].environment_variables.APIKEY",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ReferencesStayUnderCredentialMaps(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: api
    auth:
      value: ${API_VALUE}
      secret: vault://kv/data/x#s
    headers:
      X-Custom: Bearer ${T}
`)
	if len(got) != 0 {
		t.Fatalf("references must be kept, redacted %v", got)
	}
}

func TestRedact_NonSecretNamesNotRedactedButURLRuleStillApplies(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: api
    tool_config:
      token_url: https://idp.example.com/token
      token_endpoint: https://idp.example.com/endpoint
      token_endpoint_auth_method: client_secret_post
      credential_key: key-name-not-a-value
      callback_uri: https://example.com/cb?api_key=FAKE
      refresh_url: https://user:pw@idp.example.com/refresh
`)
	want := []string{"tools[api].tool_config.callback_uri", "tools[api].tool_config.refresh_url"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_TaggedAndNumericValuesUnderSecretKeys(t *testing.T) {
	in := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      password: 12345678\n      api_key: !secret FAKE\n---\nbody\n"
	out, redacted := mustRedact(t, in)
	if len(redacted) != 2 || strings.Contains(out, "12345678") || strings.Contains(out, "FAKE") || strings.Contains(out, "!secret") {
		t.Fatalf("redacted=%v out:\n%s", redacted, out)
	}
	if strings.Count(out, `"<REDACTED>"`) != 2 {
		t.Fatalf("sentinels should be double-quoted strings:\n%s", out)
	}
}

func TestRestore_TaggedAndNumericRoundTrip(t *testing.T) {
	stored := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      password: 12345678\n      api_key: !secret FAKE\n---\nbody\n"
	redacted, _ := mustRedact(t, stored)
	out, unresolved, err := Restore([]byte(redacted), []byte(stored))
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	if !strings.Contains(string(out), "password: 12345678") || !strings.Contains(string(out), "api_key: !secret FAKE") {
		t.Fatalf("value, tag or style not restored:\n%s", out)
	}
}

func TestRestore_RefusesToMoveCredentialsBetweenTools(t *testing.T) {
	stored := `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    base_url: https://good.example.com
    auth:
      value: FAKESTORED
---
`
	cases := map[string]string{
		"base_url changed": `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    base_url: https://attacker.example.com
    auth:
      value: <REDACTED>
---
`,
		"tool type changed": `---
name: s
tools:
  - tool_name: api
    tool_type: mcp
    base_url: https://good.example.com
    auth:
      value: <REDACTED>
---
`,
		"renamed tool at same position": `---
name: s
tools:
  - tool_name: other
    tool_type: openapi
    base_url: https://good.example.com
    auth:
      value: <REDACTED>
---
`,
	}
	for name, incoming := range cases {
		t.Run(name, func(t *testing.T) {
			out, unresolved, err := Restore([]byte(incoming), []byte(stored))
			if err != nil {
				t.Fatal(err)
			}
			if len(unresolved) == 0 || strings.Contains(string(out), "FAKESTORED") {
				t.Fatalf("credential was restored into a different tool: unresolved=%v\n%s", unresolved, out)
			}
		})
	}
}

func TestRestore_RefusesDuplicateStoredEntries(t *testing.T) {
	stored := `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    auth:
      value: FAKEONE
  - tool_name: api
    tool_type: openapi
    auth:
      value: FAKETWO
---
`
	incoming := "---\nname: s\ntools:\n  - tool_name: api\n    tool_type: openapi\n    auth:\n      value: <REDACTED>\n---\n"
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	if err != nil || len(unresolved) != 1 || strings.Contains(string(out), "FAKE") {
		t.Fatalf("unresolved=%v err=%v out=%s", unresolved, err, out)
	}
}

func TestRestore_RedactedEndpointKeepsStoredEndpointAndSecret(t *testing.T) {
	stored := `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    specification_url: https://example.com/spec?api_key=FAKEKEY
    auth:
      value: FAKESTORED
---
`
	redacted, paths := mustRedact(t, stored)
	if len(paths) != 2 {
		t.Fatalf("redacted = %v", paths)
	}
	out, unresolved, err := Restore([]byte(redacted), []byte(stored))
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	if !strings.Contains(string(out), "FAKEKEY") || !strings.Contains(string(out), "FAKESTORED") {
		t.Fatalf("round trip lost values:\n%s", out)
	}
}

func TestRedactAndRestore_RejectAnchorsAliasesAndMergeKeys(t *testing.T) {
	docs := map[string]string{
		"anchor and alias":  "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      a: &k FAKE\n      api_key: *k\n---\n",
		"merge key":         "---\nname: s\nbase: &b {api_key: FAKE}\ntools:\n  - tool_name: t\n    tool_config:\n      <<: *b\n---\n",
		"anchor with tools": "---\nname: &n s\ntools: []\n---\n",
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Redact([]byte(doc)); err == nil {
				t.Error("Redact must refuse")
			}
			withSentinel := doc + "<REDACTED>"
			if _, _, err := Restore([]byte(withSentinel), []byte(doc)); err == nil {
				t.Error("Restore must refuse")
			}
		})
	}
}

func TestRedactAndRestore_RejectUnclosedFrontMatter(t *testing.T) {
	doc := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      api_key: <REDACTED>\nbody that would be lost\n"
	if _, _, err := Redact([]byte(doc)); err == nil {
		t.Error("Redact must refuse an unclosed front matter")
	}
	if _, _, err := Restore([]byte(doc), []byte(doc)); err == nil {
		t.Error("Restore must refuse an unclosed front matter")
	}
}

func TestRedact_KeySuffixRuleHonoursNonSecretAllowList(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      cache_key: k
      partition_key: k
      sort_key: k
      idempotency_key: k
      primary_key: k
      public_key: k
      hash_key: k
      key_id: k
      signing_key_id: k
      credential_key: k
      private_key: fake
      db_password: fake
      PRIVATE_KEY: fake
      FUNCTIONS_KEY: fake
      openai_key: fake
      Slack-Signing-Key: fake
      AWS_ACCESS_KEY_ID: AKIAFAKEID
`)
	want := []string{
		"tools[t].tool_config.private_key",
		"tools[t].tool_config.db_password",
		"tools[t].tool_config.PRIVATE_KEY",
		"tools[t].tool_config.FUNCTIONS_KEY",
		"tools[t].tool_config.openai_key",
		"tools[t].tool_config.Slack-Signing-Key",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_EnvMapsRedactEverythingExceptSafeNames(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: gh
    tool_type: mcp
    environment_variables:
      HTTPS_PROXY: http://proxy.example.com
      NODE_OPTIONS: --require=/tmp/x
      LOG_LEVEL: info
      NODE_ENV: production
      PORT: 8080
      PWD: /work
      AWS_ACCESS_KEY_ID: AKIAFAKEID
      SERVICE_URL: https://svc.example.com
      CACHE_DIR: /tmp/c
      FROM_VAULT: ${X}
    connection_params:
      env:
        SESSION_BLOB: fake
        APP_MODE: dev
`)
	want := []string{
		"tools[gh].environment_variables.HTTPS_PROXY",
		"tools[gh].environment_variables.NODE_OPTIONS",
		"tools[gh].connection_params.env.SESSION_BLOB",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_SecretFlagsInArgs(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: srv
    tool_type: mcp
    connection_params:
      command: server
      args:
        - --host
        - example.com
        - --api-key
        - FAKEONE
        - --token=FAKETWO
        - -p
        - 8080
        - --password=${PW}
        - --client-secret
        - ${SECRET}
        - --verbose
`)
	want := []string{
		"tools[srv].connection_params.args[3]",
		"tools[srv].connection_params.args[4]",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ValueScanningCatchesCredentialsUnderAnyKey(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      note: Bearer 0123456789abcdef0123456789abcdef
      scheme_note: Basic dXNlcjpwYXNzd29yZDEyMzQ1Ng==
      prose_one: Give a basic summary of results
      prose_two: basic search
      prose_three: Use bearer tokens only
      dsn: Server=db;User=u;Password=hunter2
      sas: https://acct.blob.example.com/c?sv=2020&sig=FAKESIG
      with_userinfo: https://u:p@host.example.com/x
      slack_webhook: https://hooks.example.com/T1/B2/X3
      oauth_redirect: https://app.example.com/cb?code=FAKECODE
      plain: just some text
      docs: https://docs.example.com/page
      ref_dsn: Server=db;Password=${DBPW}
`)
	want := []string{
		"tools[t].tool_config.note",
		"tools[t].tool_config.scheme_note",
		"tools[t].tool_config.dsn",
		"tools[t].tool_config.sas",
		"tools[t].tool_config.with_userinfo",
		"tools[t].tool_config.slack_webhook",
		"tools[t].tool_config.oauth_redirect",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_HeaderLists(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_type: openapi
    headers:
      - name: Authorization
        value: Bearer FAKE
      - name: Accept
        value: application/json
      - name: X-Custom
        value: fake
`)
	want := []string{"tools[t].headers[0].value", "tools[t].headers[2].value"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_SafeValuesAreNotOverRedacted(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_type: openapi
    tool_config:
      max_token: 512
      PWD: /work
      PRIVATE_MODE: "true"
      enabled: true
    auth:
      type: oauth2
      credential_key: name-only
      ca_cert_path: /etc/ca.pem
      realm: r
      tenant_id: tid
      header: X-Auth
      prefix: Token
      scheme: bearer
      authorization_url: https://idp.example.com/authorize
      verify: false
      client_secret: FAKE
    headers:
      X-Tenant-Id: t1
      Accept-Language: en
      Accept-Encoding: gzip
      X-Request-Id: r1
      X-Custom: fake
`)
	want := []string{"tools[t].auth.client_secret", "tools[t].headers.X-Custom"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_AnchorsAnywhereFailClosedForTopLevelManifest(t *testing.T) {
	for name, doc := range map[string]string{
		"anchor without tools":      "---\nname: &n s\ndescription: *n\n---\nbody\n",
		"root merge key":            "---\nname: s\nx: &x {tools: []}\n<<: *x\n---\nbody\n",
		"anchor in unrelated block": "---\nname: s\nmetadata:\n  a: &a 1\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Redact([]byte(doc)); err == nil {
				t.Fatal("Redact must refuse")
			}
			if out, _, err := RedactNested([]byte(doc)); err != nil || string(out) != doc {
				t.Fatalf("nested copies are served as is: out=%q err=%v", out, err)
			}
		})
	}
}

func TestRedactNested_ServesUnparseableCopiesAsIs(t *testing.T) {
	for name, doc := range map[string]string{
		"unclosed": "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      api_key: FAKE\n",
		"alias":    "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      a: &k FAKE\n      b: *k\n---\n",
		"none":     "just text api_key: FAKE\n",
	} {
		out, redacted, err := RedactNested([]byte(doc))
		if err != nil || len(redacted) != 0 || string(out) != doc {
			t.Errorf("%s: out=%q redacted=%v err=%v", name, out, redacted, err)
		}
	}
	parseable := "---\nname: s\ntools:\n  - tool_name: t\n    tool_config:\n      api_key: FAKE\n---\nbody\n"
	out, redacted, err := RedactNested([]byte(parseable))
	if err != nil || len(redacted) != 1 || strings.Contains(string(out), "FAKE") {
		t.Fatalf("parseable nested copy must be redacted: out=%q redacted=%v err=%v", out, redacted, err)
	}
}

const exploitStored = `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    base_url: https://good.example.com
    specification:
      servers:
        - url: https://good.example.com
    ssl_config:
      verify: true
    auth:
      type: apikey
      in: header
      name: X-Key
      value: FAKEAPIKEY
  - tool_name: oauth
    tool_type: openapi
    auth:
      type: oauth2
      token_url: https://idp.example.com/token
      client_id: cid
      client_secret: FAKECLIENTSECRET
  - tool_name: gh
    tool_type: mcp
    connection_params:
      type: stdio
      command: npx
      args: [-y, server]
    environment_variables:
      GITHUB_TOKEN: FAKEGHTOKEN
      LOG_LEVEL: info
---
body
`

func TestRestore_ExploitedEditsAreRejected(t *testing.T) {
	redacted, _ := mustRedact(t, exploitStored)
	cases := []struct {
		name, from, to, secret string
	}{
		{"token_url changed with client_secret sentinel", "https://idp.example.com/token", "https://attacker.example.com/token", "FAKECLIENTSECRET"},
		{"inline spec server url changed", "        - url: https://good.example.com", "        - url: https://attacker.example.com", "FAKEAPIKEY"},
		{"environment variables added", "      LOG_LEVEL: info", "      LOG_LEVEL: info\n      HTTPS_PROXY: https://attacker.example.com\n      NODE_OPTIONS: --require=/tmp/x", "FAKEGHTOKEN"},
		{"ssl verify switched off", "      verify: true", "      verify: false", "FAKEAPIKEY"},
		{"ssl config extended", "      verify: true\n", "      verify: true\n      ca_cert_path: /tmp/evil\n", "FAKEAPIKEY"},
		{"transport type changed", "      type: stdio", "      type: sse", "FAKEGHTOKEN"},
		{"command changed", "      command: npx", "      command: curl", "FAKEGHTOKEN"},
		{"args extended", "[-y, server]", "[-y, server, --extra]", "FAKEGHTOKEN"},
		{"apikey moved from header to query", "      in: header", "      in: query", "FAKEAPIKEY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(redacted, tc.from) {
				t.Fatalf("edit target %q not found in:\n%s", tc.from, redacted)
			}
			incoming := strings.Replace(redacted, tc.from, tc.to, 1)
			out, unresolved, err := Restore([]byte(incoming), []byte(exploitStored))
			if err != nil {
				t.Fatal(err)
			}
			if len(unresolved) == 0 || strings.Contains(string(out), tc.secret) {
				t.Fatalf("edited tool must not get %s back: unresolved=%v\n%s", tc.secret, unresolved, out)
			}
		})
	}
}

func TestRestore_BaseURLSentinelWithTokenURLChangeIsRejected(t *testing.T) {
	stored := `---
name: s
tools:
  - tool_name: api
    tool_type: openapi
    base_url: https://svc.example.com/v1?api_key=FAKEURLKEY
    auth:
      type: oauth2
      token_url: https://idp.example.com/token
      client_secret: FAKECLIENTSECRET
---
`
	redacted, paths := mustRedact(t, stored)
	if len(paths) != 2 {
		t.Fatalf("redacted = %v", paths)
	}
	incoming := strings.Replace(redacted, "https://idp.example.com/token", "https://attacker.example.com/token", 1)
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	if err != nil || len(unresolved) != 2 || strings.Contains(string(out), "FAKE") {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, out)
	}
}

func TestRestore_UnchangedRedactedEntriesAreRestored(t *testing.T) {
	redacted, paths := mustRedact(t, exploitStored)
	if len(paths) != 3 {
		t.Fatalf("redacted = %v", paths)
	}
	out, unresolved, err := Restore([]byte(redacted), []byte(exploitStored))
	if err != nil || len(unresolved) != 0 {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	if !equalMaps(parseFM(t, out), parseFM(t, []byte(exploitStored))) {
		t.Fatalf("round trip differs:\n%s", out)
	}
}

func TestRestore_EditingOneToolKeepsTheOthersRestorable(t *testing.T) {
	redacted, _ := mustRedact(t, exploitStored)
	incoming := strings.Replace(redacted, "https://idp.example.com/token", "https://attacker.example.com/token", 1)
	out, unresolved, err := Restore([]byte(incoming), []byte(exploitStored))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unresolved, []string{"tools[oauth].auth.client_secret"}) {
		t.Fatalf("unresolved = %v", unresolved)
	}
	if !strings.Contains(string(out), "FAKEAPIKEY") || !strings.Contains(string(out), "FAKEGHTOKEN") || strings.Contains(string(out), "FAKECLIENTSECRET") {
		t.Fatalf("untouched tools must still restore:\n%s", out)
	}
}

func TestRedact_ExemptNamesKeptButValueScanningStillApplies(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      model_name: gpt
      role_arn: arn:aws:iam::1:role/x
      retry_policy: exponential
      rate_limit: 5
      aws_profile: default
      MONKEY: banana
      HOTKEY: ctrl
      key: fake
      secret_profile: https://u:p@host.example.com/x
    environment_variables:
      GOOGLE_APPLICATION_CREDENTIALS: /etc/gcp.json
      MODEL_NAME: gpt
      FEATURE_FLAGS: a,b
      AWS_PROFILE: dev
      CALL_LIMIT: "10"
      SESSION_BLOB: fake
`)
	want := []string{
		"tools[t].tool_config.key",
		"tools[t].tool_config.secret_profile",
		"tools[t].environment_variables.SESSION_BLOB",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRestore_EntryMissingAKeyIsNotTheStoredEntry(t *testing.T) {
	redacted, _ := mustRedact(t, exploitStored)
	incoming := strings.Replace(redacted, "    ssl_config:\n      verify: true\n", "", 1)
	if incoming == redacted {
		t.Fatal("test edit did not apply")
	}
	out, unresolved, err := Restore([]byte(incoming), []byte(exploitStored))
	if err != nil || len(unresolved) == 0 || strings.Contains(string(out), "FAKEAPIKEY") {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, out)
	}
}

func TestPlaceholders_SuffixNeverCollidesWithANaturalName(t *testing.T) {
	for name, fields := range map[string]string{
		"natural name after a suffixed one":  "    environment_variables:\n      API_KEY: <REDACTED>\n      API_KEY_2: <REDACTED>\n    auth:\n      api_key: <REDACTED>\n",
		"natural name before a suffixed one": "    environment_variables:\n      API_KEY_2: <REDACTED>\n      API_KEY: <REDACTED>\n    auth:\n      api_key: <REDACTED>\n",
	} {
		t.Run(name, func(t *testing.T) {
			in := "---\nname: s\ntools:\n  - tool_name: x\n" + fields + "---\n"
			_, subs, err := Placeholders([]byte(in), "s")
			if err != nil || len(subs) != 3 {
				t.Fatalf("subs=%v err=%v", subs, err)
			}
			seen := map[string]bool{}
			for _, sub := range subs {
				if seen[sub.Placeholder] {
					t.Fatalf("two secrets share %s: %v", sub.Placeholder, subs)
				}
				seen[sub.Placeholder] = true
			}
		})
	}
}
