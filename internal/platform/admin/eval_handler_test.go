// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvalGenGatewayURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		origin         string
		host           string
		forwardedHost  string
		forwardedProto string
		tls            bool
		want           string
		wantErr        bool
	}{
		{
			name:   "http origin used verbatim",
			origin: "http://localhost:5173",
			host:   "ignored:9999",
			want:   "http://localhost:5173",
		},
		{
			name:   "https origin used verbatim",
			origin: "https://app.example.com",
			host:   "ignored:9999",
			want:   "https://app.example.com",
		},
		{
			name:   "origin trailing slash trimmed",
			origin: "https://app.example.com/",
			want:   "https://app.example.com",
		},
		{
			name:   "wails origin falls through to host",
			origin: "wails://wails",
			host:   "127.0.0.1:8000",
			want:   "http://127.0.0.1:8000",
		},
		{
			name:   "file origin falls through to host",
			origin: "file://",
			host:   "127.0.0.1:8000",
			want:   "http://127.0.0.1:8000",
		},
		{
			name: "no origin uses host",
			host: "gw.internal:8080",
			want: "http://gw.internal:8080",
		},
		{
			name: "no origin with TLS uses https",
			host: "gw.internal:8443",
			tls:  true,
			want: "https://gw.internal:8443",
		},
		{
			name:           "X-Forwarded-Host preferred over Host",
			host:           "internal:8080",
			forwardedHost:  "public.example.com",
			forwardedProto: "https",
			want:           "https://public.example.com",
		},
		{
			name:           "garbage X-Forwarded-Proto ignored",
			host:           "gw:8080",
			forwardedProto: "wails",
			want:           "http://gw:8080",
		},
		{
			name:    "no origin, no host returns error",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/platform/evaluations/datasets/generate", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.forwardedHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}

			got, err := evalGenGatewayURL(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("evalGenGatewayURL: want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("evalGenGatewayURL: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("evalGenGatewayURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvalGenConfiguredGatewayURL(t *testing.T) {
	cases := []struct {
		name           string
		configured     string
		origin         string
		host           string
		forwardedHost  string
		forwardedProto string
		tls            bool
		want           string
		wantErr        bool
	}{
		{name: "the configured address wins over every header", configured: "https://sam.example.com/", origin: "http://169.254.169.254", host: "internal:8080", forwardedHost: "169.254.169.254", want: "https://sam.example.com"},
		{name: "a malformed configured address is an error", configured: "sam.example.com", host: "internal:8080", wantErr: true},
		{name: "a configured address with a path is an error", configured: "https://sam.example.com/app", host: "internal:8080", wantErr: true},
		{name: "a configured address with userinfo is an error", configured: "https://u:p@sam.example.com", host: "internal:8080", wantErr: true},
		{name: "without one, the host the request arrived at", host: "app.example.com", want: "http://app.example.com"},
		{name: "X-Forwarded-Host never picks the host", host: "app.example.com", forwardedHost: "169.254.169.254", want: "http://app.example.com"},
		{name: "Origin never picks the host", host: "app.example.com", origin: "http://169.254.169.254", want: "http://app.example.com"},
		{name: "a comma-separated X-Forwarded-Host is ignored", host: "app.example.com", forwardedHost: "a.example.com, b.example.com", want: "http://app.example.com"},
		{name: "TLS gives https", host: "app.example.com", tls: true, want: "https://app.example.com"},
		{name: "a forwarded https proto gives https", host: "app.example.com", forwardedProto: "https", want: "https://app.example.com"},
		{name: "a forwarded proto that is not https is ignored", host: "app.example.com", forwardedProto: "wails", want: "http://app.example.com"},
		{name: "no host is an error", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.configured)
			r := httptest.NewRequest(http.MethodPut, "/api/v2/platform/evaluations/datasets/generate", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.forwardedHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.forwardedProto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got, err := evalGenConfiguredGatewayURL(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("evalGenConfiguredGatewayURL = %q, want %q", got, tc.want)
			}
		})
	}
}
