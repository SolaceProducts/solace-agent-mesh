// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

type ModelConfigPrices struct {
	repo          *store.ModelConfigRepo
	logger        *slog.Logger
	invalidPrices *logThrottle
}

func NewModelConfigPrices(repo *store.ModelConfigRepo, logger *slog.Logger) ModelConfigPrices {
	return ModelConfigPrices{
		repo:          repo,
		logger:        logger.With("logger_name", "governor"),
		invalidPrices: newLogThrottle(defaultTiming.repeatWarnAfter),
	}
}

func (p ModelConfigPrices) Lookup(ctx context.Context, ref ModelRef) (Model, error) {
	mc, err := p.configuration(ctx, ref)
	if err != nil || mc == nil {
		return Model{}, err
	}
	price, err := pricing.Stored{
		Input:      mc.PriceInput,
		Output:     mc.PriceOutput,
		CacheRead:  mc.PriceCacheRead,
		CacheWrite: mc.PriceCacheWrite,
	}.Price()
	model := Model{ConfigID: mc.ID, Name: mc.ModelName, Provider: mc.Provider}
	if err != nil {
		if ok, suppressed := p.invalidPrices.admit(mc.ID, time.Now()); ok {
			p.logger.Warn("model price is invalid; its spend is filed unpriced",
				"modelConfigID", mc.ID, "error", err, "suppressed", suppressed)
		}
		return model, nil
	}
	model.Price = price
	return model, nil
}

func (p ModelConfigPrices) configuration(ctx context.Context, ref ModelRef) (*store.ModelConfiguration, error) {
	if ref.ConfigID != "" {
		return p.repo.GetByID(ctx, ref.ConfigID)
	}
	return p.repo.GetByAliasOrID(ctx, ref.Key)
}
