// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

type ReplyRequest struct {
	To                 string
	Subject            string
	BodyText           string
	OriginalMessageID  string
	OriginalReferences []string
	Attachments        []ReplyAttachment
	AutoReply          bool
}

type ReplyAttachment struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

type SMTPReplySender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Logger   *slog.Logger

	Insecure bool
}

func (s *SMTPReplySender) Send(ctx context.Context, req ReplyRequest) error {
	if strings.TrimSpace(req.BodyText) == "" {
		if s.Logger != nil {
			s.Logger.LogAttrs(ctx, slog.LevelInfo, "skipping empty reply",
				slog.String("eventType", "email.reply_skipped"),
				slog.String("reason", "empty_body"),
				slog.String("to", maskEmail(req.To)),
			)
		}
		return nil
	}
	from, to, msg, err := buildReply(s.From, req)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	return s.sendOverSTARTTLS(addr, from, to, msg)
}

func buildReply(senderFrom string, req ReplyRequest) (from, to string, msg []byte, err error) {
	to = sanitizeHeaderValue(req.To)
	if to == "" {
		return "", "", nil, errors.New("smtp reply: To address is empty")
	}
	from = sanitizeHeaderValue(senderFrom)

	subject := sanitizeHeaderValue(req.Subject)
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	originalMessageID := sanitizeHeaderValue(req.OriginalMessageID)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	if originalMessageID != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", originalMessageID)
		refs := make([]string, 0, len(req.OriginalReferences)+1)
		for _, r := range req.OriginalReferences {
			if clean := sanitizeHeaderValue(r); clean != "" {
				refs = append(refs, clean)
			}
		}
		refs = append(refs, originalMessageID)
		fmt.Fprintf(&buf, "References: %s\r\n", strings.Join(refs, " "))
	}
	if req.AutoReply {
		fmt.Fprintf(&buf, "Auto-Submitted: auto-replied\r\n")
		fmt.Fprintf(&buf, "X-Auto-Response-Suppress: All\r\n")
	}
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	body := crlfBodyBecauseStrictRelaysRejectBareLF(req.BodyText)
	buf.WriteString(body)
	return from, to, buf.Bytes(), nil
}

func (s *SMTPReplySender) sendOverSTARTTLS(addr, from, to string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer func() { _ = c.Close() }()

	if helloErr := c.Hello("localhost"); helloErr != nil {
		return fmt.Errorf("smtp ehlo: %w", helloErr)
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp: server %s does not advertise STARTTLS — refusing to send in plaintext", addr)
	}
	if tlsErr := c.StartTLS(&tls.Config{ServerName: s.Host, InsecureSkipVerify: s.Insecure}); tlsErr != nil { //nolint:gosec // Insecure is an explicit test-only opt-in
		return fmt.Errorf("smtp starttls: %w", tlsErr)
	}

	if ok, _ := c.Extension("AUTH"); !ok {
		return fmt.Errorf("smtp: server %s does not advertise AUTH after STARTTLS — refusing to send unauthenticated", addr)
	}
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	if authErr := c.Auth(auth); authErr != nil {
		return fmt.Errorf("smtp auth: %w", authErr)
	}
	if mailErr := c.Mail(from); mailErr != nil {
		return fmt.Errorf("smtp mail: %w", mailErr)
	}
	if rcptErr := c.Rcpt(to); rcptErr != nil {
		return fmt.Errorf("smtp rcpt: %w", rcptErr)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data close: %w", err)
	}
	if err := c.Quit(); err != nil && s.Logger != nil {
		s.Logger.LogAttrs(context.Background(), slog.LevelDebug, "smtp quit failed after delivery",
			slog.String("eventType", "email.smtp_quit_failed"),
			slog.String("error", sanitizeForLog(err.Error())),
		)
	}
	return nil
}

func (s *SMTPReplySender) SupportsAttachments() bool { return false }

func sanitizeHeaderValue(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	r := strings.NewReplacer("\r", "", "\n", "")
	return r.Replace(v)
}

func crlfBodyBecauseStrictRelaysRejectBareLF(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}
