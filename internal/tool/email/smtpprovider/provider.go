// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package smtpprovider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type TLSMode string

const (
	TLSModeNone     TLSMode = "none"
	TLSModeSTARTTLS TLSMode = "starttls"
	TLSModeTLS      TLSMode = "tls"
)

type AuthType string

const (
	AuthTypeNone           AuthType = "none"
	AuthTypePlain          AuthType = "plain"
	AuthTypeLogin          AuthType = "login"
	AuthTypeOAuthMicrosoft AuthType = "oauth_microsoft"
	AuthTypeOAuthGoogle    AuthType = "oauth_google"
)

type Config struct {
	Host     string
	Port     int
	TLS      TLSMode
	AuthType AuthType
	Username string
	Password string
	OAuth    *OAuthConfig
}

type OAuthConfig struct {
	Provider string

	TenantID     string
	ClientID     string
	ClientSecret string

	RefreshToken string

	Email string
}

type Message struct {
	To          []string
	CC          []string
	BCC         []string
	Subject     string
	BodyPlain   string
	BodyHTML    string
	Attachments []Attachment
	InReplyTo   string
	References  []string
}

type Attachment struct {
	Filename string
	Content  []byte
	MIMEType string
}

func (m *Message) allRecipients() []string {
	all := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	all = append(all, m.To...)
	all = append(all, m.CC...)
	all = append(all, m.BCC...)
	return all
}

type AuthError struct {
	Message string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed: %s", e.Message)
}

type ConnectionError struct {
	Host    string
	Port    int
	Message string
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection to %s:%d failed: %s", e.Host, e.Port, e.Message)
}

type SMTPError struct {
	Code    int
	Message string
}

func (e *SMTPError) Error() string {
	return fmt.Sprintf("SMTP error %d: %s", e.Code, e.Message)
}

type Provider struct {
	config       Config
	envelopeFrom string
}

func NewProvider(config Config, envelopeFrom string) *Provider {
	return &Provider{
		config:       config,
		envelopeFrom: envelopeFrom,
	}
}

func (p *Provider) Send(ctx context.Context, msg *Message) (string, error) {
	messageID := fmt.Sprintf("<%s@%s>", idgen.New(), extractDomain(p.envelopeFrom))

	emailData, err := p.buildEmail(msg, messageID)
	if err != nil {
		return "", fmt.Errorf("build email: %w", err)
	}

	if err := p.sendSMTP(ctx, msg.allRecipients(), emailData); err != nil {
		return "", err
	}

	return messageID, nil
}

func (p *Provider) TestConnection(ctx context.Context) error {
	conn, client, err := p.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = client.Close() }()

	if err := client.Hello("localhost"); err != nil {
		return &ConnectionError{
			Host:    p.config.Host,
			Port:    p.config.Port,
			Message: fmt.Sprintf("EHLO failed: %v", err),
		}
	}

	if p.config.TLS == TLSModeSTARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return &ConnectionError{
				Host:    p.config.Host,
				Port:    p.config.Port,
				Message: "STARTTLS is required but not supported by the server",
			}
		}
		tlsConfig := &tls.Config{
			ServerName: p.config.Host,
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return &ConnectionError{
				Host:    p.config.Host,
				Port:    p.config.Port,
				Message: fmt.Sprintf("STARTTLS failed: %v", err),
			}
		}
	}

	if p.config.AuthType != AuthTypeNone && p.config.AuthType != "" {
		auth, err := p.buildAuth(ctx)
		if err != nil {
			return &AuthError{Message: err.Error()}
		}
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return &AuthError{
					Message: err.Error(),
				}
			}
		}
	}

	return nil
}

func (p *Provider) Close() error {
	return nil
}

func (p *Provider) dial(ctx context.Context) (net.Conn, *smtp.Client, error) {
	addr := fmt.Sprintf("%s:%d", p.config.Host, p.config.Port)

	var conn net.Conn
	var err error

	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}

	if p.config.TLS == TLSModeTLS {
		tlsConfig := &tls.Config{
			ServerName: p.config.Host,
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		return nil, nil, &ConnectionError{
			Host:    p.config.Host,
			Port:    p.config.Port,
			Message: err.Error(),
		}
	}

	client, err := smtp.NewClient(conn, p.config.Host)
	if err != nil {
		_ = conn.Close()
		return nil, nil, &ConnectionError{
			Host:    p.config.Host,
			Port:    p.config.Port,
			Message: fmt.Sprintf("SMTP handshake failed: %v", err),
		}
	}

	return conn, client, nil
}

func (p *Provider) sendSMTP(ctx context.Context, recipients []string, emailData []byte) error {
	conn, client, err := p.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = client.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return &SMTPError{Code: 0, Message: fmt.Sprintf("failed to set connection deadline: %v", err)}
		}
	}
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stop()

	if helloErr := client.Hello("localhost"); helloErr != nil {
		return &SMTPError{Code: 0, Message: fmt.Sprintf("EHLO failed: %v", helloErr)}
	}

	if p.config.TLS == TLSModeSTARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return &SMTPError{Code: 0, Message: "STARTTLS required but not supported by SMTP server"}
		}
		tlsConfig := &tls.Config{
			ServerName: p.config.Host,
		}
		if tlsErr := client.StartTLS(tlsConfig); tlsErr != nil {
			return &SMTPError{Code: 0, Message: fmt.Sprintf("STARTTLS failed: %v", tlsErr)}
		}
	}

	if p.config.AuthType != AuthTypeNone && p.config.AuthType != "" {
		auth, authBuildErr := p.buildAuth(ctx)
		if authBuildErr != nil {
			return &AuthError{Message: authBuildErr.Error()}
		}
		if auth != nil {
			if authErr := client.Auth(auth); authErr != nil {
				return &AuthError{Message: authErr.Error()}
			}
		}
	}

	if mailErr := client.Mail(p.envelopeFrom); mailErr != nil {
		return parseSMTPError(mailErr)
	}

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return parseSMTPError(err)
		}
	}

	w, dataErr := client.Data()
	if dataErr != nil {
		return parseSMTPError(dataErr)
	}

	if _, writeErr := w.Write(emailData); writeErr != nil {
		_ = w.Close()
		return &SMTPError{Code: 0, Message: fmt.Sprintf("failed to write email data: %v", writeErr)}
	}

	if closeErr := w.Close(); closeErr != nil {
		return parseSMTPError(closeErr)
	}

	return client.Quit()
}

func (p *Provider) buildAuth(ctx context.Context) (smtp.Auth, error) {
	switch p.config.AuthType {
	case AuthTypePlain:
		return smtp.PlainAuth("", p.config.Username, p.config.Password, p.config.Host), nil
	case AuthTypeLogin:
		return &loginAuth{
			username: p.config.Username,
			password: p.config.Password,
		}, nil
	case AuthTypeOAuthMicrosoft:
		if p.config.OAuth == nil {
			return nil, fmt.Errorf("OAuth configuration is required for Microsoft OAuth")
		}
		accessToken, err := getMicrosoftAccessToken(ctx, p.config.OAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to get Microsoft access token: %w", err)
		}
		return &xoauth2Auth{
			username:    p.config.OAuth.Email,
			accessToken: accessToken,
		}, nil
	case AuthTypeOAuthGoogle:
		if p.config.OAuth == nil {
			return nil, fmt.Errorf("OAuth configuration is required for Google OAuth")
		}
		accessToken, err := getGoogleAccessToken(ctx, p.config.OAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to get Google access token: %w", err)
		}
		return &xoauth2Auth{
			username:    p.config.OAuth.Email,
			accessToken: accessToken,
		}, nil
	default:
		return nil, nil
	}
}

func getMicrosoftAccessToken(ctx context.Context, cfg *OAuthConfig) (string, error) {
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", cfg.TenantID)

	data := url.Values{}
	data.Set("client_id", cfg.ClientID)
	data.Set("client_secret", cfg.ClientSecret)
	data.Set("scope", "https://outlook.office365.com/.default")
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("OAuth error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}

	return tokenResp.AccessToken, nil
}

func getGoogleAccessToken(ctx context.Context, cfg *OAuthConfig) (string, error) {
	tokenURL := "https://oauth2.googleapis.com/token"

	data := url.Values{}
	data.Set("client_id", cfg.ClientID)
	data.Set("client_secret", cfg.ClientSecret)
	data.Set("refresh_token", cfg.RefreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("OAuth error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}

	return tokenResp.AccessToken, nil
}

type xoauth2Auth struct {
	username    string
	accessToken string
}

func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	authString := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.accessToken)
	return "XOAUTH2", []byte(authString), nil
}

func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil
	}
	return nil, nil
}

func (p *Provider) buildEmail(msg *Message, messageID string) ([]byte, error) {
	var buf bytes.Buffer

	headers := textproto.MIMEHeader{}
	headers.Set("From", p.envelopeFrom)
	headers.Set("To", strings.Join(msg.To, ", "))
	if len(msg.CC) > 0 {
		headers.Set("Cc", strings.Join(msg.CC, ", "))
	}
	headers.Set("Subject", encodeSubject(msg.Subject))
	headers.Set("Message-ID", messageID)
	headers.Set("Date", time.Now().Format(time.RFC1123Z))
	headers.Set("MIME-Version", "1.0")

	if msg.InReplyTo != "" {
		headers.Set("In-Reply-To", msg.InReplyTo)
		refs := make([]string, len(msg.References), len(msg.References)+1)
		copy(refs, msg.References)
		refs = append(refs, msg.InReplyTo)
		headers.Set("References", strings.Join(refs, " "))
	}

	hasAttachments := len(msg.Attachments) > 0
	hasHTML := msg.BodyHTML != ""

	if !hasAttachments && !hasHTML {
		headers.Set("Content-Type", "text/plain; charset=utf-8")
		headers.Set("Content-Transfer-Encoding", "quoted-printable")
		writeHeaders(&buf, headers)
		qpWriter := quotedprintable.NewWriter(&buf)
		_, _ = qpWriter.Write([]byte(msg.BodyPlain))
		_ = qpWriter.Close()
	} else if !hasAttachments && hasHTML {
		boundary := generateBoundary()
		headers.Set("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
		writeHeaders(&buf, headers)
		writeMultipartAlternative(&buf, boundary, msg.BodyPlain, msg.BodyHTML)
	} else {
		boundary := generateBoundary()
		headers.Set("Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", boundary))
		writeHeaders(&buf, headers)
		writeMultipartMixed(&buf, boundary, msg)
	}

	return buf.Bytes(), nil
}

func writeHeaders(buf *bytes.Buffer, headers textproto.MIMEHeader) {
	for key, values := range headers {
		for _, value := range values {
			fmt.Fprintf(buf, "%s: %s\r\n", key, value)
		}
	}
	buf.WriteString("\r\n")
}

func writeMultipartAlternative(buf *bytes.Buffer, boundary, plainText, htmlText string) {
	w := multipart.NewWriter(buf)
	_ = w.SetBoundary(boundary)

	plainHeaders := textproto.MIMEHeader{}
	plainHeaders.Set("Content-Type", "text/plain; charset=utf-8")
	plainHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	pw, _ := w.CreatePart(plainHeaders)
	qpWriter := quotedprintable.NewWriter(pw)
	_, _ = qpWriter.Write([]byte(plainText))
	_ = qpWriter.Close()

	htmlHeaders := textproto.MIMEHeader{}
	htmlHeaders.Set("Content-Type", "text/html; charset=utf-8")
	htmlHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	hw, _ := w.CreatePart(htmlHeaders)
	qpWriter = quotedprintable.NewWriter(hw)
	_, _ = qpWriter.Write([]byte(htmlText))
	_ = qpWriter.Close()

	_ = w.Close()
}

func writeMultipartMixed(buf *bytes.Buffer, boundary string, msg *Message) {
	w := multipart.NewWriter(buf)
	_ = w.SetBoundary(boundary)

	if msg.BodyHTML != "" {
		altBoundary := generateBoundary()
		altHeaders := textproto.MIMEHeader{}
		altHeaders.Set("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", altBoundary))
		ap, _ := w.CreatePart(altHeaders)
		writeMultipartAlternativeInline(ap, altBoundary, msg.BodyPlain, msg.BodyHTML)
	} else {
		plainHeaders := textproto.MIMEHeader{}
		plainHeaders.Set("Content-Type", "text/plain; charset=utf-8")
		plainHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
		pw, _ := w.CreatePart(plainHeaders)
		qpWriter := quotedprintable.NewWriter(pw)
		_, _ = qpWriter.Write([]byte(msg.BodyPlain))
		_ = qpWriter.Close()
	}

	for _, att := range msg.Attachments {
		attHeaders := textproto.MIMEHeader{}
		mimeType := att.MIMEType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		attHeaders.Set("Content-Type", mimeType)
		attHeaders.Set("Content-Transfer-Encoding", "base64")
		attHeaders.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))

		aw, _ := w.CreatePart(attHeaders)
		encoder := base64.NewEncoder(base64.StdEncoding, &base64LineWriter{w: aw})
		_, _ = encoder.Write(att.Content)
		_ = encoder.Close()
	}

	_ = w.Close()
}

func writeMultipartAlternativeInline(w io.Writer, boundary, plainText, htmlText string) {
	mw := multipart.NewWriter(w)
	_ = mw.SetBoundary(boundary)

	plainHeaders := textproto.MIMEHeader{}
	plainHeaders.Set("Content-Type", "text/plain; charset=utf-8")
	plainHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	pw, _ := mw.CreatePart(plainHeaders)
	qpWriter := quotedprintable.NewWriter(pw)
	_, _ = qpWriter.Write([]byte(plainText))
	_ = qpWriter.Close()

	htmlHeaders := textproto.MIMEHeader{}
	htmlHeaders.Set("Content-Type", "text/html; charset=utf-8")
	htmlHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	hw, _ := mw.CreatePart(htmlHeaders)
	qpWriter = quotedprintable.NewWriter(hw)
	_, _ = qpWriter.Write([]byte(htmlText))
	_ = qpWriter.Close()

	_ = mw.Close()
}

type base64LineWriter struct {
	w     io.Writer
	count int
}

func (w *base64LineWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		remaining := 76 - w.count
		if remaining <= 0 {
			_, _ = w.w.Write([]byte("\r\n"))
			w.count = 0
			remaining = 76
		}
		chunk := p
		if len(chunk) > remaining {
			chunk = p[:remaining]
		}
		n, err := w.w.Write(chunk)
		total += n
		w.count += n
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

func encodeSubject(subject string) string {
	for _, r := range subject {
		if r > 127 {
			return mime.QEncoding.Encode("utf-8", subject)
		}
	}
	return subject
}

func generateBoundary() string {
	return fmt.Sprintf("----=_Part_%s", idgen.New()[:16])
}

func extractDomain(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return "localhost"
}

func parseSMTPError(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()

	var code int
	if len(errStr) >= 3 {
		if c := errStr[:3]; c[0] >= '0' && c[0] <= '9' {
			_, _ = fmt.Sscanf(c, "%d", &code)
		}
	}

	return &SMTPError{
		Code:    code,
		Message: maskAddresses(errStr),
	}
}

var smtpAddressPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)

func maskAddresses(s string) string {
	return smtpAddressPattern.ReplaceAllString(s, "***@$1")
}

type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToUpper(string(fromServer)) {
	case "USERNAME:":
		return []byte(a.username), nil
	case "PASSWORD:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected server challenge: %s", fromServer)
	}
}
