// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type Context struct {
	Request *a2a.StructuredInvocationRequest

	InputSchema map[string]any

	OutputSchema map[string]any

	MaxRetries int

	RetryCount int

	RetryPending bool
}

type HandlerConfig struct {
	InputSchema map[string]any

	OutputSchema map[string]any

	MaxRetries int

	Logger *slog.Logger
}

type ResultEmbed struct {
	ArtifactName string

	Version *int

	Status string

	Message string
}

type RetryFeedback struct {
	Message string
}

var defaultInputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"text": map[string]any{
			"type": "string",
		},
	},
	"required": []any{"text"},
}

type ArtifactLoader interface {
	LoadContent(agentName, userID, sessionID, filename string, version *int) ([]byte, int, error)
}
