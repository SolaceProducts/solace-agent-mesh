// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package modelclient

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type Options struct {
	Logger         *slog.Logger
	Grants         llm.GrantTokens
	ConnectionTest *ConnectionTest
}

type ConnectionTest struct {
	TimeoutSeconds int
}

type Client struct {
	llm.Client
	Provider        string
	Model           string
	RequestDefaults llm.RequestDefaults

	bifrost *llm.BifrostClient
}

func (c *Client) Shutdown() {
	c.bifrost.Shutdown()
}

type ConfigError struct{ Cause error }

func (e *ConfigError) Error() string { return "parse model config: " + e.Cause.Error() }
func (e *ConfigError) Unwrap() error { return e.Cause }

type BuildError struct {
	Provider string
	Cause    error
}

func (e *BuildError) Error() string { return "create LLM client: " + e.Cause.Error() }
func (e *BuildError) Unwrap() error { return e.Cause }

func Build(ctx context.Context, raw any, opts Options) (*Client, error) {
	cfg, err := samapp.ParseModelConfig(raw, opts.Logger)
	if err != nil {
		return nil, &ConfigError{Cause: err}
	}
	cfg.Logger = opts.Logger
	cfg.Grants = opts.Grants
	if test := opts.ConnectionTest; test != nil {
		cfg.RequestTimeoutSeconds = test.TimeoutSeconds
		cfg.MaxRetries = new(0)
		cfg.StrictRequestDefaults = true
	}

	bifrostClient, err := llm.NewBifrostClient(ctx, *cfg)
	if err != nil {
		releaseAPIKeyEnv(cfg.APIKeyEnvVar)
		return nil, &BuildError{Provider: cfg.Provider, Cause: err}
	}

	client := &Client{
		Client:          bifrostClient,
		Provider:        cfg.Provider,
		Model:           cfg.Model,
		RequestDefaults: cfg.RequestDefaults,
		bifrost:         bifrostClient,
	}
	if opts.ConnectionTest == nil {
		client.Client = observability.NewInstrumentedLLMClient(bifrostClient, cfg.Model)
	}
	return client, nil
}

func releaseAPIKeyEnv(name string) {
	if strings.HasPrefix(name, "SAM_AWE_LLM_API_KEY_") {
		_ = os.Unsetenv(name)
	}
}
