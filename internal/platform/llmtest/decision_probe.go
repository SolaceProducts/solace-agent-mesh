// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

var decisionProbeQuestion = decision.Question{
	Key:          "connection_test",
	Type:         decision.TypeBoolean,
	Instructions: "`message` is a connection test",
}

func ProbeDecision(ctx context.Context, provider string, raw map[string]any, httpClient *http.Client, logger *slog.Logger, rec *SpendRecording) (bool, string) {
	if err := ProbeDecisionError(ctx, provider, raw, httpClient, logger, rec); err != nil {
		return false, formatProbeError(err)
	}
	return true, probeSuccessMessage
}

func ProbeDecisionError(ctx context.Context, provider string, raw map[string]any, httpClient *http.Client, logger *slog.Logger, rec *SpendRecording) error {
	apiBase, _ := raw["api_base"].(string)
	apiKey, _ := raw["api_key"].(string)
	model, _ := raw["model"].(string)
	client, err := providers.NewEvaluator(providers.Config{
		Provider:   provider,
		BaseURL:    apiBase,
		APIKey:     apiKey,
		Timeout:    probeRequestTimeoutSeconds * time.Second,
		HTTPClient: httpClient,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	if rec != nil {
		client = spend.NewMeteredEvaluator(client, rec.Publisher, rec.Attribution, rec.Gate)
	}
	questions := []decision.Question{decisionProbeQuestion}
	result, err := client.Evaluate(ctx, decision.Request{
		Model:     model,
		State:     map[string]any{"message": "SAM model configuration connection test"},
		Questions: questions,
	})
	if err == nil {
		err = result.Validate(questions)
	}
	return err
}
