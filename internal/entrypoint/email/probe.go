// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2/clientcredentials"
)

const probeBudget = 5 * time.Second

type ProbeOptions struct {
	GraphTokenURLTemplate string
	InsecureSMTPTLS       bool
}

func (o ProbeOptions) graphTokenURL(tenant string) string {
	tmpl := o.GraphTokenURLTemplate
	if tmpl == "" {
		tmpl = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	}
	if !strings.Contains(tmpl, "%s") {
		return tmpl
	}
	return fmt.Sprintf(tmpl, tenant)
}

type ProbeResult struct {
	Success    bool
	ErrorClass ErrorClass
	Hint       string
}

func ProbeReply(ctx context.Context, cfg *EmailConfig, opts ProbeOptions) ProbeResult {
	if cfg == nil {
		return noReplyTransport()
	}
	if cfg.UsesOAuth2() {
		return probeGraphReply(ctx, cfg, opts)
	}
	if cfg.UsesBasicAuth() {
		return probeSMTPReply(ctx, cfg, opts)
	}
	return noReplyTransport()
}

func noReplyTransport() ProbeResult {
	return ProbeResult{
		Success:    false,
		ErrorClass: ErrClassUnknown,
		Hint:       "Email replying transport not configured (missing OAuth2 triple or SMTP credentials).",
	}
}

func probeGraphReply(ctx context.Context, cfg *EmailConfig, opts ProbeOptions) ProbeResult {
	if cfg.OAuth2TenantID == "" || cfg.OAuth2ClientID == "" || cfg.OAuth2ClientSecret == "" {
		return ProbeResult{
			Success:    false,
			ErrorClass: ErrClassUnknown,
			Hint:       "Tenant ID, client ID, and client secret are required for OAuth2 reply.",
		}
	}

	cc := clientcredentials.Config{
		ClientID:     cfg.OAuth2ClientID,
		ClientSecret: cfg.OAuth2ClientSecret,
		TokenURL:     opts.graphTokenURL(cfg.OAuth2TenantID),
		Scopes:       []string{"https://graph.microsoft.com/.default"},
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()

	if _, err := cc.Token(probeCtx); err != nil {
		class, hint := ClassifyConnectionError(err)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}
	return ProbeResult{Success: true}
}

func probeSMTPReply(ctx context.Context, cfg *EmailConfig, opts ProbeOptions) ProbeResult {
	host := cfg.SMTPHost
	port := cfg.SMTPPort
	username := cfg.Mailbox
	password := cfg.IMAPPassword

	if host == "" || port <= 0 || username == "" || password == "" {
		return ProbeResult{
			Success:    false,
			ErrorClass: ErrClassUnknown,
			Hint:       "SMTP host, port, mailbox, and password are required.",
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: probeBudget}
	conn, err := dialer.DialContext(probeCtx, "tcp", addr)
	if err != nil {
		class, hint := ClassifyConnectionError(err)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}
	_ = conn.SetDeadline(time.Now().Add(probeBudget))
	closeConn := conn.Close
	defer func() {
		if closeConn != nil {
			_ = closeConn()
		}
	}()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		class, hint := ClassifyConnectionError(err)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}
	closeConn = nil
	defer func() { _ = c.Close() }()

	if helloErr := c.Hello("localhost"); helloErr != nil {
		class, hint := ClassifyConnectionError(helloErr)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}

	if ok, _ := c.Extension("STARTTLS"); !ok {
		return ProbeResult{
			Success:    false,
			ErrorClass: ErrClassStartTLSUnsupported,
			Hint:       fmt.Sprintf("SMTP server %s does not advertise STARTTLS. Refusing to send credentials over plaintext.", host),
		}
	}
	tlsCfg := &tls.Config{ServerName: host, InsecureSkipVerify: opts.InsecureSMTPTLS, MinVersion: tls.VersionTLS12} //nolint:gosec // InsecureSkipVerify is opt-in via ProbeOptions for in-process self-signed test fixtures only; production callers leave it false.
	if err := c.StartTLS(tlsCfg); err != nil {
		class, hint := ClassifyConnectionError(err)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}

	if ok, _ := c.Extension("AUTH"); !ok {
		return ProbeResult{
			Success:    false,
			ErrorClass: ErrClassUnknown,
			Hint:       "SMTP server does not advertise AUTH after STARTTLS. Cannot authenticate.",
		}
	}

	auth := smtp.PlainAuth("", username, password, host)
	if err := c.Auth(auth); err != nil {
		class, hint := ClassifyConnectionError(err)
		return ProbeResult{Success: false, ErrorClass: class, Hint: hint}
	}

	_ = c.Quit()
	return ProbeResult{Success: true}
}
