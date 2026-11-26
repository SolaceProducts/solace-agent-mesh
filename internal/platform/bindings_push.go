// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

const (
	AWEBindingApp = "awe"
	STRBindingApp = "str"
)

const secondaryPushTimeout = 5 * time.Second

const (
	secondaryPushAttempts = 3
	secondaryPushBackoff  = time.Second
)

type BindingsPusher struct {
	client *control.Client
	logger *slog.Logger
	apps   []string
}

func NewBindingsPusher(client *control.Client, logger *slog.Logger, apps ...string) *BindingsPusher {
	if logger == nil {
		logger = slog.Default()
	}
	if len(apps) == 0 {
		apps = []string{AWEBindingApp}
	}
	return &BindingsPusher{
		client: client,
		logger: logger.With("logger_name", "bindings-pusher"),
		apps:   apps,
	}
}

func (p *BindingsPusher) UpsertModel(ctx context.Context, alias string, rawConfig map[string]any) {
	p.patch(ctx, "upsert", map[string]any{alias: rawConfig})
}

func (p *BindingsPusher) DeleteModel(ctx context.Context, alias string) {
	p.patch(ctx, "delete", map[string]any{alias: nil})
}

func (p *BindingsPusher) UpsertModelByKeys(ctx context.Context, rawConfig map[string]any, keys ...string) {
	payload := make(map[string]any, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		payload[k] = rawConfig
	}
	if len(payload) == 0 {
		return
	}
	p.patch(ctx, "upsert", payload)
}

func (p *BindingsPusher) DeleteModelByKeys(ctx context.Context, keys ...string) {
	payload := make(map[string]any, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		payload[k] = nil
	}
	if len(payload) == 0 {
		return
	}
	p.patch(ctx, "delete", payload)
}

func (p *BindingsPusher) UpsertAndDeleteByKeys(ctx context.Context, rawConfig map[string]any, upsertKeys, deleteKeys []string) {
	payload := make(map[string]any, len(upsertKeys)+len(deleteKeys))
	for _, k := range upsertKeys {
		if k != "" {
			payload[k] = rawConfig
		}
	}
	for _, k := range deleteKeys {
		if k != "" {
			payload[k] = nil
		}
	}
	if len(payload) == 0 {
		return
	}
	p.patch(ctx, "upsert-and-delete", payload)
}

func (p *BindingsPusher) UpsertAll(ctx context.Context, models map[string]map[string]any) {
	if len(models) == 0 {
		return
	}
	payload := make(map[string]any, len(models))
	for alias, raw := range models {
		payload[alias] = raw
	}
	p.patch(ctx, "upsert-all", payload)
}

func (p *BindingsPusher) patch(ctx context.Context, op string, models map[string]any) {
	if p.client == nil {
		return
	}
	body, err := json.Marshal(map[string]any{"models": models})
	if err != nil {
		p.logger.Error("marshal binding patch", "op", op, "error", err)
		return
	}
	for i, app := range p.apps {
		if i == 0 {
			if _, err := p.client.Custom(ctx, app, "patch", "bindings", body); err != nil {
				p.logger.Warn("push binding patch", "app", app, "op", op, "error", err)
				continue
			}
			p.logger.Debug("pushed binding patch", "app", app, "op", op, "count", len(models))
			continue
		}
		go p.pushSecondary(context.WithoutCancel(ctx), app, op, body, len(models))
	}
}

func (p *BindingsPusher) pushSecondary(ctx context.Context, app, op string, body json.RawMessage, count int) {
	var lastErr error
	for attempt := 1; attempt <= secondaryPushAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, secondaryPushTimeout)
		_, err := p.client.Custom(attemptCtx, app, "patch", "bindings", body)
		cancel()
		if err == nil {
			p.logger.Debug("pushed binding patch", "app", app, "op", op, "count", count, "attempt", attempt)
			return
		}
		lastErr = err
		if attempt < secondaryPushAttempts {
			time.Sleep(secondaryPushBackoff)
		}
	}
	p.logger.Debug("secondary binding push failed (best-effort)", "app", app, "op", op, "attempts", secondaryPushAttempts, "error", lastErr)
}
