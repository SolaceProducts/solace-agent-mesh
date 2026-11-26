// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type graphFixture struct {
	tokenStatus int
	aadstsBody  string
}

func (f *graphFixture) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		if f.tokenStatus != 0 && f.tokenStatus != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.tokenStatus)
			body := f.aadstsBody
			if body == "" {
				body = `{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided."}`
			}
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type smtpReplyFixture struct {
	rejectAuth bool
}

func (f *smtpReplyFixture) serve(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	tlsCfg := selfSignedSMTPProbeTLSConfig(t)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = serveOneSMTPProbeClient(conn, tlsCfg, f.rejectAuth)
	}()
	return ln.Addr().String()
}

func serveOneSMTPProbeClient(c net.Conn, tlsCfg *tls.Config, rejectAuth bool) error {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)
	w := func(s string) error {
		if _, err := bw.WriteString(s); err != nil {
			return err
		}
		return bw.Flush()
	}
	if err := w("220 probe-stub ESMTP\r\n"); err != nil {
		return err
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if err := w("250-probe-stub greets\r\n250-STARTTLS\r\n250-AUTH PLAIN\r\n250 SIZE 10485760\r\n"); err != nil {
				return err
			}
		case cmd == "STARTTLS":
			if err := w("220 ready\r\n"); err != nil {
				return err
			}
			tlsConn := tls.Server(c, tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return err
			}
			c = tlsConn
			br = bufio.NewReader(c)
			bw = bufio.NewWriter(c)
		case strings.HasPrefix(cmd, "AUTH"):
			if rejectAuth {
				if err := w("535 5.7.8 Authentication failed\r\n"); err != nil {
					return err
				}
			} else {
				if err := w("235 2.7.0 Authentication successful\r\n"); err != nil {
					return err
				}
			}
		case cmd == "QUIT":
			_ = w("221 bye\r\n")
			return nil
		case cmd == "":
			continue
		default:
			if err := w("502 unsupported\r\n"); err != nil {
				return err
			}
		}
	}
}

func selfSignedSMTPProbeTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"127.0.0.1", "localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true} //nolint:gosec // self-signed fixture used by in-process SMTP probe tests only.
}

func splitHostPortForTest(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port not numeric: %q", portStr)
	}
	return host, port
}
