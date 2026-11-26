// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"testing"
)

func TestFirstHTTPAdapterConfig_PicksFirstWithPort(t *testing.T) {
	entries := []AppEntry{
		{Name: "no-port", AppConfig: map[string]any{"fastapi_host": "0.0.0.0"}},
		{Name: "has-port", AppConfig: map[string]any{
			"fastapi_host":         "0.0.0.0",
			"fastapi_port":         8000,
			"fastapi_https_port":   8443,
			"ssl_certfile":         "/tmp/cert.pem",
			"ssl_keyfile":          "/tmp/key.pem",
			"ssl_keyfile_password": "pw",
		}},
		{Name: "also-has-port", AppConfig: map[string]any{"fastapi_port": 9000}},
	}

	got := FirstHTTPAdapterConfig(entries)
	if got == nil {
		t.Fatal("expected non-nil HTTP adapter config")
	}
	if got.Host != "0.0.0.0" || got.Port != 8000 || got.HTTPSPort != 8443 {
		t.Errorf("got %+v, want host=0.0.0.0 port=8000 https=8443", got)
	}
	if got.SSLCertFile != "/tmp/cert.pem" || got.SSLKeyFile != "/tmp/key.pem" || got.SSLKeyPassword != "pw" {
		t.Errorf("TLS fields = %+v, want cert/key/pw set", got)
	}
}

func TestFirstHTTPAdapterConfig_NilWhenNoPortKey(t *testing.T) {
	entries := []AppEntry{
		{AppConfig: map[string]any{"fastapi_host": "0.0.0.0"}},
		{AppConfig: map[string]any{}},
	}
	if got := FirstHTTPAdapterConfig(entries); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestFirstHTTPAdapterConfig_ZeroPortIsExplicit(t *testing.T) {
	entries := []AppEntry{
		{AppConfig: map[string]any{"fastapi_port": 0, "fastapi_host": "127.0.0.1"}},
	}
	got := FirstHTTPAdapterConfig(entries)
	if got == nil {
		t.Fatal("expected non-nil config for fastapi_port: 0")
	}
	if got.Port != 0 || got.Host != "127.0.0.1" {
		t.Errorf("got %+v, want host=127.0.0.1 port=0", got)
	}
	if got.ListenAddr() != "127.0.0.1:0" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:0 (auto-assign)", got.ListenAddr())
	}
}

func TestFirstHTTPAdapterConfig_FloatPort(t *testing.T) {
	entries := []AppEntry{
		{AppConfig: map[string]any{"fastapi_port": float64(8080)}},
	}
	got := FirstHTTPAdapterConfig(entries)
	if got == nil || got.Port != 8080 {
		t.Errorf("got %+v, want port=8080 (from float64)", got)
	}
}

func TestListenAddr_DefaultHost(t *testing.T) {
	c := &HTTPAdapterConfig{Port: 8000}
	if got := c.ListenAddr(); got != "127.0.0.1:8000" {
		t.Errorf("ListenAddr() = %q, want 127.0.0.1:8000", got)
	}
}

func TestListenAddr_ExplicitHost(t *testing.T) {
	c := &HTTPAdapterConfig{Host: "0.0.0.0", Port: 8080}
	if got := c.ListenAddr(); got != "0.0.0.0:8080" {
		t.Errorf("ListenAddr() = %q, want 0.0.0.0:8080", got)
	}
}

func TestHTTPSAddr_Unset(t *testing.T) {
	c := &HTTPAdapterConfig{Host: "0.0.0.0", Port: 8080}
	if got := c.HTTPSAddr(); got != "" {
		t.Errorf("HTTPSAddr() = %q, want empty", got)
	}
}

func TestHTTPSAddr_Set(t *testing.T) {
	c := &HTTPAdapterConfig{Host: "0.0.0.0", Port: 8080, HTTPSPort: 8443}
	if got := c.HTTPSAddr(); got != "0.0.0.0:8443" {
		t.Errorf("HTTPSAddr() = %q, want 0.0.0.0:8443", got)
	}
}

func TestResolveListenAddr_FlagWins(t *testing.T) {
	cfg := &HTTPAdapterConfig{Host: "0.0.0.0", Port: 8080}
	if got := ResolveListenAddr(":9999", cfg); got != ":9999" {
		t.Errorf("ResolveListenAddr = %q, want :9999 (flag wins)", got)
	}
}

func TestResolveListenAddr_YAMLFallback(t *testing.T) {
	cfg := &HTTPAdapterConfig{Host: "0.0.0.0", Port: 8080}
	if got := ResolveListenAddr("", cfg); got != "0.0.0.0:8080" {
		t.Errorf("ResolveListenAddr = %q, want 0.0.0.0:8080", got)
	}
}

func TestResolveListenAddr_DefaultWhenNoConfig(t *testing.T) {
	if got := ResolveListenAddr("", nil); got != ":8080" {
		t.Errorf("ResolveListenAddr = %q, want :8080", got)
	}
}

func TestResolveProxyTLS_YAMLWins(t *testing.T) {
	t.Setenv("SSL_CERTFILE", "/env/cert.pem")
	t.Setenv("SSL_KEYFILE", "/env/key.pem")
	t.Setenv("SSL_KEYFILE_PASSWORD", "env-pw")
	t.Setenv("FASTAPI_HTTPS_PORT", "9443")

	cfg := &HTTPAdapterConfig{
		Host:           "0.0.0.0",
		Port:           8080,
		HTTPSPort:      8443,
		SSLCertFile:    "/yaml/cert.pem",
		SSLKeyFile:     "/yaml/key.pem",
		SSLKeyPassword: "yaml-pw",
	}
	cert, key, pw, addr := ResolveProxyTLS(cfg)
	if cert != "/yaml/cert.pem" || key != "/yaml/key.pem" || pw != "yaml-pw" {
		t.Errorf("YAML TLS not preferred: cert=%q key=%q pw=%q", cert, key, pw)
	}
	if addr != "0.0.0.0:8443" {
		t.Errorf("tlsAddr = %q, want 0.0.0.0:8443 (from YAML)", addr)
	}
}

func TestResolveProxyTLS_EnvFallback(t *testing.T) {
	t.Setenv("SSL_CERTFILE", "/env/cert.pem")
	t.Setenv("SSL_KEYFILE", "/env/key.pem")
	t.Setenv("SSL_KEYFILE_PASSWORD", "env-pw")
	t.Setenv("FASTAPI_HTTPS_PORT", "9443")

	cert, key, pw, addr := ResolveProxyTLS(nil)
	if cert != "/env/cert.pem" || key != "/env/key.pem" || pw != "env-pw" {
		t.Errorf("env TLS not used: cert=%q key=%q pw=%q", cert, key, pw)
	}
	if addr != "0.0.0.0:9443" {
		t.Errorf("tlsAddr = %q, want 0.0.0.0:9443 (from FASTAPI_HTTPS_PORT)", addr)
	}
}

func TestResolveProxyTLS_NoneConfigured(t *testing.T) {
	t.Setenv("SSL_CERTFILE", "")
	t.Setenv("SSL_KEYFILE", "")
	t.Setenv("FASTAPI_HTTPS_PORT", "")

	cert, key, pw, addr := ResolveProxyTLS(nil)
	if cert != "" || key != "" || pw != "" || addr != "" {
		t.Errorf("expected all empty, got cert=%q key=%q pw=%q addr=%q", cert, key, pw, addr)
	}
}

func TestResolveProxyTLS_YAMLPartialFallsThroughToEnv(t *testing.T) {
	t.Setenv("SSL_CERTFILE", "/env/cert.pem")
	t.Setenv("SSL_KEYFILE", "/env/key.pem")

	cfg := &HTTPAdapterConfig{SSLCertFile: "/yaml/cert.pem"}
	cert, key, _, _ := ResolveProxyTLS(cfg)
	if cert != "/env/cert.pem" || key != "/env/key.pem" {
		t.Errorf("partial YAML should fall through to env, got cert=%q key=%q", cert, key)
	}
}
