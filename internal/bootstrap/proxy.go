// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"io/fs"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type ProxyOpts struct {
	ListenAddr string

	TLS *entrypoint.ProxyTLSConfig

	TLSAddr string

	StaticFS fs.FS

	Timeouts *entrypoint.ServerTimeouts
}

func NewGatewayProxy(opts ProxyOpts, logger *slog.Logger) *entrypoint.GatewayProxy {
	p := entrypoint.NewGatewayProxy(opts.ListenAddr, logger)
	if opts.TLS != nil {
		p.SetTLS(opts.TLS, opts.TLSAddr)
	}
	if opts.StaticFS != nil {
		p.SetStaticFS(opts.StaticFS)
	}
	if opts.Timeouts != nil {
		p.SetTimeouts(*opts.Timeouts)
	}
	return p
}
