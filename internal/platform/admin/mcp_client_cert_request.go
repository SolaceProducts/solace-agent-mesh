// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/tls"
	"errors"
	"net/http"
	"sync/atomic"
)

type clientCertRequest struct {
	hasCertificate bool
	requested      atomic.Bool
	offered        atomic.Bool
	served         atomic.Bool
	refusedWith    atomic.Int32
}

func watchClientCertRequests(cfg *tls.Config) *clientCertRequest {
	seen := &clientCertRequest{hasCertificate: len(cfg.Certificates) > 0}
	configured := cfg.Certificates
	cfg.GetClientCertificate = func(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		seen.requested.Store(true)
		for i := range configured {
			if request.SupportsCertificate(&configured[i]) == nil {
				seen.offered.Store(true)
				return &configured[i], nil
			}
		}
		return &tls.Certificate{}, nil
	}
	return seen
}

type clientCertTransport struct {
	base http.RoundTripper
	seen *clientCertRequest
}

func (t clientCertTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	switch {
	case err != nil:
	case resp.StatusCode < http.StatusMultipleChoices:
		t.seen.served.Store(true)
	default:
		t.seen.refusedWith.Store(int32(resp.StatusCode))
	}
	return resp, err
}

func (s *clientCertRequest) mark(err error) error {
	if err == nil || !s.requested.Load() || s.served.Load() {
		return err
	}
	return &afterClientCertRequestError{
		cause:                 err,
		certificateSent:       s.offered.Load(),
		certificateConfigured: s.hasCertificate,
		refusedWith:           int(s.refusedWith.Load()),
	}
}

type afterClientCertRequestError struct {
	cause                 error
	certificateSent       bool
	certificateConfigured bool
	refusedWith           int
}

func (e *afterClientCertRequestError) Error() string { return e.cause.Error() }

func (e *afterClientCertRequestError) Unwrap() error { return e.cause }

func (e *afterClientCertRequestError) failure() mcpFailure {
	switch {
	case e.certificateSent:
		return mcpFailure{"the server rejected the client certificate", msgMCPTLSClientCert}
	case e.certificateConfigured:
		return mcpFailure{"the server asked for a client certificate and the configured one does not match what it accepts", msgMCPClientCertNotAccepted}
	default:
		return mcpFailure{"the server asked for a client certificate and none is configured", msgMCPClientCertMissing}
	}
}

func clientCertRequestFailure(err error, kind transportFailure) (mcpFailure, bool) {
	after, ok := errors.AsType[*afterClientCertRequestError](err)
	if !ok {
		return mcpFailure{}, false
	}
	closed := kind == transportClosedByServer || kind == transportRequestFailed
	if (after.certificateSent && (closed || isClientCertRejected(err))) ||
		(!after.certificateSent && (closed || isTLSAlertFromPeer(err))) {
		return after.failure(), true
	}
	return mcpFailure{}, false
}

func clientCertStatusFailure(err error) (mcpFailure, bool) {
	after, ok := errors.AsType[*afterClientCertRequestError](err)
	if !ok || after.refusedWith != http.StatusBadRequest {
		return mcpFailure{}, false
	}
	switch {
	case after.certificateSent:
		return mcpFailure{"the server answered HTTP 400 after the client certificate was sent", msgMCPStatus400AfterCertSent}, true
	case after.certificateConfigured:
		return mcpFailure{"the server answered HTTP 400 after asking for a client certificate the configured one does not match", msgMCPStatus400AfterCertUnmatched}, true
	default:
		return mcpFailure{"the server answered HTTP 400 after asking for a client certificate, and none is configured", msgMCPStatus400AfterCertMissing}, true
	}
}

func withoutClientCertRequest(err error) error {
	if after, ok := errors.AsType[*afterClientCertRequestError](err); ok {
		return after.cause
	}
	return err
}
