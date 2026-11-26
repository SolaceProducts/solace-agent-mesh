// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrefixedSentinelOnlyExistsInArgs(t *testing.T) {
	doc := "---\nname: s\ndescription: \"Explains log lines such as Authorization: <REDACTED>\"\ntools:\n" +
		"  - tool_name: t\n    tool_type: builtin\n    description: \"masks key=<REDACTED>\"\n    tool_config:\n      note: \"a=<REDACTED>\"\n" +
		"  - tool_name: srv\n    tool_type: mcp\n    connection_params:\n      args:\n        - \"--api-key=<REDACTED>\"\n---\n"
	_, unresolved, err := Restore([]byte(doc), []byte(doc))
	if err != nil || !slices.Equal(unresolved, []string{"tools[srv].connection_params.args[0]"}) {
		t.Fatalf("prose must not be a sentinel; unresolved=%v err=%v", unresolved, err)
	}
	out, subs, err := Placeholders([]byte(doc), "s")
	if err != nil || len(subs) != 1 || subs[0].FieldPath != "tools[srv].connection_params.args[0]" {
		t.Fatalf("subs=%v err=%v", subs, err)
	}
	for _, kept := range []string{"Authorization: <REDACTED>", "masks key=<REDACTED>", "a=<REDACTED>"} {
		if !strings.Contains(string(out), kept) {
			t.Errorf("%q must be left alone:\n%s", kept, out)
		}
	}
}

func TestRestore_EqualityOracleIsClosed(t *testing.T) {
	stored := "---\nname: s\ntools:\n  - tool_name: t\n    tool_type: openapi\n    auth:\n      api_key: FAKEK\n      password: FAKEP\n---\n"
	mk := func(key, pw string) string {
		return "---\nname: s\ntools:\n  - tool_name: t\n    tool_type: openapi\n    auth:\n      api_key: " + key + "\n      password: " + pw + "\n---\n"
	}
	right, wrong := mk("FAKEK", "<REDACTED>"), mk("WRONG", "<REDACTED>")
	outRight, unRight, errRight := Restore([]byte(right), []byte(stored))
	outWrong, unWrong, errWrong := Restore([]byte(wrong), []byte(stored))
	if errRight != nil || errWrong != nil {
		t.Fatal(errRight, errWrong)
	}
	if len(unRight) != 1 || !slices.Equal(unRight, unWrong) {
		t.Fatalf("right and wrong guesses must be indistinguishable: %v vs %v", unRight, unWrong)
	}
	if strings.Contains(string(outRight), "FAKEP") || strings.Contains(string(outWrong), "FAKEP") {
		t.Fatal("the password must not be restored")
	}

	both, unresolved, err := Restore([]byte(mk("<REDACTED>", "<REDACTED>")), []byte(stored))
	if err != nil || len(unresolved) != 0 || !strings.Contains(string(both), "FAKEK") || !strings.Contains(string(both), "FAKEP") {
		t.Fatalf("both redacted must restore both: unresolved=%v err=%v\n%s", unresolved, err, both)
	}

	storedArgs := "---\nname: s\ntools:\n  - tool_name: srv\n    tool_type: mcp\n    connection_params:\n      args:\n        - --api-key=real\n        - --password=pw\n---\n"
	incomingArgs := strings.Replace(storedArgs, "--password=pw", `"--password=<REDACTED>"`, 1)
	out, unresolved, err := Restore([]byte(incomingArgs), []byte(storedArgs))
	if err != nil || len(unresolved) != 1 || strings.Contains(string(out), "=pw") {
		t.Fatalf("a literal beside a prefixed sentinel must not restore: unresolved=%v err=%v\n%s", unresolved, err, out)
	}
}

func TestRedact_ReferenceOnlyStructureIsNotASecret(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      kept_query: https://api.example.com/v1?api_key=${API_KEY}
      kept_userinfo: https://${U}:${P}@host.example.com/x
      kept_sig: https://h.example.com/v?sig=${SIG}&a=1
      kept_json: '{"token": "${TOK}"}'
      literal_query: https://api.example.com/v1?api_key=hunter2&x=${Y}
      literal_userinfo: https://u:hunter2@h.example.com/${T}
      literal_conn: Password=hunter2;Db=${X}
`)
	want := []string{"tools[t].tool_config.literal_query", "tools[t].tool_config.literal_userinfo", "tools[t].tool_config.literal_conn"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_CountLikeFlagsKeepTheirNumbers(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: srv
    tool_type: mcp
    connection_params:
      args:
        - --token-limit
        - 5
        - --max-token=1000
        - --password
        - 123456
        - --api-key=12345678
`)
	want := []string{"tools[srv].connection_params.args[4]", "tools[srv].connection_params.args[5]"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_JSONSecretKeysByNameAndStringValue(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      a: '{"access_token": "FAKEONE"}'
      b: '{"api_key": "FAKETWO"}'
      c: '{"apiKey": "FAKETHREE"}'
      d: '{"refresh_token": "FAKEFOUR"}'
      e: '{"max_tokens": 100}'
      f: '{"token_count": 3}'
      g: '{"name": "plain", "mode": "fast"}'
`)
	want := []string{"tools[t].tool_config.a", "tools[t].tool_config.b", "tools[t].tool_config.c", "tools[t].tool_config.d"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ArgHeaderFormsWithoutSpaceAndAttachedShortFlags(t *testing.T) {
	in := `---
name: s
tools:
  - tool_name: srv
    tool_type: mcp
    connection_params:
      args:
        - "--header=Authorization:Bearer faketok1"
        - "Authorization:Bearer faketok2"
        - "-HAuthorization: Bearer faketok3"
        - "-HAuthorization:Bearer faketok4"
        - "-HContent-Type:application/json"
        - "-eGITHUB_TOKEN=faketok5"
---
`
	out, paths := mustRedact(t, in)
	if len(paths) != 5 || strings.Contains(out, "faketok") || !strings.Contains(out, "-HContent-Type:application/json") {
		t.Fatalf("paths=%v\n%s", paths, out)
	}
	restored, unresolved, err := Restore([]byte(out), []byte(in))
	if err != nil || len(unresolved) != 0 || strings.Count(string(restored), "faketok") != 5 {
		t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, restored)
	}
	withPlaceholders, subs, err := Placeholders([]byte(out), "s")
	if err != nil || len(subs) != 5 || strings.Contains(string(withPlaceholders), "<REDACTED>") {
		t.Fatalf("subs=%v err=%v\n%s", subs, err, withPlaceholders)
	}
}

func TestRedact_ReferenceDefaultsAreJudgedAsLiterals(t *testing.T) {
	const fakeGitHubToken = "ghp_" + "FAKEabcdefghijklmnop1234"
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      token: "${TOK:-hunter2}"
      api_key: "${TOK, `+fakeGitHubToken+`}"
      port: "${PORT:-8080}"
      secret: "${TOK}"
      password: "${TOK:-}"
      base_url: "${URL:-https://api.example.com}"
`)
	want := []string{"tools[t].tool_config.token", "tools[t].tool_config.api_key"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRestore_MismatchedArgPrefixesNeverRestore(t *testing.T) {
	cases := []struct{ name, stored, incoming string }{
		{"different flag", "--api-key=abc", "--token=<REDACTED>"},
		{"flag with embedded value", "--api-key=abc", "--api-key=x=<REDACTED>"},
		{"different env name", "GITHUB_TOKEN=ghp_x", "OTHER_TOKEN=<REDACTED>"},
		{"different header", "Authorization: Bearer x", "X-Api-Key: <REDACTED>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := func(arg string) string {
				return "---\nname: s\ntools:\n  - tool_name: srv\n    tool_type: mcp\n    connection_params:\n      args:\n        - \"" + arg + "\"\n---\n"
			}
			out, unresolved, err := Restore([]byte(doc(tc.incoming)), []byte(doc(tc.stored)))
			if err != nil || len(unresolved) != 1 || string(out) != doc(tc.incoming) {
				t.Fatalf("unresolved=%v err=%v\n%s", unresolved, err, out)
			}
		})
	}
}

func hugeToolConfig(n int, secret string) string {
	var b strings.Builder
	b.WriteString("---\nname: s\ntools:\n  - tool_name: t\n    tool_type: builtin\n    tool_config:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "      k%d: v%d\n", i, i)
	}
	b.WriteString("      api_key: " + secret + "\n---\nbody\n")
	return b.String()
}

func TestRestore_LargeToolConfigIsLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("large input")
	}
	stored, incoming := hugeToolConfig(50_000, "FAKEBIG"), hugeToolConfig(50_000, "<REDACTED>")
	start := time.Now()
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	elapsed := time.Since(start)
	if err != nil || len(unresolved) != 0 || !strings.Contains(string(out), "api_key: FAKEBIG") {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
	t.Logf("Restore of 50,000 keys: %v", elapsed)
}

func BenchmarkRestoreLargeToolConfig(b *testing.B) {
	stored, incoming := []byte(hugeToolConfig(50_000, "FAKEBIG")), []byte(hugeToolConfig(50_000, "<REDACTED>"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Restore(incoming, stored); err != nil {
			b.Fatal(err)
		}
	}
}

func manyToolsDoc(n int, secret string) string {
	var b strings.Builder
	b.WriteString("---\nname: s\ntools:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  - tool_type: builtin\n    tool_name: tool%d\n    tool_config:\n      api_key: %s\n      region: r%d\n", i, secret, i)
	}
	b.WriteString("---\nbody\n")
	return b.String()
}

func TestRestore_ManyToolsStaysFast(t *testing.T) {
	if testing.Short() {
		t.Skip("large input")
	}
	stored, incoming := manyToolsDoc(6000, "FAKEKEY"), manyToolsDoc(6000, "<REDACTED>")
	start := time.Now()
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	elapsed := time.Since(start)
	if err != nil || len(unresolved) != 0 || strings.Contains(string(out), "<REDACTED>") || strings.Count(string(out), "FAKEKEY") != 6000 {
		t.Fatalf("unresolved=%d err=%v", len(unresolved), err)
	}
	t.Logf("Restore of 6,000 tools: %v", elapsed)
}

func TestRestore_TooManyComparisonsFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("large input")
	}
	same := func(secret string) string {
		var b strings.Builder
		b.WriteString("---\nname: s\ntools:\n")
		for i := 0; i < 500; i++ {
			fmt.Fprintf(&b, "  - tool_type: builtin\n    tool_name: same\n    tool_config:\n      api_key: %s\n      n: %d\n", secret, i)
		}
		return b.String() + "---\n"
	}
	out, unresolved, err := Restore([]byte(same("<REDACTED>")), []byte(same("FAKEKEY")))
	if err != nil || len(unresolved) != 500 || strings.Contains(string(out), "FAKEKEY") {
		t.Fatalf("a quadratic bucket must fail closed: unresolved=%d err=%v", len(unresolved), err)
	}
}

func BenchmarkRestoreManyTools(b *testing.B) {
	stored, incoming := []byte(manyToolsDoc(6000, "FAKEKEY")), []byte(manyToolsDoc(6000, "<REDACTED>"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Restore(incoming, stored); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRedact_JSONKeysMatchBySegmentNotSubstring(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      keep_a: '{"tokenizer": "cl100k"}'
      keep_b: '{"passwordPolicy": "strict"}'
      keep_c: '{"max_tokens": "100"}'
      hit_a: '{"access_token": "FAKEONE"}'
      hit_b: '{"refresh_token": "FAKETWO"}'
      hit_c: '{"apiKey": "FAKETHREE"}'
      hit_d: '{"client_secret": "FAKEFOUR"}'
      hit_e: '{"password": "FAKEFIVE"}'
`)
	want := []string{"tools[t].tool_config.hit_a", "tools[t].tool_config.hit_b", "tools[t].tool_config.hit_c", "tools[t].tool_config.hit_d", "tools[t].tool_config.hit_e"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_ReferenceDefaultsInMapsUseNameAndShapeRulesOnly(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_type: mcp
    environment_variables:
      MCP_TRANSPORT: ${T:-stdio}
      DB_PASSWORD: ${P:-hunter2}
    headers:
      X-Mode: ${M:-fast}
      X-Api-Key: ${K:-literalkey}
`)
	want := []string{"tools[t].environment_variables.DB_PASSWORD", "tools[t].headers.X-Api-Key"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}

func TestRedact_LiteralSecretBesideAHostReferenceIsRedacted(t *testing.T) {
	got := redactedPaths(t, `tools:
  - tool_name: t
    tool_config:
      hit_query: https://${HOST}/v1?api_key=SECRETLIT
      hit_userinfo: postgres://admin:SECRETLIT@${DB}/db
      keep_query: https://api.example.com/v1?api_key=${API_KEY}
      keep_userinfo: https://${U}:${P}@host.example.com/x
      keep_host: https://${HOST}/v1
      keep_conn: Server=${S};Password=${PW}
      keep_bearer: Bearer ${A}${B}${C}${D}
      keep_json: '{"token": "${TOK}"}'
`)
	want := []string{"tools[t].tool_config.hit_query", "tools[t].tool_config.hit_userinfo"}
	if !slices.Equal(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
}
