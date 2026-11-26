// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelclient"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func BuildLLMOptions(ctx context.Context, modelRaw any, logger *slog.Logger) ([]runtime.Option, error) {
	return buildLLMOptions(ctx, modelRaw, logger)
}

func buildLLMOptions(ctx context.Context, modelRaw any, logger *slog.Logger) ([]runtime.Option, error) {
	built, err := modelclient.Build(ctx, modelRaw, modelclient.Options{Logger: logger})
	if err != nil {
		return nil, err
	}

	reconfigurable := llm.NewReconfigurableClientFor(built.Client, built.Model)

	opts := []runtime.Option{runtime.WithDefaultLLM(reconfigurable)}
	if built.Model != "" {
		opts = append(opts, runtime.WithLLM(built.Model, reconfigurable))
	}
	return opts, nil
}
