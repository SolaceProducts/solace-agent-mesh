// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"unicode"
)

type Sink interface {
	Send(ctx context.Context, events []Event) error
}

type Identifier interface {
	Identify(ctx context.Context, installID string, traits map[string]any) error
}

type Flusher interface {
	Flush(ctx context.Context) error
}

type BatchLimiter interface {
	MaxBatchSize() int
}

type retryable interface {
	Retryable() bool
}

func IsRetryable(err error) bool {
	var r retryable
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return true
}

const redactedSecret = "[redacted]"

const secretWords = `(?:api|access|write|private|public|client|signing|secret|auth|session|encryption)[ _-]?(?:keys?|secrets?|tokens?|ids?)` +
	`|x-api-key|apikey|authorization|proxy-authorization|bearer|pass(?:word|wd|phrase)?|pwd|secrets?|tokens?|credentials?|cookie|signature`

const destinationWords = `host(?:name)?|server|addr(?:ess)?|endpoint|url|uri|dsn|user(?:name)?|uid|db(?:name)?|database|account|region|bucket`

const minSecretLen = 8

const minOpaqueLen = 24

var (
	reSchemeURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s"'` + "`" + `)\]]*`)

	reRelativeURL = regexp.MustCompile(`(^|[\s"'(=])//[A-Za-z0-9._-]+\.[A-Za-z0-9._-]+[^\s"'` + "`" + `)\]]*`)

	reHost = regexp.MustCompile(`\b[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9-]+)*\.[a-z]{2,}(?::\d{1,5})?\b`)

	reIPPort = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`)

	reLabelledValue = regexp.MustCompile(`(?i)\b(` + secretWords + `|` + destinationWords + `)(\s*[:=]\s*)(?:(?:bearer|basic|digest|token)\s+)?[^\s,;"'` + "`" + `)\]]+`)

	reNamedValue = regexp.MustCompile(`(?i)\b(` + secretWords + `)(\s+)([^\s,;"'` + "`" + `)\]]+)`)

	reOpaqueValue = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9._+/=-]{` + strconv.Itoa(minOpaqueLen-1) + `,}`)
)

func withoutSecrets(err error) error {
	if err == nil {
		return nil
	}
	err = withoutPath(err)
	redacted := redactSecrets(err.Error())
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}

func sanitizedPanic(recovered any) string {
	if err, ok := recovered.(error); ok {
		return withoutSecrets(err).Error()
	}
	return redactSecrets(fmt.Sprint(recovered))
}

func redactSecrets(msg string) string {
	msg = reSchemeURL.ReplaceAllString(msg, redactedSecret)
	msg = reRelativeURL.ReplaceAllString(msg, "${1}"+redactedSecret)
	msg = reLabelledValue.ReplaceAllString(msg, "${1}${2}"+redactedSecret)
	msg = replaceValueIf(msg, reNamedValue, looksLikeSecret)
	msg = reHost.ReplaceAllString(msg, redactedSecret)
	msg = reIPPort.ReplaceAllString(msg, redactedSecret)
	return replaceWholeIf(msg, reOpaqueValue, looksLikeSecret)
}

func replaceValueIf(msg string, re *regexp.Regexp, isSecret func(string) bool) string {
	const label, separator, value = 1, 2, 3
	return re.ReplaceAllStringFunc(msg, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) <= value || !isSecret(parts[value]) {
			return match
		}
		return parts[label] + parts[separator] + redactedSecret
	})
}

func replaceWholeIf(msg string, re *regexp.Regexp, isSecret func(string) bool) string {
	return re.ReplaceAllStringFunc(msg, func(match string) string {
		if !isSecret(match) {
			return match
		}
		return redactedSecret
	})
}

func looksLikeSecret(value string) bool {
	if len(value) < minSecretLen {
		return false
	}
	var hasDigit, hasUpper, hasLower bool
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		}
	}
	return hasDigit || (hasUpper && hasLower)
}

func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return nonRetryableError{err: err}
}

type nonRetryableError struct{ err error }

func (e nonRetryableError) Error() string { return e.err.Error() }
func (e nonRetryableError) Unwrap() error { return e.err }

func (nonRetryableError) Retryable() bool { return false }

func PartialDrop(err error, n int) error {
	if err == nil || n <= 0 {
		return err
	}
	return partialDropError{err: err, n: n}
}

type partialDropError struct {
	err error
	n   int
}

func (e partialDropError) Error() string { return e.err.Error() }
func (e partialDropError) Unwrap() error { return e.err }

func (partialDropError) Retryable() bool { return false }

func (e partialDropError) Discarded() int { return e.n }

type discardCounter interface {
	Discarded() int
}

func PartialDropCount(err error) int {
	var d discardCounter
	if errors.As(err, &d) {
		return d.Discarded()
	}
	return 0
}

type NoopSink struct{}

func (NoopSink) Send(context.Context, []Event) error { return nil }

type LoggingSink struct {
	logger *slog.Logger
}

func NewLoggingSink(logger *slog.Logger) LoggingSink {
	return LoggingSink{logger: logger}
}

func (s LoggingSink) Send(_ context.Context, events []Event) error {
	if s.logger != nil {
		s.logger.Debug("product analytics batch accepted", "count", len(events))
	}
	return nil
}

func SanitizedError(err error) error { return withoutSecrets(err) }

func SanitizedPanic(recovered any) string { return sanitizedPanic(recovered) }
