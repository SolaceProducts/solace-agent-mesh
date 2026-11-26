// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aliased

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type Config struct {
	Alias         string
	Bindings      runtime.ModelBindings
	HTTPClient    *http.Client
	Logger        *slog.Logger
	Timeout       time.Duration
	MaxStateBytes int
	Instrument    func(decision.Evaluator) decision.Evaluator
}

type Evaluator struct {
	cfg Config

	mu       sync.Mutex
	endpoint endpoint
	client   decision.Evaluator
}

type endpoint struct {
	provider string
	apiBase  string
	apiKey   string
}

func New(cfg Config) (*Evaluator, error) {
	if cfg.Alias == "" {
		return nil, configError(errors.New("decision model alias is required"))
	}
	if cfg.Bindings == nil {
		return nil, configError(fmt.Errorf("decision model %q needs the platform model registry", cfg.Alias))
	}
	return &Evaluator{cfg: cfg}, nil
}

func (e *Evaluator) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	raw, err := e.cfg.Bindings.ResolveRawOrFetch(ctx, e.cfg.Alias)
	if err != nil {
		err = fmt.Errorf("resolve decision model %q: %w", e.cfg.Alias, err)
		if errors.Is(err, bindings.ErrAliasUnbound) {
			return decision.Result{}, configError(err)
		}
		return decision.Result{}, err
	}
	if !samapp.IsDecisionModel(raw) {
		return decision.Result{}, configError(fmt.Errorf("model %q is a chat model; a decision needs a model registered with modelType decision", e.cfg.Alias))
	}
	client, err := e.clientFor(samapp.DecisionProvider(raw), raw)
	if err != nil {
		return decision.Result{}, configError(fmt.Errorf("decision model %q: %w", e.cfg.Alias, err))
	}
	registeredModel, _ := raw["model"].(string)
	req.Model = cmp.Or(req.Model, registeredModel)
	result, err := client.Evaluate(ctx, req)
	result.ModelConfigID = samapp.ModelConfigID(raw)
	return result, err
}

func configError(err error) error {
	return &decision.Error{Kind: decision.FailureConfig, Err: err}
}

func (e *Evaluator) clientFor(provider string, raw map[string]any) (decision.Evaluator, error) {
	apiBase, _ := raw["api_base"].(string)
	apiKey, _ := raw["api_key"].(string)
	ep := endpoint{provider: provider, apiBase: apiBase, apiKey: apiKey}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil && e.endpoint == ep {
		return e.client, nil
	}
	client, err := providers.NewEvaluator(providers.Config{Provider: provider, ClientConfig: decision.ClientConfig{
		BaseURL:       apiBase,
		APIKey:        apiKey,
		Timeout:       e.cfg.Timeout,
		MaxStateBytes: e.cfg.MaxStateBytes,
		HTTPClient:    e.cfg.HTTPClient,
		Logger:        e.cfg.Logger,
	}})
	if err != nil {
		return nil, err
	}
	if e.cfg.Instrument != nil {
		client = e.cfg.Instrument(client)
	}
	e.endpoint, e.client = ep, client
	return client, nil
}
