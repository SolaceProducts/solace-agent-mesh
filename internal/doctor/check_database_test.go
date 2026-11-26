// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const goodDB = "postgresql://u:p@db.example.com:5432/x"

func TestDatabaseTCPCheck_AllPass(t *testing.T) {
	calls := []string{}
	check := &DatabaseTCPCheck{Dialer: func(_ context.Context, addr string) error {
		calls = append(calls, addr)
		return nil
	}}
	env := Env{
		PlatformDatabaseURL:     goodDB,
		OrchestratorDatabaseURL: goodDB,
		WebUIGatewayDatabaseURL: goodDB,
	}
	got := check.Run(context.Background(), env, ContextHelm)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
	if !strings.Contains(got.Detail, "3 databases") {
		t.Errorf("detail: got %q, want '3 databases'", got.Detail)
	}
	if len(calls) != 3 {
		t.Errorf("expected 3 dial calls, got %d", len(calls))
	}
}

func TestDatabaseTCPCheck_MissingPlatformInHelmFails(t *testing.T) {
	check := &DatabaseTCPCheck{Dialer: func(context.Context, string) error { return nil }}
	got := check.Run(context.Background(), Env{}, ContextHelm)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "PLATFORM_DATABASE_URL not set") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestDatabaseTCPCheck_OptionalMissingInWheelWarns(t *testing.T) {
	check := &DatabaseTCPCheck{Dialer: func(context.Context, string) error { return nil }}
	got := check.Run(context.Background(), Env{PlatformDatabaseURL: goodDB}, ContextWheel)
	if got.Status != StatusWarn {
		t.Errorf("status: got %s, want WARN", got.Status)
	}
}

func TestDatabaseTCPCheck_LocalSkipsAbsent(t *testing.T) {
	check := &DatabaseTCPCheck{Dialer: func(context.Context, string) error { return nil }}
	got := check.Run(context.Background(), Env{}, ContextLocal)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}

func TestDatabaseTCPCheck_SQLiteFailsInHelm(t *testing.T) {
	check := &DatabaseTCPCheck{Dialer: func(context.Context, string) error { return nil }}
	got := check.Run(context.Background(), Env{PlatformDatabaseURL: "sqlite:///x.db"}, ContextHelm)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "SQLite not suitable") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestDatabaseTCPCheck_DialError(t *testing.T) {
	check := &DatabaseTCPCheck{Dialer: func(context.Context, string) error { return errors.New("connection refused") }}
	got := check.Run(context.Background(), Env{PlatformDatabaseURL: goodDB}, ContextWheel)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "connection refused") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestDatabaseChecks_ParseErrorOmitsURL(t *testing.T) {
	env := Env{PlatformDatabaseURL: "postgresql://u:s3cr%zzet@db.example.com:5432/x"}
	tcp := (&DatabaseTCPCheck{Dialer: func(context.Context, string) error { return nil }}).Run(context.Background(), env, ContextWheel)
	authRes := (&DatabaseAuthCheck{ConnectFunc: func(context.Context, string) error { return nil }}).Run(context.Background(), env, ContextLocal)
	for name, got := range map[string]CheckResult{"tcp": tcp, "auth": authRes} {
		if !strings.Contains(got.Reason, "could not parse URL") {
			t.Errorf("%s reason: %q", name, got.Reason)
		}
		if strings.Contains(got.Reason+got.Detail, "s3cr") {
			t.Errorf("%s output leaks the password: reason=%q detail=%q", name, got.Reason, got.Detail)
		}
	}
}

func TestDatabaseAuthCheck_PassAndStripDriverSuffix(t *testing.T) {
	var seenDSN string
	check := &DatabaseAuthCheck{ConnectFunc: func(_ context.Context, dsn string) error {
		seenDSN = dsn
		return nil
	}}
	env := Env{PlatformDatabaseURL: "postgresql+psycopg2://u:p@db.example.com:5432/x"}
	got := check.Run(context.Background(), env, ContextLocal)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
	if !strings.HasPrefix(seenDSN, "postgresql://") {
		t.Errorf("dsn: got %q, want postgresql:// prefix", seenDSN)
	}
}

func TestDatabaseAuthCheck_WarnOnConnectError(t *testing.T) {
	check := &DatabaseAuthCheck{ConnectFunc: func(context.Context, string) error {
		return errors.New("password authentication failed for user u\nmore details below")
	}}
	got := check.Run(context.Background(), Env{PlatformDatabaseURL: goodDB}, ContextLocal)
	if got.Status != StatusWarn {
		t.Errorf("status: got %s, want WARN", got.Status)
	}
	if !strings.Contains(got.Reason, "password authentication failed") {
		t.Errorf("reason: %q", got.Reason)
	}
	if strings.Contains(got.Reason, "more details below") {
		t.Errorf("reason should be truncated to first line: %q", got.Reason)
	}
}

func TestStripDriverSuffix(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"postgresql+psycopg2://x", "postgresql://x"},
		{"postgresql://x", "postgresql://x"},
		{"sqlite+aiosqlite:///x.db", "sqlite:///x.db"},
		{"postgres://x", "postgres://x"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stripDriverSuffix(tt.in); got != tt.out {
				t.Errorf("got %q, want %q", got, tt.out)
			}
		})
	}
}
