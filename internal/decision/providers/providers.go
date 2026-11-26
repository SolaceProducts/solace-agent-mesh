// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package providers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/gemini"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
)

type provider struct {
	build          func(decision.ClientConfig) (decision.Evaluator, error)
	defaultModel   string
	defaultBaseURL string
	mediaTypes     func(model string, kind decision.MediaKind) []string
	contextTokens  int
}

var known = map[string]provider{
	decision.ProviderTypeSafe: {
		build:          func(cfg decision.ClientConfig) (decision.Evaluator, error) { return evaluator(typesafe.New(cfg)) },
		defaultModel:   typesafe.DefaultModel,
		defaultBaseURL: typesafe.DefaultBaseURL,
		mediaTypes:     typesafe.MediaMIMETypes,
	},
	decision.ProviderOpenAI: {
		build:          func(cfg decision.ClientConfig) (decision.Evaluator, error) { return evaluator(openai.New(cfg)) },
		defaultModel:   openai.DefaultModel,
		defaultBaseURL: openai.DefaultBaseURL,
		mediaTypes:     openai.MediaMIMETypes,
		contextTokens:  1_000_000,
	},
	decision.ProviderGoogleAIStudio: {
		build:          func(cfg decision.ClientConfig) (decision.Evaluator, error) { return evaluator(gemini.New(cfg)) },
		defaultModel:   gemini.DefaultModel,
		defaultBaseURL: gemini.DefaultBaseURL,
		mediaTypes:     gemini.MediaMIMETypes,
		contextTokens:  gemini.ContextTokens,
	},
}

func evaluator[C decision.Evaluator](client C, err error) (decision.Evaluator, error) {
	if err != nil {
		return nil, err
	}
	return client, nil
}

func Supported() []string {
	ids := make([]string, 0, len(known))
	for id := range known {
		if id != decision.ProviderTypeSafe {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return append([]string{decision.ProviderTypeSafe}, ids...)
}

type Config struct {
	Provider string
	decision.ClientConfig
}

func NewEvaluator(cfg Config) (decision.Evaluator, error) {
	p, ok := known[cfg.Provider]
	if !ok {
		return nil, &decision.Error{Kind: decision.FailureConfig, Err: unknownProviderError(cfg.Provider)}
	}
	return p.build(cfg.ClientConfig)
}

func unknownProviderError(id string) error {
	return fmt.Errorf("unknown decision provider %q (supported: %s)", id, strings.Join(Supported(), ", "))
}

func DefaultModel(id string) string { return known[id].defaultModel }

func DefaultBaseURL(id string) string { return known[id].defaultBaseURL }

func ContextTokens(id string) int { return known[id].contextTokens }

func MIMETypes(id, model string, kind decision.MediaKind) []string {
	p, ok := known[id]
	if !ok {
		return nil
	}
	return p.mediaTypes(model, kind)
}

func Accepts(id, model string, kind decision.MediaKind) bool {
	return len(MIMETypes(id, model, kind)) > 0
}
