// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"

	_ "github.com/emersion/go-message/charset"
)

type NormalizeOpts struct {
	TrustedAuthservIDs []string

	MaxAttachmentSizeBytes int64
}

const (
	maxHeaderCount     = 256
	maxHeaderLineBytes = 8 * 1024
	maxMultipartDepth  = 16
)

func Normalize(rfc822 []byte, trustedAuthservIDs []string) (*NormalizedEmail, error) {
	return NormalizeWithOpts(rfc822, NormalizeOpts{TrustedAuthservIDs: trustedAuthservIDs})
}

func NormalizeWithOpts(rfc822 []byte, opts NormalizeOpts) (*NormalizedEmail, error) {
	if err := checkHeaderCaps(rfc822); err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}
	if err := checkMultipartDepth(rfc822, maxMultipartDepth); err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}

	mr, err := mail.CreateReader(bytes.NewReader(rfc822))
	if err != nil {
		return nil, fmt.Errorf("normalize: parse: %w", err)
	}
	header := &mr.Header

	n := &NormalizedEmail{
		ReceivedAt: time.Now(),
		RawHeaders: collectRawHeaders(header),
	}

	if from, _ := header.AddressList("From"); len(from) > 0 {
		n.From = EmailAddress{
			Address:     strings.ToLower(from[0].Address),
			DisplayName: from[0].Name,
		}
	}
	if to, _ := header.AddressList("To"); len(to) > 0 {
		for _, a := range to {
			n.To = append(n.To, EmailAddress{
				Address:     strings.ToLower(a.Address),
				DisplayName: a.Name,
			})
		}
	}
	if cc, _ := header.AddressList("Cc"); len(cc) > 0 {
		for _, a := range cc {
			n.CC = append(n.CC, EmailAddress{
				Address:     strings.ToLower(a.Address),
				DisplayName: a.Name,
			})
		}
	}
	if rt, _ := header.AddressList("Reply-To"); len(rt) > 0 {
		n.ReplyTo = &EmailAddress{
			Address:     strings.ToLower(rt[0].Address),
			DisplayName: rt[0].Name,
		}
	}

	n.Subject, _ = header.Subject()

	n.MessageID, n.InReplyTo, n.References, n.ThreadID = threadHeaders(header, rfc822)

	var authResultsAll []string
	fields := header.FieldsByKey("Authentication-Results")
	for fields.Next() {
		v, err := fields.Text()
		if err != nil {
			v = fields.Value()
		}
		if v != "" {
			authResultsAll = append(authResultsAll, v)
		}
	}
	n.AuthResults = ParseAuthResults(authResultsAll, opts.TrustedAuthservIDs)

	if err := readBodyParts(mr, n, opts.MaxAttachmentSizeBytes); err != nil {
		return nil, fmt.Errorf("normalize: body: %w", err)
	}

	return n, nil
}

func threadHeaders(header *mail.Header, rfc822 []byte) (messageID, inReplyTo string, references []string, threadID string) {
	if mid, err := header.MessageID(); err == nil && mid != "" {
		messageID = "<" + mid + ">"
	} else {
		messageID = synthesizeMessageID(rfc822)
	}
	if irt, err := header.MsgIDList("In-Reply-To"); err == nil && len(irt) > 0 {
		inReplyTo = "<" + irt[0] + ">"
	}
	if refs, err := header.MsgIDList("References"); err == nil {
		for _, r := range refs {
			references = append(references, "<"+r+">")
		}
	}
	switch {
	case len(references) > 0:
		threadID = references[0]
	case inReplyTo != "":
		threadID = inReplyTo
	default:
		threadID = messageID
	}
	return messageID, inReplyTo, references, threadID
}

func threadKey(rfc822 []byte) string {
	if err := checkHeaderCaps(rfc822); err != nil {
		return ""
	}
	mr, err := mail.CreateReader(bytes.NewReader(rfc822))
	if err != nil {
		return ""
	}
	_, _, _, threadID := threadHeaders(&mr.Header, rfc822)
	return threadID
}

func synthesizeMessageID(rfc822 []byte) string {
	sum := sha256.Sum256(rfc822)
	return fmt.Sprintf("<synth-%s@sam-email-entrypoint>", hex.EncodeToString(sum[:8]))
}

func readBodyParts(mr *mail.Reader, n *NormalizedEmail, maxAttachmentBytes int64) error {
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			body, _ := io.ReadAll(p.Body)
			switch ct {
			case "text/plain":
				if n.BodyText == "" {
					n.BodyText = string(body)
				}
			case "text/html":
				if n.BodyHTML == "" {
					n.BodyHTML = string(body)
				}
			}
		case *mail.AttachmentHeader:
			if dispo, _, _ := h.ContentDisposition(); dispo == "inline" {
				_, _ = io.Copy(io.Discard, p.Body)
				continue
			}
			declared, _, _ := h.ContentType()
			filename, _ := h.Filename()
			body, err := readAttachmentBytes(p.Body, maxAttachmentBytes)
			if err != nil {
				return err
			}
			detected := http.DetectContentType(body)
			n.Attachments = append(n.Attachments, Attachment{
				Filename:         filename,
				DeclaredMIMEType: declared,
				DetectedMIMEType: detected,
				SizeBytes:        int64(len(body)),
				Content:          AttachmentContent{InlineBytes: body},
			})
		}
	}
}

func readAttachmentBytes(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	return io.ReadAll(io.LimitReader(r, maxBytes+1))
}

func checkHeaderCaps(rfc822 []byte) error {
	headerEnd := bytes.Index(rfc822, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		headerEnd = bytes.Index(rfc822, []byte("\n\n"))
	}
	if headerEnd < 0 {
		headerEnd = len(rfc822)
	}
	headerBlock := rfc822[:headerEnd]

	var (
		count   int
		lineLen int
	)
	for _, b := range headerBlock {
		if b == '\n' {
			if lineLen > maxHeaderLineBytes {
				return fmt.Errorf("header line exceeds %d bytes", maxHeaderLineBytes)
			}
			count++
			lineLen = 0
			continue
		}
		if b == '\r' {
			continue
		}
		lineLen++
	}
	if lineLen > maxHeaderLineBytes {
		return fmt.Errorf("header line exceeds %d bytes", maxHeaderLineBytes)
	}
	if count > maxHeaderCount {
		return fmt.Errorf("header count %d exceeds limit %d", count, maxHeaderCount)
	}
	return nil
}

func checkMultipartDepth(rfc822 []byte, maxDepth int) error {
	depth := bytes.Count(bytes.ToLower(rfc822), []byte("multipart/"))
	if depth > maxDepth {
		return fmt.Errorf("multipart nesting %d exceeds limit %d", depth, maxDepth)
	}
	return nil
}

func collectRawHeaders(h *mail.Header) map[string][]string {
	out := map[string][]string{}
	fields := h.Fields()
	for fields.Next() {
		k := http.CanonicalHeaderKey(fields.Key())
		out[k] = append(out[k], strings.TrimSpace(fields.Value()))
	}
	return out
}
