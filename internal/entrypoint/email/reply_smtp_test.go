// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log/slog"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildReply_AddsReprefixWhenMissing(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Help with billing",
		BodyText: "Sure, looking now.",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if !strings.Contains(string(msg), "\r\nSubject: Re: Help with billing\r\n") {
		t.Errorf("Re: prefix not added; msg=\n%s", msg)
	}
}

func TestBuildReply_PreservesExistingReprefix(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Re: Help with billing",
		BodyText: "Sure, looking now.",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	if strings.Count(string(msg), "Subject:") != 1 {
		t.Errorf("expected exactly one Subject header, got %d", strings.Count(string(msg), "Subject:"))
	}
	if !strings.Contains(string(msg), "Subject: Re: Help with billing") {
		t.Errorf("Re: prefix duplicated or removed; msg=\n%s", msg)
	}
}

func TestBuildReply_ThreadingHeaders(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:                 "user@example.com",
		Subject:            "Q1",
		BodyText:           "A1",
		OriginalMessageID:  "<orig@vendor.com>",
		OriginalReferences: []string{"<a@vendor.com>", "<b@vendor.com>"},
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	s := string(msg)
	if !strings.Contains(s, "\r\nIn-Reply-To: <orig@vendor.com>\r\n") {
		t.Errorf("In-Reply-To missing or malformed; msg=\n%s", s)
	}
	if !strings.Contains(s, "\r\nReferences: <a@vendor.com> <b@vendor.com> <orig@vendor.com>\r\n") {
		t.Errorf("References chain wrong; msg=\n%s", s)
	}
}

func TestBuildReply_NoOriginalMessageIDOmitsThreading(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Help",
		BodyText: "Sure.",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	s := string(msg)
	if strings.Contains(s, "In-Reply-To:") || strings.Contains(s, "References:") {
		t.Errorf("threading headers should be omitted with no OriginalMessageID; msg=\n%s", s)
	}
}

func TestBuildReply_StripsCRLFFromAttackerControlledFields(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:                "user@example.com",
		Subject:           "Hi\r\nBcc: victim@evil.com",
		BodyText:          "ok",
		OriginalMessageID: "<orig@vendor.com>\r\nBcc: another@evil.com",
		OriginalReferences: []string{
			"<ref@vendor.com>\r\nX-Injected: 1",
		},
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	headers, _, ok := strings.Cut(string(msg), "\r\n\r\n")
	if !ok {
		t.Fatalf("malformed message — no header/body separator; msg=\n%s", msg)
	}
	for _, line := range strings.Split(headers, "\r\n") {
		switch {
		case strings.HasPrefix(line, "Bcc:"):
			t.Errorf("CRLF injection landed a Bcc header line; full headers=\n%s", headers)
		case strings.HasPrefix(line, "X-Injected:"):
			t.Errorf("CRLF injection landed an X-Injected header line; full headers=\n%s", headers)
		}
	}
}

func TestBuildReply_NormalizesBareLFInBody(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Hi",
		BodyText: "line1\nline2\nline3",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	_, body, ok := strings.Cut(string(msg), "\r\n\r\n")
	if !ok {
		t.Fatalf("malformed message — no header/body separator")
	}
	if strings.Contains(body, "\n") && !strings.Contains(body, "\r\n") {
		t.Errorf("body line terminators not normalized to CRLF; body=%q", body)
	}
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' && (i == 0 || body[i-1] != '\r') {
			t.Errorf("bare LF at body offset %d; body=%q", i, body)
			break
		}
	}
}

func TestBuildReply_PreservesExistingCRLFInBody(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Hi",
		BodyText: "line1\r\nline2\r\nline3",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	_, body, _ := strings.Cut(string(msg), "\r\n\r\n")
	if strings.Contains(body, "\r\r") {
		t.Errorf("CRLF body got doubled CR; body=%q", body)
	}
}

func TestBuildReply_NormalizesMixedLineEndings(t *testing.T) {
	t.Parallel()
	_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Hi",
		BodyText: "crlf\r\nlf-only\nback-to-crlf\r\n",
	})
	if err != nil {
		t.Fatalf("buildReply: %v", err)
	}
	_, body, _ := strings.Cut(string(msg), "\r\n\r\n")
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' && (i == 0 || body[i-1] != '\r') {
			t.Errorf("bare LF at body offset %d; body=%q", i, body)
			return
		}
	}
}

func TestBuildReply_EmptyToReturnsError(t *testing.T) {
	t.Parallel()
	_, _, _, err := buildReply("agent@example.org", ReplyRequest{
		To:       "",
		Subject:  "Hi",
		BodyText: "x",
	})
	if err == nil {
		t.Fatal("expected error for empty To")
	}
}

func TestSend_EmptyBodySkipsAndReturnsNil(t *testing.T) {
	t.Parallel()
	s := &SMTPReplySender{
		Host:     "127.0.0.1",
		Port:     1,
		Username: "u", Password: "p", From: "agent@example.org",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := s.Send(context.Background(), ReplyRequest{
		To: "user@example.com", Subject: "Hi", BodyText: "   \t\n",
	}); err != nil {
		t.Errorf("expected nil for empty body, got %v", err)
	}
}

func TestSend_RefusesPlaintextWhenSTARTTLSNotAdvertised(t *testing.T) {
	t.Parallel()
	addr := startSMTPStub(t, false)
	host, port := splitHostPort(t, addr)
	s := &SMTPReplySender{
		Host: host, Port: port,
		Username: "u", Password: "p", From: "agent@example.org",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	err := s.Send(context.Background(), ReplyRequest{
		To: "user@example.com", Subject: "Hi", BodyText: "body",
	})
	if err == nil {
		t.Fatal("expected error refusing plaintext, got nil")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("error should mention STARTTLS; got %v", err)
	}
}

func TestSend_ProgressesPastEHLOCheckWhenSTARTTLSAdvertised(t *testing.T) {
	t.Parallel()
	addr := startSMTPStub(t, true)
	host, port := splitHostPort(t, addr)
	s := &SMTPReplySender{
		Host: host, Port: port,
		Username: "u", Password: "p", From: "agent@example.org",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	err := s.Send(context.Background(), ReplyRequest{
		To: "user@example.com", Subject: "Hi", BodyText: "body",
	})
	if err == nil {
		t.Fatal("expected TLS verify failure against self-signed stub; got nil")
	}
	if strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("EHLO check incorrectly refused STARTTLS-advertising server: %v", err)
	}
	if !strings.Contains(err.Error(), "starttls") && !strings.Contains(err.Error(), "tls") {
		t.Errorf("expected TLS-stage error, got %v", err)
	}
}

type smtpStubOptions struct {
	advertiseSTARTTLS bool
	failQuit          bool
}

func TestSend_QuitFailureDoesNotShadowSuccessfulDelivery(t *testing.T) {
	t.Parallel()
	addr := startSMTPStubWithOptions(t, smtpStubOptions{
		advertiseSTARTTLS: true,
		failQuit:          true,
	})
	host, port := splitHostPort(t, addr)
	s := &SMTPReplySender{
		Host: host, Port: port,
		Username: "u", Password: "p", From: "agent@example.org",
		Insecure: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := s.Send(context.Background(), ReplyRequest{
		To: "user@example.com", Subject: "Hi", BodyText: "body",
	}); err != nil {
		t.Errorf("Send returned %v; expected nil because DATA was accepted before QUIT failed", err)
	}
}

func TestSend_SucceedsAgainstHealthyStub(t *testing.T) {
	t.Parallel()
	addr := startSMTPStubWithOptions(t, smtpStubOptions{advertiseSTARTTLS: true})
	host, port := splitHostPort(t, addr)
	s := &SMTPReplySender{
		Host: host, Port: port,
		Username: "u", Password: "p", From: "agent@example.org",
		Insecure: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := s.Send(context.Background(), ReplyRequest{
		To: "user@example.com", Subject: "Hi", BodyText: "body",
	}); err != nil {
		t.Errorf("Send returned %v against healthy stub; want nil", err)
	}
}

func startSMTPStub(t *testing.T, advertiseSTARTTLS bool) string {
	return startSMTPStubWithOptions(t, smtpStubOptions{advertiseSTARTTLS: advertiseSTARTTLS})
}

func startSMTPStubWithOptions(t *testing.T, opts smtpStubOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	tlsCfg := selfSignedTLSConfig(t)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = serveOneSMTPClient(conn, tlsCfg, opts)
	}()
	return ln.Addr().String()
}

func serveOneSMTPClient(c net.Conn, tlsCfg *tls.Config, opts smtpStubOptions) error {
	advertiseSTARTTLS := opts.advertiseSTARTTLS
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)
	w := func(s string) error {
		if _, err := bw.WriteString(s); err != nil {
			return err
		}
		return bw.Flush()
	}
	if err := w("220 stub ESMTP\r\n"); err != nil {
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
			resp := "250-stub greets\r\n250-AUTH PLAIN\r\n"
			if advertiseSTARTTLS {
				resp = "250-stub greets\r\n250-STARTTLS\r\n250-AUTH PLAIN\r\n"
			}
			resp += "250 SIZE 10485760\r\n"
			if err := w(resp); err != nil {
				return err
			}
		case cmd == "STARTTLS":
			if !advertiseSTARTTLS {
				return w("502 unsupported\r\n")
			}
			if err := w("220 ready\r\n"); err != nil {
				return err
			}
			if err := bw.Flush(); err != nil {
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
			if err := w("235 ok\r\n"); err != nil {
				return err
			}
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			if err := w("250 ok\r\n"); err != nil {
				return err
			}
		case cmd == "DATA":
			if err := w("354 send\r\n"); err != nil {
				return err
			}
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return err
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
			}
			if err := w("250 queued\r\n"); err != nil {
				return err
			}
		case cmd == "QUIT":
			if opts.failQuit {
				return nil
			}
			_ = w("221 bye\r\n")
			return nil
		case cmd == "":
			continue
		default:
			if err := w("500 unknown\r\n"); err != nil {
				return err
			}
		}
	}
}

func selfSignedTLSConfig(t *testing.T) *tls.Config {
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
	return &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true}
}

func TestSMTPReplySender_SupportsAttachments(t *testing.T) {
	t.Parallel()
	if (&SMTPReplySender{}).SupportsAttachments() {
		t.Error("SMTPReplySender.SupportsAttachments() = true; want false (Gmail-only path is text-only)")
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port not numeric: %q", port)
	}
	return host, p
}

func TestBuildReply_AnAutoReplyCarriesTheAutoResponderHeaders(t *testing.T) {
	t.Parallel()
	for _, autoReply := range []bool{true, false} {
		_, _, msg, err := buildReply("agent@example.org", ReplyRequest{
			To: "user@example.com", Subject: "Help", BodyText: "Refused.", AutoReply: autoReply,
		})
		if err != nil {
			t.Fatalf("buildReply: %v", err)
		}
		for _, header := range []string{"\r\nAuto-Submitted: auto-replied\r\n", "\r\nX-Auto-Response-Suppress: All\r\n"} {
			if got := strings.Contains(string(msg), header); got != autoReply {
				t.Errorf("AutoReply=%v: header %q present = %v; msg=\n%s", autoReply, strings.TrimSpace(header), got, msg)
			}
		}
	}
}
