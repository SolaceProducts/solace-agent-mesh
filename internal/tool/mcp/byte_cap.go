// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
)

const MCPDefaultMaxResponseBytes int64 = 64 * 1024 * 1024

const unboundedTransportMessage int64 = 0

var ErrMCPResponseTooLarge = errors.New("mcp response exceeds configured byte cap")

func (cfg *ToolsetConfig) EffectiveMaxResponseBytes() int64 {
	switch {
	case cfg.MaxResponseBytes < 0:
		return 0
	case cfg.MaxResponseBytes == 0:
		return MCPDefaultMaxResponseBytes
	default:
		return cfg.MaxResponseBytes
	}
}

type responseTooLargeError struct {
	limit int64
}

func (e *responseTooLargeError) Error() string {
	return fmt.Sprintf("MCP response exceeded %d bytes", e.limit)
}

func (e *responseTooLargeError) Is(target error) bool {
	return target == ErrMCPResponseTooLarge
}

func (e *responseTooLargeError) Limit() int64 { return e.limit }

type byteCapRoundTripper struct {
	base     http.RoundTripper
	limit    int64
	clientID string
	logger   *slog.Logger
	tripped  atomic.Bool
}

func (rt *byteCapRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Body = &capReadCloser{
		base:   resp.Body,
		limit:  rt.limit,
		onTrip: rt.recordTrip,
	}
	return resp, nil
}

func (rt *byteCapRoundTripper) CloseIdleConnections() {
	if closer, ok := rt.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (rt *byteCapRoundTripper) recordTrip(observed int64) {
	if !rt.tripped.CompareAndSwap(false, true) {
		return
	}
	if rt.logger != nil {
		rt.logger.Warn("mcp response cap exceeded",
			"clientID", rt.clientID,
			"limit_bytes", rt.limit,
			"observed_bytes", observed,
		)
	}
}

type capReadCloser struct {
	base   io.ReadCloser
	limit  int64
	read   int64
	onTrip func(observed int64)
}

func (c *capReadCloser) Read(p []byte) (int, error) {
	if c.read > c.limit {
		return 0, &responseTooLargeError{limit: c.limit}
	}
	remaining := c.limit + 1 - c.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := c.base.Read(p)
	c.read += int64(n)
	if c.read > c.limit {
		if c.onTrip != nil {
			c.onTrip(c.read)
		}
		return n, &responseTooLargeError{limit: c.limit}
	}
	return n, err
}

func (c *capReadCloser) Close() error {
	return c.base.Close()
}

func wrapWithByteCap(base http.RoundTripper, cfg *ToolsetConfig, logger *slog.Logger) http.RoundTripper {
	if cfg == nil {
		return base
	}
	return NewByteCappedTransport(base, cfg.EffectiveMaxResponseBytes(), cfg.ClientID, logger)
}

func NewByteCappedTransport(base http.RoundTripper, limit int64, clientID string, logger *slog.Logger) http.RoundTripper {
	if limit <= 0 {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &byteCapRoundTripper{
		base:     base,
		limit:    limit,
		clientID: clientID,
		logger:   logger,
	}
}
