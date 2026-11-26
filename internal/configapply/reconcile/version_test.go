// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func gatewayPV(version, minClient string) *PlatformVersion {
	return &PlatformVersion{
		Products:         []ProductVersion{{ID: gatewayProductID, Version: version}},
		MinClientVersion: minClient,
	}
}

func TestEvaluateVersionCompat(t *testing.T) {
	tests := []struct {
		name    string
		client  string
		pv      *PlatformVersion
		want    CompatLevel
		msgPart string
	}{
		{name: "dev client skips", client: "dev", pv: gatewayPV("main-v2.65.8", ""), want: CompatOK},
		{name: "dev server skips", client: "main-v2.65.8", pv: gatewayPV("dev", ""), want: CompatOK},
		{name: "malformed server version skips", client: "main-v2.65.8", pv: gatewayPV("garbage", ""), want: CompatOK},
		{name: "nil pv skips", client: "main-v2.65.8", pv: nil, want: CompatOK},
		{name: "equal is silent", client: "main-v2.65.8", pv: gatewayPV("main-v2.65.8", ""), want: CompatOK},
		{name: "client newer is silent", client: "main-v2.66.0", pv: gatewayPV("main-v2.65.8", ""), want: CompatOK},
		{name: "patch behind is silent", client: "main-v2.65.1", pv: gatewayPV("main-v2.65.8", ""), want: CompatOK},
		{name: "minor behind warns", client: "main-v2.64.9", pv: gatewayPV("main-v2.65.8", ""), want: CompatWarn},
		{name: "major behind is fatal", client: "main-v1.99.9", pv: gatewayPV("main-v2.0.0", ""), want: CompatFatal, msgPart: "major version behind"},
		{name: "below floor is fatal", client: "main-v2.60.0", pv: gatewayPV("main-v2.65.8", "main-v2.65.0"), want: CompatFatal, msgPart: "min v2.65.0"},
		{name: "at floor with patch lag is silent", client: "main-v2.65.0", pv: gatewayPV("main-v2.65.8", "main-v2.65.0"), want: CompatOK},
		{name: "floor beats minor when below", client: "main-v2.64.0", pv: gatewayPV("main-v2.65.8", "main-v2.65.0"), want: CompatFatal, msgPart: "min v2.65.0"},
		{name: "git describe ahead suffix normalizes", client: "main-v2.65.8-5-gabc123", pv: gatewayPV("main-v2.65.8", ""), want: CompatOK},
		{name: "no gateway product skips", client: "main-v2.0.0", pv: &PlatformVersion{Products: []ProductVersion{{ID: "sam-str-go", Version: "main-v1.0.0"}}}, want: CompatOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			level, msg := EvaluateVersionCompat(tc.client, tc.pv)
			if level != tc.want {
				t.Fatalf("level = %v, want %v (msg=%q)", level, tc.want, msg)
			}
			if tc.msgPart != "" && !strings.Contains(msg, tc.msgPart) {
				t.Errorf("msg = %q, want it to contain %q", msg, tc.msgPart)
			}
			if level == CompatOK && msg != "" {
				t.Errorf("CompatOK should have empty msg, got %q", msg)
			}
		})
	}
}

func TestNormalizeSemver(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"dev", "", false},
		{"", "", false},
		{"   ", "", false},
		{"deadbeef", "", false},
		{"main-v2.65.8", "v2.65.8", true},
		{"main-v2.65.8-5-gabc123", "v2.65.8", true},
		{"v2.65.8", "v2.65.8", true},
		{"2.65.8", "", false},
		{"192.168.1.1", "", false},
		{"str-v1.16.2", "v1.16.2", true},
	}
	for _, tc := range tests {
		got, ok := normalizeSemver(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("normalizeSemver(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestFetchVersion(t *testing.T) {
	var declared string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		declared = r.Header.Get(httputil.HeaderAPIVersion)
		if r.URL.Path != versionEndpoint {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products":[{"id":"sam-gateway-go","version":"main-v2.65.8"}],"minClientVersion":"main-v2.0.0","apiVersion":"v1"}`))
	}))
	defer srv.Close()

	c := NewClient(target.Resolved{URL: srv.URL})
	pv, err := c.FetchVersion(t.Context())
	if err != nil {
		t.Fatalf("FetchVersion: %v", err)
	}
	if got := pv.gatewayVersion(); got != "main-v2.65.8" {
		t.Errorf("gatewayVersion = %q, want main-v2.65.8", got)
	}
	if pv.MinClientVersion != "main-v2.0.0" {
		t.Errorf("MinClientVersion = %q, want main-v2.0.0", pv.MinClientVersion)
	}
	if declared != samversion.APIVersion {
		t.Errorf("SAM-Version header = %q, want %q", declared, samversion.APIVersion)
	}
}

func TestFetchVersion_OmittedOptionalFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"products":[{"id":"sam-gateway-go","version":"main-v2.65.8"}]}`))
	}))
	defer srv.Close()

	c := NewClient(target.Resolved{URL: srv.URL})
	pv, err := c.FetchVersion(t.Context())
	if err != nil {
		t.Fatalf("FetchVersion: %v", err)
	}
	if pv.MinClientVersion != "" || pv.APIVersion != "" {
		t.Errorf("optional fields should be empty when omitted, got %+v", pv)
	}
	if level, _ := EvaluateVersionCompat("main-v2.65.8", pv); level != CompatOK {
		t.Errorf("no-floor equal versions = %v, want CompatOK", level)
	}
}

func TestFetchVersion_MissingEndpointErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(target.Resolved{URL: srv.URL})
	if _, err := c.FetchVersion(t.Context()); err == nil {
		t.Fatal("expected error for 404 version endpoint, got nil")
	}
}
