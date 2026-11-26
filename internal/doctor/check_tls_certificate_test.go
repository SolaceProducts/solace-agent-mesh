// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type certOpts struct {
	NotAfter   time.Time
	DNSNames   []string
	CommonName string
}

func generateTestCertPEM(t *testing.T, opts certOpts) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: opts.CommonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     opts.NotAfter,
		DNSNames:     opts.DNSNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestTLSCheck(t *testing.T) {
	now := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		opts       certOpts
		dnsName    string
		wantStatus Status
		wantReason string
	}{
		{
			name:       "valid match",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "api.example.com",
			wantStatus: StatusPass,
		},
		{
			name:       "valid wildcard match single label",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), DNSNames: []string{"*.example.com"}},
			dnsName:    "api.example.com",
			wantStatus: StatusPass,
		},
		{
			name:       "wildcard does NOT match multi-label",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), DNSNames: []string{"*.example.com"}},
			dnsName:    "api.sub.example.com",
			wantStatus: StatusFail,
			wantReason: "does not match",
		},
		{
			name:       "no DNS name set warns",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "",
			wantStatus: StatusWarn,
			wantReason: "TLS_DNS_NAME not set",
		},
		{
			name:       "expired",
			opts:       certOpts{NotAfter: now.Add(-24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "api.example.com",
			wantStatus: StatusFail,
			wantReason: "expired",
		},
		{
			name:       "near expiry warns",
			opts:       certOpts{NotAfter: now.Add(15 * 24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "api.example.com",
			wantStatus: StatusWarn,
			wantReason: "expires in",
		},
		{
			name:       "near expiry plus mismatch fails",
			opts:       certOpts{NotAfter: now.Add(15 * 24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "other.example.com",
			wantStatus: StatusFail,
			wantReason: "does not match",
		},
		{
			name:       "CN fallback when no SANs",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), CommonName: "api.example.com"},
			dnsName:    "api.example.com",
			wantStatus: StatusPass,
		},
		{
			name:       "san mismatch fails",
			opts:       certOpts{NotAfter: now.Add(60 * 24 * time.Hour), DNSNames: []string{"api.example.com"}},
			dnsName:    "other.example.com",
			wantStatus: StatusFail,
			wantReason: "does not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pemBytes := generateTestCertPEM(t, tt.opts)
			env := Env{TLSCert: string(pemBytes), TLSDNSName: tt.dnsName}
			c := &TLSCheck{Now: func() time.Time { return now }}
			got := c.Run(context.Background(), env, ContextLocal)
			if got.Status != tt.wantStatus {
				t.Errorf("status: got %s, want %s; reason=%q", got.Status, tt.wantStatus, got.Reason)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("reason: got %q, want contains %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestTLSCheck_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	pemBytes := generateTestCertPEM(t, certOpts{
		NotAfter: time.Now().Add(60 * 24 * time.Hour),
		DNSNames: []string{"x.example.com"},
	})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{TLSCertPath: path, TLSDNSName: "x.example.com"}
	got := NewTLSCheck().Run(context.Background(), env, ContextLocal)
	if got.Status != StatusPass {
		t.Errorf("status: got %s, want PASS; reason=%q", got.Status, got.Reason)
	}
}

func TestTLSCheck_FileNotFound(t *testing.T) {
	env := Env{TLSCertPath: "/no/such/file.pem"}
	got := NewTLSCheck().Run(context.Background(), env, ContextLocal)
	if got.Status != StatusFail {
		t.Errorf("status: got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "not found") {
		t.Errorf("reason: %q", got.Reason)
	}
}

func TestTLSCheck_SkipWhenUnset(t *testing.T) {
	got := NewTLSCheck().Run(context.Background(), Env{}, ContextLocal)
	if got.Status != StatusSkip {
		t.Errorf("status: got %s, want SKIP", got.Status)
	}
}

func TestMatchHostname(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		certNames []string
		want      bool
	}{
		{"exact match", "api.example.com", []string{"api.example.com"}, true},
		{"case insensitive", "API.EXAMPLE.COM", []string{"api.example.com"}, true},
		{"wildcard single label", "api.example.com", []string{"*.example.com"}, true},
		{"wildcard multi-label rejected", "api.sub.example.com", []string{"*.example.com"}, false},
		{"wildcard no leading label", "example.com", []string{"*.example.com"}, false},
		{"no names", "x", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchHostname(tt.host, tt.certNames); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
