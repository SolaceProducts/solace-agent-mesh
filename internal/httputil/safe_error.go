// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"context"
	"errors"
	"net"
	"net/url"
)

func SafeTransportErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "connection timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "request canceled"
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return "connection timed out"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "DNS lookup failed"
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return "network connection failed"
	}
	return "request failed"
}

func TransportErrorCause(err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		return urlErr.Err
	}
	return err
}
