// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	fakeMapsKey     = "AIza" + "FAKEMAPSKEY0000000000000000000000"
	fakeStripeKey   = "sk_live_" + "51H8xQ2abcdefgh"
	fakeGitHubPAT   = "github_pat_" + "11ABCDEFG0abcdefghij"
	fakeGitLabToken = "glpat-" + "abcdefghijklmnopqrst"
	fakeGitHubToken = "ghp_" + "0123456789abcdefghijABCDEFGHIJ"
)

func encodeForAssertion(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.String()
}

func TestRedactToolArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   map[string]any
		absent []string
		keep   []string
	}{
		{
			name:   "authorization header under an innocuous parent key",
			args:   map[string]any{"headers": map[string]any{"Authorization": "Bearer sk-live-abcdefgh"}},
			absent: []string{"sk-live-abcdefgh"},
			keep:   []string{"Authorization"},
		},
		{
			name:   "header map typed as map[string]string",
			args:   map[string]any{"headers": map[string]string{"X-Api-Key": "apikeysecretvalue"}},
			absent: []string{"apikeysecretvalue"},
		},
		{
			name:   "azure apim subscription key",
			args:   map[string]any{"headers": map[string]any{"Ocp-Apim-Subscription-Key": "8f3c9b1d4e5a6f70819293a4"}},
			absent: []string{"8f3c9b1d4e5a6f70819293a4"},
		},
		{
			name:   "credential in a query parameter",
			args:   map[string]any{"url": "https://api.example.com/v1?api_key=sk-live-1234abcd&q=weather"},
			absent: []string{"sk-live-1234abcd"},
			keep:   []string{"q=weather"},
		},
		{
			name:   "userinfo AND a token in the same url",
			args:   map[string]any{"url": "https://user:pa55word@example.com/x?k=sk-live-abcdefgh&q=1"},
			absent: []string{"pa55word", "sk-live-abcdefgh"},
			keep:   []string{"q=1"},
		},
		{
			name:   "google api key in a query, key named only 'key'",
			args:   map[string]any{"url": "https://maps.googleapis.com/x?address=y&key=" + fakeMapsKey},
			absent: []string{fakeMapsKey},
			keep:   []string{"address=y"},
		},
		{
			name:   "token in a url fragment, where implicit-flow puts it",
			args:   map[string]any{"url": "https://example.com/cb#access_token=sk-live-abcdefgh&state=xyz"},
			absent: []string{"sk-live-abcdefgh"},
			keep:   []string{"state=xyz"},
		},
		{
			name:   "slack webhook url is itself the credential",
			args:   map[string]any{"url": "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXX"},
			absent: []string{"XXXXXXXXXXXXXXXXXXXX"},
			keep:   []string{"hooks.slack.com/services/"},
		},
		{
			name:   "semicolon-delimited query that ParseQuery cannot read",
			args:   map[string]any{"url": "https://api.example.com/d?q=1;auth=abcdefgh12345678"},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"q=1"},
		},
		{
			name:   "short credential in a query",
			args:   map[string]any{"url": "https://h/api?auth=s3cr3t&page=2"},
			absent: []string{"s3cr3t"},
			keep:   []string{"page=2"},
		},
		{
			name:   "short credential in a fragment",
			args:   map[string]any{"url": "https://h/cb#auth=s3cr3t&state=xyz"},
			absent: []string{"s3cr3t"},
			keep:   []string{"state=xyz"},
		},
		{
			name:   "short credential after a semicolon separator",
			args:   map[string]any{"url": "https://h/api?q=1;auth=s3cr3t"},
			absent: []string{"s3cr3t"},
			keep:   []string{"q=1"},
		},
		{
			name:   "connection string embedded in a config blob",
			args:   map[string]any{"content": "DATABASE_URL=postgres://admin:pa55wordxyz@db:5432/app\nDEBUG=1"},
			absent: []string{"pa55wordxyz"},
			keep:   []string{"admin", "DEBUG=1"},
		},
		{
			name:   "url embedded in prose, which url.Parse rejects",
			args:   map[string]any{"body": "Fetched https://h/api?auth=abcdefgh12345678 (200 OK)"},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"200 OK"},
		},
		{
			name:   "second url in the same value is walked too",
			args:   map[string]any{"body": "curl https://u:pa55wordxyz@a/x then https://u2:pa55word222@b/y"},
			absent: []string{"pa55wordxyz", "pa55word222"},
		},
		{
			name:   "azure blob sas signature",
			args:   map[string]any{"url": "https://a.blob.core.windows.net/c/f?sv=2021&se=2026&sig=abcdEFGH1234xyz"},
			absent: []string{"abcdEFGH1234xyz"},
			keep:   []string{"sv=2021"},
		},
		{
			name:   "bearer scheme survives while the token goes",
			args:   map[string]any{"note": "curl -H 'Authorization: Bearer abcdef1234567890abcdef'"},
			absent: []string{"abcdef1234567890abcdef"},
			keep:   []string{"Bearer"},
		},
		{
			name:   "quoted value with an unquoted name",
			args:   map[string]any{"config_yaml": `client_secret: "abcdefgh12345678"`},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"client_secret"},
		},
		{
			name:   "single-quoted python kwarg",
			args:   map[string]any{"code": "S3(secret='wJalrXUtnFEMI')"},
			absent: []string{"wJalrXUtnFEMI"},
			keep:   []string{"secret"},
		},
		{
			name:   "openapi apiKey security scheme under a credential parent",
			args:   map[string]any{"auth": map[string]any{"type": "apiKey", "in": "header", "name": "X-Key", "value": "abcdefgh12345678"}},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"apiKey", "X-Key"},
		},
		{
			name:   "credential-named parent key never consumes its child key across a newline",
			args:   map[string]any{"content": "auth:\n  client_secret: 9f8c7b6a5d4e3f2a1b\n  username: bob\n"},
			absent: []string{"9f8c7b6a5d4e3f2a1b"},
			keep:   []string{"client_secret", "username", "bob"},
		},
		{
			name:   "quoted child key under a credential-named parent",
			args:   map[string]any{"content": "auth:\n  \"client_secret\": \"abcdefgh12345678\"\n"},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"client_secret"},
		},
		{
			name:   "single-quoted child key under a credential-named parent",
			args:   map[string]any{"content": "auth:\n  'client_secret': 'abcdefgh12345678'\n"},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"client_secret"},
		},
		{
			name:   "endpoint metadata survives in free text as it does as a key",
			args:   map[string]any{"content": "auth:\n  token_url: https://idp.example.com/oauth2/token\n  client_secret: abcdefgh12345678\n"},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"https://idp.example.com/oauth2/token"},
		},
		{
			name: "single-word metadata under a credential-named parent",
			args: map[string]any{"auth": map[string]any{"type": "client_credentials", "url": "https://idp.example.com/oauth2/token", "scheme": "client_secret_basic"}},
			keep: []string{"client_credentials", "idp.example.com/oauth2/token", "client_secret_basic"},
		},
		{
			name: "code reading a credential from the environment is untouched",
			args: map[string]any{"code": "cfg.Token = os.Getenv(\"TOK\")\npassword=os.environ[\"PW\"]"},
			keep: []string{"os.Getenv(", "os.environ["},
		},
		{
			name:   "markdown link keeps its closing paren",
			args:   map[string]any{"body": "[docs](https://h/x?api_key=sk-live-abcdefgh)"},
			absent: []string{"sk-live-abcdefgh"},
			keep:   []string{"[docs](", ")"},
		},
		{
			name: "empty query value is left alone",
			args: map[string]any{"url": "https://h/x?api_key=&q=1"},
			keep: []string{"api_key=&q=1"},
		},
		{
			name:   "oauth config under a credential-named parent keeps its metadata",
			args:   map[string]any{"auth": map[string]any{"client_id": "my-application-client", "token_url": "https://idp.example.com/oauth2/token", "type": "oauth2", "client_secret": "abcdefgh12345678"}},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"my-application-client", "idp.example.com/oauth2/token", "oauth2"},
		},
		{
			name:   "non-string value under a credential-named key",
			args:   map[string]any{"password": float64(12345678), "api_key": 424242, "secret": true},
			absent: []string{"12345678", "424242", "true"},
		},
		{
			name: "inherited numeric metadata survives",
			args: map[string]any{"auth": map[string]any{"expires_in": 3600, "type": "none"}},
			keep: []string{"3600", "none"},
		},
		{
			name:   "short value under a directly credential-named key",
			args:   map[string]any{"api_key": "short", "password": "abc", "Authorization": "xyz123"},
			absent: []string{"short", "abc", "xyz123"},
		},
		{
			name:   "credentials blob whose leaf name says nothing",
			args:   map[string]any{"credentials": map[string]any{"user": "alice", "pass": "abcdefgh12345678"}},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"alice"},
		},
		{
			name:   "private key block pasted into a body",
			args:   map[string]any{"body": "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"},
			absent: []string{"MIIEowIBAAKCAQEA"},
		},
		{
			name:   "string slice values are scanned",
			args:   map[string]any{"headers": []string{"Authorization: Bearer abcdefgh12345678", "Accept: */*"}},
			absent: []string{"abcdefgh12345678"},
			keep:   []string{"Accept"},
		},
		{
			name:   "env-file shape with the credential word mid-name",
			args:   map[string]any{"config_yaml": "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMIbPxRfiCYEX\nname: bot"},
			absent: []string{"wJalrXUtnFEMIbPxRfiCYEX"},
			keep:   []string{"AWS_SECRET_ACCESS_KEY", "name: bot"},
		},
		{
			name:   "yaml shape with a trailing-digit name",
			args:   map[string]any{"config_yaml": "password2: hunter2hunter2"},
			absent: []string{"hunter2hunter2"},
			keep:   []string{"password2"},
		},
		{
			name:   "json body shape, the common real-world case",
			args:   map[string]any{"body": `{"api_key":"hunter2secret","note":"hi"}`},
			absent: []string{"hunter2secret"},
			keep:   []string{"api_key", "note"},
		},
		{
			name:   "value containing a comma inside quotes",
			args:   map[string]any{"body": `{"api_key":"a,bcdefghij"}`},
			absent: []string{"a,bcdefghij"},
		},
		{
			name:   "raw authorization header inside a text value",
			args:   map[string]any{"body": "Authorization: abcdef1234567890abcdef"},
			absent: []string{"abcdef1234567890abcdef"},
			keep:   []string{"Authorization"},
		},
		{
			name:   "jwt anywhere",
			args:   map[string]any{"note": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27u"},
			absent: []string{"dBjftJeZ4CVPmB92K27u"},
		},
		{
			name:   "vendor-prefixed secrets with no telltale key",
			args:   map[string]any{"a": "xoxb-9999-secretvalue", "b": fakeStripeKey, "c": fakeGitHubPAT, "d": fakeGitLabToken},
			absent: []string{"xoxb-9999-secretvalue", fakeStripeKey, fakeGitHubPAT, fakeGitLabToken},
		},
		{
			name:   "credential nested in a list of objects",
			args:   map[string]any{"requests": []any{map[string]any{"password": "hunter2hunter"}}},
			absent: []string{"hunter2hunter"},
		},
		{
			name:   "structured auth object keeps its shape",
			args:   map[string]any{"auth": map[string]any{"type": "none", "value": "sk-live-abcdefgh"}},
			absent: []string{"sk-live-abcdefgh"},
			keep:   []string{`"type":"none"`},
		},
		{
			name: "acronym-prefixed credential names",
			args: map[string]any{
				"DBPassword": "hunter2secretvalue",
				"APIToken":   "8f3c9b1d4e5a6f70",
				"JWTSecret":  "s3cr3tsigningvalue",
				"body":       `{"SMTPPassword":"mailer2secretpw","CSRFToken":"csrf9f8c7b6a5d4e"}`,
			},
			absent: []string{"hunter2secretvalue", "8f3c9b1d4e5a6f70", "s3cr3tsigningvalue",
				"mailer2secretpw", "csrf9f8c7b6a5d4e"},
			keep: []string{"DBPassword", "SMTPPassword"},
		},
		{
			name: "authorization schemes other than bearer and basic",
			args: map[string]any{
				"okta":    "curl -H 'Authorization: SSWS 00QCjAl4MlV-WPXM0g1UOu4'",
				"github":  "Authorization: Token 8f3c9b1d4e5a6f70",
				"elastic": "Authorization: ApiKey ZmFrZWtleWlkOmZha2VzZWNyZXQ=",
				"proxy":   "Proxy-Authorization: Negotiate YIIKfwYGKwYBBQUCoIIK",
			},
			absent: []string{"00QCjAl4MlV-WPXM0g1UOu4", "8f3c9b1d4e5a6f70",
				"ZmFrZWtleWlkOmZha2VzZWNyZXQ=", "YIIKfwYGKwYBBQUCoIIK"},
			keep: []string{"SSWS", "Token", "ApiKey", "Negotiate"},
		},
		{
			name: "digest authorization redacts its credential fields only",
			args: map[string]any{
				"curl": `Authorization: Digest username="alice", realm="example.com", ` +
					`nonce="dcd98b7102dd2f0e", uri="/dir/index.html", qop=auth, nc=00000001, ` +
					`cnonce="0a4f113b9d1fa2ec", response="6629fae49393a05397450978507c4ef1", ` +
					`opaque="5ccc069c403ebaf9f0171e9517f40e41"`,
			},
			absent: []string{"6629fae49393a05397450978507c4ef1", "5ccc069c403ebaf9f0171e9517f40e41",
				"0a4f113b9d1fa2ec"},
			keep: []string{"Digest", `username=\"alice\"`, `realm=\"example.com\"`,
				`nonce=\"dcd98b7102dd2f0e\"`, "qop=auth"},
		},
		{
			name: "aws sigv4 authorization keeps its signed-header list",
			args: map[string]any{
				"curl": "Authorization: AWS4-HMAC-SHA256 " +
					"Credential=AKIAIOSFODNN7EXAMPLE/20260904/us-east-1/s3/aws4_request, " +
					"SignedHeaders=host;x-amz-date, " +
					"Signature=fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024",
			},
			absent: []string{"fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024",
				"AKIAIOSFODNN7EXAMPLE"},
			keep: []string{"AWS4-HMAC-SHA256", "SignedHeaders=host;x-amz-date"},
		},
		{
			name:   "generated password containing brackets is not code",
			args:   map[string]any{"env": "DB_PASSWORD=Ku(9xLm2Qpz\nPGPASSWORD=Xy9[aBcdefgh"},
			absent: []string{"Ku(9xLm2Qpz", "Xy9[aBcdefgh"},
			keep:   []string{"DB_PASSWORD", "PGPASSWORD"},
		},
		{
			name: "header-suffixed keys hold the value, not a name",
			args: map[string]any{
				"auth_header":   "Token 8f3c9b1d4e5a6f70",
				"cookie_header": "session=9f8c7b6a5d4e3f2a; csrf=abc12345",
			},
			absent: []string{"8f3c9b1d4e5a6f70", "9f8c7b6a5d4e3f2a"},
		},
		{
			name: "credential names the vocabulary used to miss",
			args: map[string]any{
				"passcode":     "s3cr3tvalue",
				"app_key":      "dd9f8c7b6a5d4e3f",
				"consumer_key": "CK9f8c7b6a5d4e12",
				"session_key":  "SK9f8c7b6a5d4e12",
				"bind_pw":      "ldapsecret1234",
			},
			absent: []string{"s3cr3tvalue", "dd9f8c7b6a5d4e3f", "CK9f8c7b6a5d4e12",
				"SK9f8c7b6a5d4e12", "ldapsecret1234"},
		},
		{
			name: "credential query parameters named key and session",
			args: map[string]any{
				"a": "https://maps.googleapis.com/maps/api/geocode/json?address=x&key=9f8c7b6a5d4e",
				"b": "https://h/x?sessionid=9f8c7b6a5d4e3f2a&page=2",
			},
			absent: []string{"9f8c7b6a5d4e3f2a"},
			keep:   []string{"address=x", "page=2"},
		},
		{
			name:   "query and fragment carrying credentials together",
			args:   map[string]any{"url": "https://h/cb?state=abc&api_key=sk-live-abcdefgh#access_token=t0kenvalue123&expires_in=3600"},
			absent: []string{"sk-live-abcdefgh", "t0kenvalue123"},
			keep:   []string{"state=abc", "expires_in=3600"},
		},
		{
			name: "vendor value shapes with no telltale key",
			args: map[string]any{
				"a": "xapp-1-A0123456789-abcdefghij",
				"b": fakeGitHubToken,
				"c": "AKIAIOSFODNN7EXAMPLE",
				"d": "Authorization: Basic dXNlcjpzdXBlcnNlY3JldA==",
				"e": "posting to https://hooks.slack.com/services/example-path-not-a-token",
			},
			absent: []string{"xapp-1-A0123456789-abcdefghij", fakeGitHubToken,
				"AKIAIOSFODNN7EXAMPLE", "dXNlcjpzdXBlcnNlY3JldA==",
				"example-path-not-a-token"},
			keep: []string{"Basic", "hooks.slack.com/services/"},
		},
		{
			name:   "inherited leaf at the length floor",
			args:   map[string]any{"credentials": map[string]any{"pass": "hunter22", "user": "alice"}},
			absent: []string{"hunter22"},
			keep:   []string{"alice"},
		},
		{
			name:   "quoted secret ending in a bracket is not an expression",
			args:   map[string]any{"body": `password: "Tr0ub4dor3(" ; api_key: "s3cr3tval["`},
			absent: []string{"Tr0ub4dor3(", "s3cr3tval["},
			keep:   []string{"password", "api_key"},
		},
		{
			name: "inherited leaf below the length floor stays readable",
			args: map[string]any{"credentials": map[string]any{"pass": "hunter2"}},
			keep: []string{"hunter2"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := encodeForAssertion(t, RedactToolArgs(c.args))
			for _, secret := range c.absent {
				if strings.Contains(got, secret) {
					t.Errorf("credential %q survived: %s", secret, got)
				}
			}
			for _, keep := range c.keep {
				if !strings.Contains(got, keep) {
					t.Errorf("%q was removed but is not a credential: %s", keep, got)
				}
			}
		})
	}
}

func TestRedactToolArgs_RedactedURLIsExact(t *testing.T) {
	got := RedactToolArgs(map[string]any{"url": "https://h/x?api_key=sk-live-abcdefgh&page=2"})
	want := "https://h/x?api_key=" + RedactedMarker + "&page=2"
	if got["url"] != want {
		t.Errorf("redacted url:\n want %q\n got  %q", want, got["url"])
	}
}

func TestRedactString_IsIdempotent(t *testing.T) {
	once := (&redaction{}).str("api_key=sk-live-abcdefgh")
	if twice := (&redaction{}).str(once); twice != once {
		t.Errorf("not idempotent:\n once  %q\n twice %q", once, twice)
	}
}

func TestRedactString_KeepsVariablePlaceholders(t *testing.T) {
	for _, in := range []string{
		"bot_token: ${SLACK_BOT_TOKEN}", "token=${TOKEN_VAR}",
		"api_key: {{ vault_key }}", "password: %(pw_variable_name)s", "secret: $MY_SECRET",
	} {
		if got := (&redaction{}).str(in); got != in {
			t.Errorf("placeholder was redacted:\n want %q\n got  %q", in, got)
		}
	}
	if got := (&redaction{}).str("password=$MySecretValue"); !strings.Contains(got, RedactedMarker) {
		t.Errorf("a literal $-prefixed secret survived: %q", got)
	}
}

func TestRedactToolArgs_DoesNotOverRedact(t *testing.T) {
	args := map[string]any{
		"max_tokens":          4096,
		"tokens_used":         128,
		"foreign_key":         "orders.id",
		"keyword":             "monkey",
		"author":              "ada",
		"authority":           "local",
		"secretary_name":      "Ada Lovelace",
		"public_key_pem":      "ssh-rsa AAAAB3NzaC1yc2EAAAA",
		"query":               "SELECT * FROM t WHERE k = 'v'",
		"url":                 "https://example.com/search?q=weather&page=2",
		"code":                "def f(token=None):\n    return token",
		"filename":            "report.md",
		"json_body":           `{"author":"Ada Lovelace","title":"Notes"}`,
		"assignments":         "authority=us-east-1-control\ndesign=material_theme_v2\nsigma=0.5000001234",
		"prose":               "the designation was assigned=alice_smith_jr",
		"auth_method":         "client_credentials",
		"auth_provider":       "keycloak-prod",
		"auth_mode":           "delegated",
		"cookie_domain":       ".example.com",
		"cookie_path":         "/application/v1",
		"cookie_secure":       true,
		"token_scope":         "read write admin",
		"signature_algorithm": "RSA-SHA256",
		"secret_version":      "3",
		"expiry_log":          "token.expires_at=1735689600",
		"sql_update":          "UPDATE u SET auth_provider = 'okta-prod' WHERE id = 1",
		"docs":                "Set up Basic authentication with the proxy; it is a basic requirement.",
		"auth":                "none",
		"git_remote":          "https://git@github.com/org/repo.git",
		"response":            "the server returned 200 OK with a body",
		"response_log":        "response=success_code_1234, opaque=theme_dark_v2",
		"cnonce_note":         "cnonce is a client nonce",
		"templated_url":       "https://h/x?api_key=${MY_KEY}",
		"flag_url":            "https://h/x?auth=false&page=2",
		"spa_route":           "https://app.example.com/#/settings?auth=true",
		"temperature":         float64(0.7),
		"seed":                int64(42),
		"stream":              true,
	}
	before, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := RedactToolArgs(args)
	if !reflect.DeepEqual(got, args) {
		for key, want := range args {
			if !reflect.DeepEqual(got[key], want) {
				t.Errorf("%s was rewritten:\n want %#v\n got  %#v", key, want, got[key])
			}
		}
		for key := range got {
			if _, ok := args[key]; !ok {
				t.Errorf("%s appeared in the output but not the input", key)
			}
		}
	}

	after, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("input map was mutated:\n before=%s\n after =%s", before, after)
	}
}

func TestRedactToolArgs_PreservesQueryOrderAndEscaping(t *testing.T) {
	got := RedactToolArgs(map[string]any{"url": "https://u:p@h/x?z=1&a=2&q=a b&filter=a:b"})
	want := "https://u:" + RedactedMarker + "@h/x?z=1&a=2&q=a b&filter=a:b"
	if got["url"] != want {
		t.Errorf("query was rewritten:\n want %q\n got  %q", want, got["url"])
	}
}

func TestRedactToolArgs_MarkerIsNotEscaped(t *testing.T) {
	got, _ := RedactToolArgs(map[string]any{"url": "https://h/x?api_key=sk-live-abcdefgh"})["url"].(string)
	if !strings.Contains(got, "api_key="+RedactedMarker) {
		t.Errorf("marker is not readable in the query: %q", got)
	}
}

func TestRedactToolArgs_DoesNotMutateNestedInput(t *testing.T) {
	headers := map[string]any{"Authorization": "Bearer sk-live-original"}
	args := map[string]any{"headers": headers, "url": "https://u:p@example.com/x"}

	redacted := RedactToolArgs(args)

	if headers["Authorization"] != "Bearer sk-live-original" {
		t.Errorf("nested caller map was mutated: %v", headers["Authorization"])
	}
	if args["url"] != "https://u:p@example.com/x" {
		t.Errorf("caller url was mutated: %v", args["url"])
	}
	if reflect.ValueOf(redacted["headers"]).Pointer() == reflect.ValueOf(any(headers)).Pointer() {
		t.Error("redacted headers alias the caller's map")
	}
}

func TestRedactToolArgs_TruncatesOversizedValues(t *testing.T) {
	huge := "readable head. " + strings.Repeat("a", maxRedactedValueBytes) + "PAST_THE_CUT"
	got, _ := RedactToolArgs(map[string]any{"body": huge})["body"].(string)
	if strings.Contains(got, "PAST_THE_CUT") {
		t.Error("content past the truncation point reached the wire")
	}
	if len(got) > maxRedactedValueBytes {
		t.Errorf("value exceeds the cap: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "readable head. ") {
		t.Errorf("prefix was discarded: %q", got[:min(40, len(got))])
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncation is not signalled: %q", got[max(0, len(got)-40):])
	}
}

func TestRedactToolArgs_CapSurvivesRedactionGrowth(t *testing.T) {
	dense := "https://h/?" + strings.Repeat("token=a&", 2048)
	if len(dense) <= maxRedactedValueBytes {
		t.Fatalf("fixture must exceed the cap to exercise it: %d bytes", len(dense))
	}
	got, _ := RedactToolArgs(map[string]any{"body": dense})["body"].(string)
	if len(got) > maxRedactedValueBytes {
		t.Errorf("redaction grew the value past the cap: %d bytes, cap %d", len(got), maxRedactedValueBytes)
	}
	if strings.Contains(got, "token=a") {
		t.Error("a credential-named parameter survived")
	}
}

func TestRedactValue_UnknownContainer(t *testing.T) {
	type payload struct {
		Token string `json:"token"`
		ID    int64  `json:"id"`
	}
	got, err := json.Marshal(RedactValue(&payload{Token: "sk-live-abcdefgh", ID: 9007199254740993}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(got), "sk-live-abcdefgh") {
		t.Errorf("credential survived an unhandled type: %s", got)
	}
	if !strings.Contains(string(got), "9007199254740993") {
		t.Errorf("integer lost precision: %s", got)
	}

	if unmarshalable := RedactValue(struct{ C chan int }{}); unmarshalable != unserializableMarker {
		t.Errorf("RedactValue(unserializable) = %#v, want %q", unmarshalable, unserializableMarker)
	}
}

func TestIsCredentialKey(t *testing.T) {
	credential := []string{
		"Authorization", "api_key", "apiKey", "X-Api-Key", "access_token", "refreshToken",
		"password", "passwd", "passphrase", "client_secret", "Cookie", "Set-Cookie",
		"connection_string", "credentials", "token", "auth", "Ocp-Apim-Subscription-Key",
		"license_key", "deploy_key", "signing_key", "account_key", "jwt", "signature",
		"sig", "passwords", "cookies", "requestSignature", "xAuthValue",
		"DBPassword", "APIToken", "JWTSecret", "SMTPPassword", "CSRFToken", "SASToken",
		"HMACSignature", "GPGPassphrase",
		"passcode", "app_key", "consumer_key", "session_key", "shared_key", "bind_pw",
		"auth_header", "authorization_header", "cookie_header",
	}
	for _, key := range credential {
		if !isCredentialKey(key) {
			t.Errorf("isCredentialKey(%q) = false, want true", key)
		}
	}

	benign := []string{
		"max_tokens", "tokens_used", "foreign_key", "keyword", "author", "authority",
		"secretary_name", "url", "query", "filename", "monkey", "body",
		"method", "public_key_pem", "keys", "author_name",
		"token_url", "auth_type", "cookie_name", "password_required", "session_id",
		"auth_method", "auth_provider", "auth_mode", "auth_status", "cookie_domain",
		"cookie_path", "cookie_secure", "token_scope", "signature_algorithm",
		"secret_version", "private_key_path",
		"-", "", "_",
	}
	for _, key := range benign {
		if isCredentialKey(key) {
			t.Errorf("isCredentialKey(%q) = true, want false", key)
		}
	}
}

func TestRedactValueReportingCutReportsOnlyTheCutsItMakes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  bool
	}{
		{"short", "hello", false},
		{"at the cap", strings.Repeat("x", maxRedactedValueBytes), false},
		{"over the cap", strings.Repeat("x", maxRedactedValueBytes+1), true},
		{"grown past the cap by redaction", strings.Repeat("http://a:b@c ", maxRedactedValueBytes/13), true},
		{"a long credential replaced, not cut", map[string]any{"password": strings.Repeat("x", 2*maxRedactedValueBytes)}, false},
		{"a mark already in the input", "…[truncated 9 bytes]" + strings.Repeat("x", maxRedactedValueBytes), true},
		{"nested over the cap", map[string]any{"rows": []any{strings.Repeat("x", maxRedactedValueBytes+1)}}, true},
		{"a cut then an uncut value in a list", []any{strings.Repeat("x", maxRedactedValueBytes+1), "short"}, true},
		{"a cut then an uncut string in a string list", []string{strings.Repeat("x", maxRedactedValueBytes+1), "short"}, true},
		{"an uncut then a cut value in a list", []any{"short", strings.Repeat("x", maxRedactedValueBytes+1)}, true},
		{"a cut beside a password assignment", strings.Repeat("x", 16352-9) + "password=" + strings.Repeat("A", 2000), true},
		{"a cut beside a token query", strings.Repeat("x", 16322) + "http://h/?token=" + strings.Repeat("A", 2000), true},
	} {
		if _, cut := RedactValueReportingCut(tc.value); cut != tc.want {
			t.Errorf("%s: cut %v, want %v", tc.name, cut, tc.want)
		}
	}
}
