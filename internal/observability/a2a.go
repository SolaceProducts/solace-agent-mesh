// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
	"syscall"
)

type A2AHTTPError interface {
	error
	StatusCode() int
}

type A2AJSONRPCError interface {
	error
	JSONRPCErrorCode() int
}

var rpcErrorTextPattern = regexp.MustCompile(`(?i)(?:jsonrpc|json[- ]rpc|downstream error)\b.*-?\d+`)

var httpStatusTextPattern = regexp.MustCompile(`(?i)\bHTTP\s+(\d{3})\b`)

func classifyA2AError(err error) string {
	if err == nil {
		return ""
	}

	if httpErr, ok := errors.AsType[A2AHTTPError](err); ok {
		return classifyHTTPStatus(httpErr.StatusCode())
	}

	if _, ok := errors.AsType[A2AJSONRPCError](err); ok {
		return "jsonrpc_error"
	}

	if isConnectionError(err) {
		return "connection_error"
	}

	msg := err.Error()

	if rpcErrorTextPattern.MatchString(msg) {
		return "jsonrpc_error"
	}

	if m := httpStatusTextPattern.FindStringSubmatch(msg); m != nil {
		code := 0
		for _, c := range m[1] {
			code = code*10 + int(c-'0')
		}
		if code >= 400 {
			return classifyHTTPStatus(code)
		}
	}

	if netErr, ok := errors.AsType[net.Error](err); (ok && netErr.Timeout()) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	return classifyError(err)
}

func isConnectionError(err error) bool {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		lower := strings.ToLower(urlErr.Error())
		if strings.Contains(lower, "connection refused") ||
			strings.Contains(lower, "no such host") ||
			strings.Contains(lower, "connection reset") ||
			strings.Contains(lower, "eof") {
			return true
		}
	}

	return false
}

func ClassifyA2AError(err error) string {
	return classifyA2AError(err)
}
