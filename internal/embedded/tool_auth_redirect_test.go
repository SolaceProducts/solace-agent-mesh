// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

func TestConfigureToolAuthRedirectURI(t *testing.T) {
	t.Run("loopback bind pins redirect to actual host and port", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("127.0.0.1", 9123, false, false)

		if got, want := toolauth.ResolveRedirectURI(), "http://127.0.0.1:9123/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("single-port TLS resolves an https redirect on that port", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("127.0.0.1", 8443, true, false)

		if got, want := toolauth.ResolveRedirectURI(), "https://127.0.0.1:8443/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("dual-port TLS keeps the callback on the plain-HTTP port", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("127.0.0.1", 8800, true, true)

		if got, want := toolauth.ResolveRedirectURI(), "http://127.0.0.1:8800/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("IPv6 loopback bind brackets the host", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("::1", 9123, false, false)

		if got, want := toolauth.ResolveRedirectURI(), "http://[::1]:9123/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("explicit OAUTH_TOOL_REDIRECT_URI is never overridden", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "https://ops.example.com/api/v1/auth/tool/callback")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("127.0.0.1", 9123, false, false)

		if got, want := toolauth.ResolveRedirectURI(), "https://ops.example.com/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("explicit FRONTEND_REDIRECT_URI is never overridden", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "https://frontend.example.com")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("127.0.0.1", 9123, false, false)

		if got, want := toolauth.ResolveRedirectURI(), "https://frontend.example.com/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("non-loopback bind leaves the redirect for the operator to set", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")

		inst := newTestInstance(Config{})
		inst.configureToolAuthRedirectURI("0.0.0.0", 9123, false, false)

		if got, want := toolauth.ResolveRedirectURI(), "http://127.0.0.1:8800/api/v1/auth/tool/callback"; got != want {
			t.Errorf("ResolveRedirectURI() = %q, want %q (default, no env pinned)", got, want)
		}
	})
}
