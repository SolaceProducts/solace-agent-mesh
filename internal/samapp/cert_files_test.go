// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"slices"
	"testing"
)

func TestResolveGatewayCertFiles(t *testing.T) {
	t.Run("from app config across multiple apps", func(t *testing.T) {
		apps := []AppEntry{
			{AppConfig: map[string]any{"ssl_certfile": "/etc/certs/a.pem"}},
			{AppConfig: map[string]any{}},
			{AppConfig: map[string]any{"ssl_certfile": "/etc/certs/b.pem"}},
		}
		got := ResolveGatewayCertFiles(apps)
		want := []string{"/etc/certs/a.pem", "/etc/certs/b.pem"}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("dedups a shared ssl_certfile", func(t *testing.T) {
		apps := []AppEntry{
			{AppConfig: map[string]any{"ssl_certfile": "/etc/certs/shared.pem"}},
			{AppConfig: map[string]any{"ssl_certfile": "/etc/certs/shared.pem"}},
		}
		got := ResolveGatewayCertFiles(apps)
		if !slices.Equal(got, []string{"/etc/certs/shared.pem"}) {
			t.Errorf("got %v, want one /etc/certs/shared.pem (deduped)", got)
		}
	})

	t.Run("blank/whitespace ssl_certfile ignored", func(t *testing.T) {
		t.Setenv("SSL_CERTFILE", "")
		apps := []AppEntry{{AppConfig: map[string]any{"ssl_certfile": "   "}}}
		if got := ResolveGatewayCertFiles(apps); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("falls back to SSL_CERTFILE env when no app config", func(t *testing.T) {
		t.Setenv("SSL_CERTFILE", "/env/cert.pem")
		apps := []AppEntry{{AppConfig: map[string]any{}}}
		got := ResolveGatewayCertFiles(apps)
		if !slices.Equal(got, []string{"/env/cert.pem"}) {
			t.Errorf("got %v, want [/env/cert.pem]", got)
		}
	})

	t.Run("app config wins over env", func(t *testing.T) {
		t.Setenv("SSL_CERTFILE", "/env/cert.pem")
		apps := []AppEntry{{AppConfig: map[string]any{"ssl_certfile": "/cfg/cert.pem"}}}
		got := ResolveGatewayCertFiles(apps)
		if !slices.Equal(got, []string{"/cfg/cert.pem"}) {
			t.Errorf("got %v, want [/cfg/cert.pem] (config takes precedence)", got)
		}
	})

	t.Run("nothing configured returns empty", func(t *testing.T) {
		t.Setenv("SSL_CERTFILE", "")
		if got := ResolveGatewayCertFiles([]AppEntry{{AppConfig: map[string]any{}}}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}
