// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package securitytest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func assertNoSecret(t *testing.T, buf *bytes.Buffer, secret string) {
	t.Helper()
	if secret == "" {
		t.Fatal("assertNoSecret called with empty secret")
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("secret %q leaked into log output:\n%s", secret, buf.String())
	}
}

func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func reservedLoopbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestRedactDSN_MasksPostgresCredentials(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		mustNotContain string
		mustContain    string
	}{
		{
			name:           "postgres with userinfo",
			in:             "postgres://sam:hunter2@db.internal:5432/sam",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "postgresql alt scheme with userinfo",
			in:             "postgresql://sam:hunter2@db.internal:5432/sam",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:        "postgres without userinfo",
			in:          "postgres://db.internal:5432/sam",
			mustContain: "db.internal:5432/sam",
		},
		{
			name:        "sqlite path",
			in:          "sqlite:///tmp/sam.db",
			mustContain: "sqlite:///tmp/sam.db",
		},
		{
			name:           "unparseable url with userinfo",
			in:             "mongo://sam:hunter2@host/db\x00bad",
			mustNotContain: "hunter2",
			mustContain:    "mongo://",
		},
		{
			name:           "password in the query string",
			in:             "postgres://sam@db.internal:5432/sam?password=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "password among other query params",
			in:             "postgresql://db.internal/sam?user=sam&password=hunter2&sslmode=require",
			mustNotContain: "hunter2",
			mustContain:    "sslmode=require",
		},
		{
			name:           "sslpassword in the query string",
			in:             "postgres://sam@db.internal/sam?sslpassword=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "uppercase query key",
			in:             "postgres://sam@db.internal/sam?PASSWORD=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "unparseable query with a password",
			in:             "postgres://sam@db.internal/sam?password=hunter2;sslmode=require",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "password with an invalid percent escape",
			in:             "postgres://sam@db.internal/sam?password=100%pass",
			mustNotContain: "100%pass",
			mustContain:    "db.internal",
		},
		{
			name:           "invalid escape alongside a real param",
			in:             "postgres://sam@db.internal/sam?password=p%zz&sslmode=require",
			mustNotContain: "p%zz",
			mustContain:    "db.internal",
		},
		{
			name:           "libpq keyword/value dsn",
			in:             "host=db.internal port=5432 user=sam password=hunter2 dbname=sam",
			mustNotContain: "hunter2",
			mustContain:    "<unparseable",
		},
		{
			name:           "scheme-less user:pass@host",
			in:             "sam:hunter2@db.internal:5432/sam",
			mustNotContain: "hunter2",
			mustContain:    "<unparseable",
		},
		{
			name:           "keyword/value dsn with a url-shaped value",
			in:             "host=db.internal password=hunter2 options=proto://x",
			mustNotContain: "hunter2",
			mustContain:    "<unparseable",
		},
		{
			name:           "credential in the fragment",
			in:             "postgres://db.internal/sam#password=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "sqlite key pragma",
			in:             "sqlite:///tmp/sam.db?_pragma=key('hunter2')",
			mustNotContain: "hunter2",
			mustContain:    "/tmp/sam.db",
		},
		{
			name:           "sqlite key pragma among other pragmas",
			in:             "sqlite:///tmp/sam.db?_pragma=busy_timeout(5000)&_pragma=textkey('hunter2')",
			mustNotContain: "hunter2",
			mustContain:    "/tmp/sam.db",
		},
		{
			name:           "sqlite fragment",
			in:             "sqlite:///tmp/sam.db#key=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "/tmp/sam.db",
		},
		{
			name:           "sqlite userinfo",
			in:             "sqlite://sam:hunter2@db.internal/sam.db",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "sqlite userinfo with a password-only authority",
			in:             "sqlite://:hunter2@db.internal/sam.db?_pragma=key('hunter2')",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "unvouched query key",
			in:             "postgres://sam@db.internal/sam?key=hunter2",
			mustNotContain: "hunter2",
			mustContain:    "db.internal",
		},
		{
			name:           "unvouched key alongside a vouched one",
			in:             "postgres://sam@db.internal/sam?auth_token=hunter2&sslmode=require",
			mustNotContain: "hunter2",
			mustContain:    "sslmode=require",
		},
		{
			name:           "sqlite pragma is never vouched for",
			in:             "sqlite:///tmp/sam.db?_pragma=key('hunter2')",
			mustNotContain: "hunter2",
			mustContain:    "/tmp/sam.db",
		},
		{name: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dbutil.RedactDSN(tc.in)
			if tc.mustNotContain != "" && strings.Contains(got, tc.mustNotContain) {
				t.Fatalf("RedactDSN(%q) = %q, must not contain %q", tc.in, got, tc.mustNotContain)
			}
			if tc.mustContain != "" && !strings.Contains(got, tc.mustContain) {
				t.Fatalf("RedactDSN(%q) = %q, must contain %q", tc.in, got, tc.mustContain)
			}
		})
	}
}

func TestGatewayInit_DoesNotLogDatabaseCredentials(t *testing.T) {
	const password = "hunter2-gateway-leak-canary"
	port := reservedLoopbackPort(t)
	dsn := fmt.Sprintf("postgres://sam:%s@127.0.0.1:%d/sam", password, port)

	buf := captureDefaultLogger(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := entrypoint.NewGatewayCore(entrypoint.NewLoggingAdapter(slog.Default()), map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": dsn,
		},
	})
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err == nil {
		t.Fatal("Init unexpectedly succeeded; bogus DSN was supposed to fail Open")
	}
	assertNoSecret(t, buf, password)
}

func TestGatewayInit_PostOpenLogOmitsURL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gw.db")
	dsn := "sqlite:///" + dbPath

	buf := captureDefaultLogger(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := entrypoint.NewGatewayCore(entrypoint.NewLoggingAdapter(slog.Default()), map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": dsn,
		},
	})
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed unexpectedly: %v", err)
	}

	requirePostOpenLogOmitsURL(t, buf, "database initialized", dbPath)
}

func TestPlatformInit_DoesNotLogDatabaseCredentials(t *testing.T) {
	const password = "hunter2-platform-leak-canary"
	port := reservedLoopbackPort(t)
	dsn := fmt.Sprintf("postgres://sam:%s@127.0.0.1:%d/sam", password, port)

	buf := captureDefaultLogger(t)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core := platform.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"fastapi_host": "127.0.0.1",
		"fastapi_port": 0,
		"database_url": dsn,
	}, platform.ExtensionPoints{})
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err == nil {
		t.Fatal("Init unexpectedly succeeded; bogus DSN was supposed to fail Open")
	}
	assertNoSecret(t, buf, password)
}

func TestOAuth2TokenError_BoundedSize(t *testing.T) {
	const bodySize = 2 << 20
	huge := strings.Repeat("A", bodySize)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, huge)
	}))
	t.Cleanup(srv.Close)

	mgr, err := oauth2cc.NewTokenManager(oauth2cc.Config{
		TokenURL:              srv.URL,
		ClientID:              "id",
		ClientSecret:          "secret",
		AllowInsecureTokenURL: true,
		MaxRetries:            1,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	_, err = mgr.GetToken(context.Background())
	if err == nil {
		t.Fatal("GetToken unexpectedly succeeded against a 400 endpoint")
	}

	const maxAcceptable = 1024
	if got := len(err.Error()); got > maxAcceptable {
		t.Fatalf("error message length %d exceeds bound %d (body size %d)\nfirst 200 bytes: %s",
			got, maxAcceptable, bodySize, err.Error()[:200])
	}
}

func TestOAuth2TokenError_PreservesRFC6749FieldsInLogsOnly(t *testing.T) {
	buf := captureDefaultLogger(t)
	body := `{"error":"invalid_client","error_description":"Client authentication failed"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	mgr, err := oauth2cc.NewTokenManager(oauth2cc.Config{
		TokenURL:              srv.URL,
		ClientID:              "id",
		ClientSecret:          "secret",
		AllowInsecureTokenURL: true,
		MaxRetries:            1,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	_, err = mgr.GetToken(context.Background())
	if err == nil {
		t.Fatal("GetToken unexpectedly succeeded against a 401 endpoint")
	}
	if strings.Contains(err.Error(), "invalid_client") || strings.Contains(err.Error(), "Client authentication failed") {
		t.Fatalf("GetToken's returned error must never carry the IdP's RFC 6749 fields (customer-visible field risk), got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("GetToken's returned error should still carry the HTTP status code, got: %q", err.Error())
	}
	if !strings.Contains(buf.String(), "invalid_client") || !strings.Contains(buf.String(), "Client authentication failed") {
		t.Fatalf("operator log should still preserve the RFC 6749 fields, got: %s", buf.String())
	}
}

func TestLoggingHandler_RedactsSecretAttrs(t *testing.T) {
	const secret = "shhh-this-is-the-key"

	cases := []string{
		"client_secret",
		"oauth_client_secret",
		"api_key",
		"anthropic_api_key",
		"password",
		"broker_password",
		"bearer_token",
		"credential",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			logger, buf := newCapturingLogger()
			logger.Info("test", key, secret)
			if strings.Contains(buf.String(), secret) {
				t.Fatalf("secret %q leaked under attr key %q:\n%s", secret, key, buf.String())
			}
		})
	}
}

func TestLoggingHandler_PreservesNonSecretAttrs(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"token_url", "https://idp.example.com/token"},
		{"credential_key", "salesforce_oauth"},
		{"url", "postgres://host/db"},
		{"client_id", "my-app"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			logger, buf := newCapturingLogger()
			logger.Info("test", tc.key, tc.value)
			if !strings.Contains(buf.String(), tc.value) {
				t.Fatalf("non-secret attr %q=%q was redacted:\n%s", tc.key, tc.value, buf.String())
			}
		})
	}
}

func requirePostOpenLogOmitsURL(t *testing.T, buf *bytes.Buffer, msgWanted, dbPath string) {
	t.Helper()
	found := false
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(line, fmt.Sprintf(`"msg":%q`, msgWanted)) {
			continue
		}
		found = true
		if strings.Contains(line, `"url":`) {
			t.Fatalf("post-Open log line %q must not carry a `url` attribute (would leak DSN %q):\n%s",
				msgWanted, dbPath, line)
		}
	}
	if !found {
		t.Fatalf("expected log line with msg=%q in captured output:\n%s", msgWanted, buf.String())
	}
}

func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: logging.MaskSecretAttr,
	}))
	return logger, buf
}
