// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBrokerState struct{ s atomic.Int64 }

func (f *fakeBrokerState) ConnectionState() int { return int(f.s.Load()) }

func TestRegisterInstanceUp(t *testing.T) {
	reg, reader := newTestRegistry(t)
	RegisterInstanceUp(reg, "GWE")

	dps := collectGauge(t, reader, "test.instance.up")
	if len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("got %+v, want single value 1", dps)
	}
	if v, _ := attrString(dps[0], "component_type"); v != "GWE" {
		t.Errorf("component_type = %q, want GWE", v)
	}
}

func TestRegisterBrokerConnectionState(t *testing.T) {
	reg, reader := newTestRegistry(t)
	brk := &fakeBrokerState{}
	brk.s.Store(1)
	RegisterBrokerConnectionState(reg, "AWE", brk)

	if dps := collectGauge(t, reader, "test.broker.connection.state"); len(dps) != 1 || dps[0].Value != 1 {
		t.Fatalf("connected: got %+v, want value 1", dps)
	}
	if v, _ := attrString(collectGauge(t, reader, "test.broker.connection.state")[0], "component_type"); v != "AWE" {
		t.Errorf("component_type label missing/wrong")
	}

	brk.s.Store(2)
	if dps := collectGauge(t, reader, "test.broker.connection.state"); len(dps) != 1 || dps[0].Value != 2 {
		t.Fatalf("reconnecting: got %+v, want value 2", dps)
	}
}

func TestRegisterComponentCount(t *testing.T) {
	reg, reader := newTestRegistry(t)
	var names atomic.Pointer[[]string]
	initial := []string{"alpha", "beta"}
	names.Store(&initial)
	RegisterComponentCount(reg, "agent", func() []string { return *names.Load() })

	dps := collectGauge(t, reader, "test.component.count")
	if len(dps) != 2 {
		t.Fatalf("got %d series, want 2", len(dps))
	}
	for _, dp := range dps {
		if dp.Value != 1 {
			t.Errorf("value = %v, want 1", dp.Value)
		}
		if k, _ := attrString(dp, "type"); k != "agent" {
			t.Errorf("type = %q, want agent", k)
		}
	}

	reduced := []string{"alpha"}
	names.Store(&reduced)
	if dps := collectGauge(t, reader, "test.component.count"); len(dps) != 1 {
		t.Fatalf("after removal: got %d series, want 1", len(dps))
	}
}

func TestRegisterCertExpiry_EmitsDaysPerCN(t *testing.T) {
	reg, reader := newTestRegistry(t)
	path := writeTestCert(t, "agentmesh.acme.com", 10*24*time.Hour)
	RegisterCertExpiry(reg, path)

	dps := collectGauge(t, reader, "test.tls.certificate.expiry.days")
	if len(dps) != 1 {
		t.Fatalf("got %d series, want 1", len(dps))
	}
	if cn, _ := attrString(dps[0], "common_name"); cn != "agentmesh.acme.com" {
		t.Errorf("common_name = %q, want agentmesh.acme.com", cn)
	}
	if isCA, ok := attrString(dps[0], "is_ca"); !ok || isCA != "false" {
		t.Errorf("is_ca = %q (present=%v), want \"false\"", isCA, ok)
	}
	if dps[0].Value < 9 || dps[0].Value > 10 {
		t.Errorf("days = %v, want ~10", dps[0].Value)
	}
}

func TestRegisterCertExpiry_MultipleCertsMultipleSeries(t *testing.T) {
	reg, reader := newTestRegistry(t)
	wantDays := map[string]float64{
		"api.acme.com":  10,
		"chat.acme.com": 30,
		"soon.acme.com": 1,
	}
	bundle := make(map[string]time.Duration, len(wantDays))
	for cn, d := range wantDays {
		bundle[cn] = time.Duration(d) * 24 * time.Hour
	}
	RegisterCertExpiry(reg, writeCertBundle(t, bundle))

	dps := collectGauge(t, reader, "test.tls.certificate.expiry.days")
	if len(dps) != len(wantDays) {
		t.Fatalf("got %d series, want %d (one per host cert)", len(dps), len(wantDays))
	}
	seen := map[string]bool{}
	for _, dp := range dps {
		cn, _ := attrString(dp, "common_name")
		exp, ok := wantDays[cn]
		if !ok {
			t.Errorf("unexpected common_name %q", cn)
			continue
		}
		seen[cn] = true
		if dp.Value <= exp-0.02 || dp.Value > exp {
			t.Errorf("%s days = %v, want within (%.2f, %.0f]", cn, dp.Value, exp-0.02, exp)
		}
	}
	if len(seen) != len(wantDays) {
		t.Errorf("covered %d of %d common_names: %v", len(seen), len(wantDays), seen)
	}
}

func TestRegisterCertExpiry_FallsBackToSANWhenNoCN(t *testing.T) {
	reg, reader := newTestRegistry(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{},
		DNSNames:     []string{"sanonly.acme.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(15 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sancert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	RegisterCertExpiry(reg, path)
	dps := collectGauge(t, reader, "test.tls.certificate.expiry.days")
	if len(dps) != 1 {
		t.Fatalf("got %d series, want 1", len(dps))
	}
	if cn, _ := attrString(dps[0], "common_name"); cn != "sanonly.acme.com" {
		t.Errorf("common_name = %q, want sanonly.acme.com (SAN fallback for CN-less cert)", cn)
	}
}

func TestRegisterCertExpiry_OversizedFileSkipped(t *testing.T) {
	reg, reader := newTestRegistry(t)
	path := filepath.Join(t.TempDir(), "huge.pem")
	blob := make([]byte, maxCertFileBytes+1)
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterCertExpiry(reg, path)
	if dps := collectGauge(t, reader, "test.tls.certificate.expiry.days"); dps != nil {
		t.Errorf("oversized cert file emitted %d series, want 0 (capped + skipped)", len(dps))
	}
}

func TestRegisterCertExpiry_NoPathsNoMetric(t *testing.T) {
	reg, reader := newTestRegistry(t)
	RegisterCertExpiry(reg)
	RegisterCertExpiry(reg, "", " ")

	if dps := collectGauge(t, reader, "test.tls.certificate.expiry.days"); dps != nil {
		t.Errorf("expected no metric when no cert paths, got %d series", len(dps))
	}
}

func TestRegisterCertExpiry_UnreadableFileNoSeries(t *testing.T) {
	reg, reader := newTestRegistry(t)
	RegisterCertExpiry(reg, filepath.Join(t.TempDir(), "missing.pem"))
	if dps := collectGauge(t, reader, "test.tls.certificate.expiry.days"); dps != nil {
		t.Errorf("unreadable cert path emitted %d series, want 0", len(dps))
	}
}

func TestRegisterCertExpiry_NonCertPEMNoSeries(t *testing.T) {
	reg, reader := newTestRegistry(t)
	path := filepath.Join(t.TempDir(), "key.pem")
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-cert")})
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterCertExpiry(reg, path)
	if dps := collectGauge(t, reader, "test.tls.certificate.expiry.days"); dps != nil {
		t.Errorf("non-cert PEM emitted %d series, want 0", len(dps))
	}
}

func TestRegisterGauges_NilRegistryNoPanic(t *testing.T) {
	RegisterInstanceUp(nil, "GWE")
	RegisterBrokerConnectionState(nil, "GWE", &fakeBrokerState{})
	RegisterComponentCount(nil, "agent", func() []string { return nil })
	RegisterCertExpiry(nil, "/does/not/matter.pem")
}

func certPEM(t *testing.T, cn string, validFor time.Duration) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validFor),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeTestCert(t *testing.T, cn string, validFor time.Duration) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, certPEM(t, cn, validFor), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path
}

func writeCertBundle(t *testing.T, cns map[string]time.Duration) string {
	t.Helper()
	var buf []byte
	for cn, d := range cns {
		buf = append(buf, certPEM(t, cn, d)...)
	}
	path := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path
}

func TestRegisterGatewayInFlightTasks(t *testing.T) {
	reg, reader := newTestRegistry(t)
	var n atomic.Int64
	RegisterGatewayInFlightTasks(reg, func() int { return int(n.Load()) })

	n.Store(3)
	dps := collectGauge(t, reader, "test.gateway.tasks.in_flight")
	if len(dps) != 1 || dps[0].Value != 3 {
		t.Fatalf("got %+v, want single value 3", dps)
	}
}

func TestRegisterGatewayInFlightTasks_NilSafe(t *testing.T) {
	RegisterGatewayInFlightTasks(nil, func() int { return 1 })
	reg, _ := newTestRegistry(t)
	RegisterGatewayInFlightTasks(reg, nil)
}
