// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const tlsExpiryWarnDays = 30

type TLSCheck struct {
	Now func() time.Time
}

func NewTLSCheck() *TLSCheck { return &TLSCheck{} }

func (TLSCheck) Name() string { return "tls_certificate" }

func (TLSCheck) DisplayName() string { return "TLS Certificate" }

func (c *TLSCheck) Run(_ context.Context, env Env, _ Context) CheckResult {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}

	certPEM, source, err := readCertSource(env)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CheckResult{Name: c.DisplayName(), Status: StatusFail, Reason: fmt.Sprintf("TLS cert file not found: %v", err)}
		}
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Reason: fmt.Sprintf("Failed to load TLS cert: %v", err)}
	}
	if certPEM == nil {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusSkip,
			Reason: "No TLS certificate configured (TLS_CERT_PATH and TLS_CERT not set)",
		}
	}

	cert, err := parsePEMCertificate(certPEM)
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Reason: fmt.Sprintf("Failed to load TLS cert: %v", err)}
	}

	notAfter := cert.NotAfter
	daysRemaining := int(notAfter.Sub(now()).Hours() / 24)
	dnsName := env.TLSDNSName
	expDate := notAfter.UTC().Format("2006-01-02")
	detail := fmt.Sprintf("expires %s (%s)", expDate, source)
	if dnsName != "" {
		detail = fmt.Sprintf("%s, expires %s (%s)", dnsName, expDate, source)
	}

	if daysRemaining < 0 {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusFail,
			Detail: detail,
			Reason: fmt.Sprintf("Certificate expired %d days ago (expired %s)", -daysRemaining, expDate),
		}
	}

	nearExpiry := daysRemaining <= tlsExpiryWarnDays
	nearExpiryReason := ""
	if nearExpiry {
		nearExpiryReason = fmt.Sprintf("Certificate expires in %d days (%s)", daysRemaining, expDate)
	}

	if dnsName == "" {
		reason := "TLS_DNS_NAME not set, skipping CN/SAN validation"
		if nearExpiry {
			reason = nearExpiryReason + "; " + reason
		}
		return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: detail, Reason: reason}
	}

	names := certNames(cert)
	if !matchHostname(dnsName, names) {
		listed := "none"
		if len(names) > 0 {
			listed = strings.Join(names, ", ")
		}
		reason := fmt.Sprintf("Certificate CN/SAN does not match '%s' (found: %s)", dnsName, listed)
		if nearExpiry {
			reason = nearExpiryReason + "; " + reason
		}
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: detail, Reason: reason}
	}

	if nearExpiry {
		return CheckResult{Name: c.DisplayName(), Status: StatusWarn, Detail: detail, Reason: nearExpiryReason}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: detail}
}

func readCertSource(env Env) ([]byte, string, error) {
	if env.TLSCertPath != "" {
		data, err := os.ReadFile(env.TLSCertPath)
		if err != nil {
			return nil, "", err
		}
		return data, env.TLSCertPath, nil
	}
	if env.TLSCert != "" {
		return []byte(env.TLSCert), "TLS_CERT (inline)", nil
	}
	return nil, "", nil
}

func parsePEMCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func certNames(cert *x509.Certificate) []string {
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames
	}
	if cn := cert.Subject.CommonName; cn != "" {
		return []string{cn}
	}
	return nil
}

func matchHostname(hostname string, certNames []string) bool {
	hostname = strings.ToLower(hostname)
	for _, name := range certNames {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "*.") {
			suffix := name[1:]
			i := strings.Index(hostname, ".")
			if i < 0 {
				continue
			}
			label, rest := hostname[:i], hostname[i:]
			if rest == suffix && !strings.Contains(label, ".") && label != "" {
				return true
			}
			continue
		}
		if name == hostname {
			return true
		}
	}
	return false
}
