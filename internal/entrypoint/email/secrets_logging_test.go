// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSecretsNotLogged(t *testing.T) {
	t.Parallel()
	const (
		basicPwd     = "PWD-SHOULD-NEVER-LEAK-7c3f"
		oauthSecret  = "SECRET-SHOULD-NEVER-LEAK-9a2b"
		smtpPassword = "SMTP-PWD-SHOULD-NEVER-LEAK-d18e"
	)

	t.Run("imap basic-auth connect failure", func(t *testing.T) {
		t.Parallel()
		buf, logger := captureSlog()
		p := &ImapProvider{
			Host:     "127.0.0.1",
			Port:     1,
			Mailbox:  "INBOX",
			Username: "user@example.com",
			Password: basicPwd,
			Logger:   logger,
		}
		_ = p.Connect(t.Context())
		assertNoLeak(t, buf, basicPwd)
	})

	t.Run("imap oauth2 connect failure (no TokenSource)", func(t *testing.T) {
		t.Parallel()
		buf, logger := captureSlog()
		p := &ImapProvider{
			Host:               "127.0.0.1",
			Port:               1,
			Mailbox:            "INBOX",
			Username:           "user@example.com",
			OAuth2ClientSecret: oauthSecret,
			UseOAuth2:          true,
			Logger:             logger,
		}
		_ = p.Connect(t.Context())
		assertNoLeak(t, buf, oauthSecret)
	})

	t.Run("smtp reply against stub without STARTTLS", func(t *testing.T) {
		t.Parallel()
		buf, logger := captureSlog()
		addr := startSMTPStub(t, false)
		host, port := splitHostPort(t, addr)
		s := &SMTPReplySender{
			Host:     host,
			Port:     port,
			Username: "u@example.com",
			Password: smtpPassword,
			From:     "u@example.com",
			Logger:   logger,
		}
		_ = s.Send(t.Context(), ReplyRequest{
			To: "user@example.com", Subject: "Hi", BodyText: "body",
		})
		assertNoLeak(t, buf, smtpPassword)
	})
}

func captureSlog() (*lockedBuffer, *slog.Logger) {
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf, logger
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func assertNoLeak(t *testing.T, buf *lockedBuffer, secret string) {
	t.Helper()
	got := buf.String()
	if strings.Contains(got, secret) {
		t.Fatalf("secret %q leaked into log output:\n%s", secret, got)
	}
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"normal address elides local-part", "alice@corp.com", "***@corp.com"},
		{"subdomain preserved", "bob@mail.example.co.uk", "***@mail.example.co.uk"},
		{"plus-addressing in local-part elided", "alice+tag@corp.com", "***@corp.com"},
		{"empty stays empty", "", ""},
		{"no at-sign is fully masked", "not-an-email", "***"},
		{"only domain after last at", "a@b@corp.com", "***@corp.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := maskEmail(tc.in); got != tc.want {
				t.Fatalf("maskEmail(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.in != "" && strings.Contains(maskEmail(tc.in), "alice") {
				t.Fatalf("maskEmail(%q) leaked the local-part: %q", tc.in, maskEmail(tc.in))
			}
		})
	}
}

func TestSecretsNotLogged_SelfCheck(t *testing.T) {
	t.Parallel()
	const secret = "SENTINEL-LEAK-9c1f"
	buf, logger := captureSlog()
	logger.LogAttrs(context.Background(), slog.LevelInfo, "test", slog.String("password", secret))
	got := buf.String()
	if !strings.Contains(got, secret) {
		t.Fatalf("captureSlog did not capture an intentional leak — assertNoLeak would always pass.\nbuf:\n%s", got)
	}
}
