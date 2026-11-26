// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !product_analytics

package clianalytics

import (
	"context"
	"log/slog"

	"github.com/spf13/cobra"
)

func SessionID() string { return "" }

type Gateway struct {
	BaseURL        string
	Token          string
	AllowPlainHTTP bool
	SkipTLSVerify  bool
}

func (g Gateway) Addressed() bool { return g.BaseURL != "" }

type Options struct {
	DatabaseURL string
	DataDir     string
	Gateway     Gateway
	Logger      *slog.Logger
}

func TrackCommand(cmd *cobra.Command) *cobra.Command { return cmd }

func Report(context.Context, *cobra.Command, error, Options) {
}
