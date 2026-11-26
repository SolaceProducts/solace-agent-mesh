// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"syscall"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

const ssrfBlockedMessageFormat = "The %s resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable %s. If it is a private-network address this deployment is meant to reach, an administrator can allow it."

func ssrfBlockedMessage(subject, advice string) string {
	return fmt.Sprintf(ssrfBlockedMessageFormat, subject, advice)
}

const maxCauseLogBytes = 256

func capLogDetail(s string) string {
	if len(s) <= maxCauseLogBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxCauseLogBytes], "") + "…(truncated)"
}

type outboundCause struct {
	redacted string
	err      error
}

func newOutboundCause(err error) *outboundCause {
	if err == nil {
		err = errors.New("outbound request failed")
	}
	cause := logging.UnwrapURLError(err)
	return &outboundCause{redacted: capLogDetail(logging.RedactURLQueries(cause.Error())), err: cause}
}

func newClassifiedOutboundCause(redacted string, err error) *outboundCause {
	return &outboundCause{redacted: redacted, err: logging.UnwrapURLError(err)}
}

func (e *outboundCause) Error() string { return e.redacted }

func (e *outboundCause) Unwrap() error { return e.err }

func (e *outboundCause) LoggedAsWarning() bool { return true }

func isOutboundCause(err error) bool {
	_, ok := errors.AsType[*outboundCause](err)
	return ok
}

func loggableCause(err error) error {
	if cause, ok := errors.AsType[*outboundCause](err); ok {
		return cause
	}
	return nil
}

type describedCause struct {
	message string
	cause   error
}

func (e *describedCause) Error() string { return e.message + ": " + e.cause.Error() }

func (e *describedCause) Unwrap() error { return e.cause }

func LogCause(err error) error {
	cause := loggableCause(err)
	if cause == nil {
		return err
	}
	return &describedCause{message: err.Error(), cause: cause}
}

type causeLoggingError struct{ error }

func (e causeLoggingError) Unwrap() error { return e.error }

func (e causeLoggingError) LogValue() slog.Value {
	return slog.StringValue(e.Error() + ": " + loggableCause(e.error).Error())
}

func withLoggableCause(err error) error {
	if err == nil || loggableCause(err) == nil {
		return err
	}
	return causeLoggingError{err}
}

func guardedFetchFailure(err error, subject, advice string) (*ExternalServiceError, bool) {
	if !errors.Is(err, errSSRFBlocked) {
		return nil, false
	}
	return &ExternalServiceError{Message: ssrfBlockedMessage(subject, advice), Cause: newOutboundCause(err)}, true
}

type unreachableTargetError struct {
	*ValidationError
	cause *outboundCause
}

func newUnreachableTargetError(prefix string, err error) *unreachableTargetError {
	return &unreachableTargetError{
		ValidationError: &ValidationError{Message: prefix + ": " + describeTransportFailure(err)},
		cause:           newOutboundCause(err),
	}
}

func (e *unreachableTargetError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.ValidationError}
	}
	return []error{e.ValidationError, e.cause}
}

type transportFailure int

const (
	transportRequestFailed transportFailure = iota
	transportRedirectRefused
	transportTooManyRedirects
	transportCanceled
	transportTimedOut
	transportUnresolved
	transportRefused
	transportUnreachable
	transportClosedByServer
	transportTLSFailed
	transportPlainHTTP
)

func (f transportFailure) String() string {
	switch f {
	case transportRedirectRefused:
		return errRedirectRefused.Error()
	case transportTooManyRedirects:
		return errTooManyRedirects.Error()
	case transportCanceled:
		return "the request was canceled"
	case transportTimedOut:
		return "the request timed out"
	case transportUnresolved:
		return "the host could not be resolved"
	case transportRefused:
		return "the connection was refused"
	case transportUnreachable:
		return "the host is unreachable from the platform"
	case transportClosedByServer:
		return "the connection was closed by the server"
	case transportTLSFailed:
		return "the TLS handshake or certificate check failed"
	case transportPlainHTTP:
		return "the server answered over plain HTTP; use an http:// URL"
	case transportRequestFailed:
	}
	return "the request failed"
}

func classifyTransportFailure(err error) transportFailure {
	cause := logging.UnwrapURLError(err)
	switch {
	case errors.Is(cause, errRedirectRefused):
		return transportRedirectRefused
	case errors.Is(cause, errTooManyRedirects):
		return transportTooManyRedirects
	case errors.Is(cause, context.Canceled):
		return transportCanceled
	case isTimeout(cause):
		return transportTimedOut
	case isDNSFailure(cause):
		return transportUnresolved
	case errors.Is(cause, syscall.ECONNREFUSED):
		return transportRefused
	case errors.Is(cause, syscall.EHOSTUNREACH), errors.Is(cause, syscall.ENETUNREACH):
		return transportUnreachable
	case errors.Is(cause, syscall.ECONNRESET), errors.Is(cause, io.EOF), errors.Is(cause, io.ErrUnexpectedEOF):
		return transportClosedByServer
	case isTLSFailure(cause), isTLSHandshakeFailure(err):
		return transportTLSFailed
	case errors.Is(cause, http.ErrSchemeMismatch):
		return transportPlainHTTP
	default:
		return transportRequestFailed
	}
}

func describeTransportFailure(err error) string {
	return classifyTransportFailure(err).String()
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	netErr, ok := errors.AsType[net.Error](err)
	return ok && netErr.Timeout()
}

func isDNSFailure(err error) bool {
	_, ok := errors.AsType[*net.DNSError](err)
	return ok
}

func isTLSFailure(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.AlertError](err); ok {
		return true
	}
	return isTLSAlertFromPeer(err)
}

var peerAlertsRejectingClientCert = []string{
	"tls: bad certificate",
	"tls: unsupported certificate",
	"tls: revoked certificate",
	"tls: expired certificate",
	"tls: unknown certificate",
	"tls: unknown certificate authority",
	"tls: access denied",
	"tls: certificate required",
}

func isClientCertRejected(err error) bool {
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && opErr.Op == tlsPeerAlertOp && opErr.Err != nil &&
		slices.Contains(peerAlertsRejectingClientCert, opErr.Err.Error())
}

func isUntrustedServerCA(err error) bool {
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return false
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return false
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	_, ok := errors.AsType[*tls.CertificateVerificationError](err)
	return ok
}

const tlsPeerAlertOp = "remote error"

func isTLSAlertFromPeer(err error) bool {
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && opErr.Op == tlsPeerAlertOp
}

func isTLSHandshakeFailure(err error) bool {
	urlErr, ok := errors.AsType[*url.Error](err)
	return ok && urlErr.Err != nil && strings.HasPrefix(urlErr.Err.Error(), "tls: ")
}
