// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
)

var templateBlockRegex = directive.TemplateBlockRegex

var templateParamRegex = directive.TemplateParamRegex

func ResolveTemplateBlocks(
	ctx context.Context,
	text string,
	artifactSvc *artifact.ScopedService,
	agentName, userID, sessionID string,
	resolveEmbeds func(ctx context.Context, text string) (string, error),
	logger *slog.Logger,
) (string, error) {
	matches := templateBlockRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	renderer := agent.NewTemplateRenderer(artifactSvc, agentName, userID, sessionID, logger)

	result := text
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		fullStart, fullEnd := m[0], m[1]
		block := templateBlockEvent(text[m[2]:m[3]], text[m[4]:m[5]])

		rendered, err := renderer.RenderBlock(ctx, block)
		if err != nil {
			logger.Warn("template block rendering failed",
				"dataArtifact", block.DataSource,
				"templateLen", len(block.Content),
				"errorKind", agent.TemplateErrorKind(err),
			)
		}

		if resolveEmbeds != nil && rendered != "" {
			resolved, err := resolveEmbeds(ctx, rendered)
			if err != nil {
				logger.Warn("recursive embed resolution in template output failed", "error", err)
			} else {
				rendered = resolved
			}
		}

		result = result[:fullStart] + rendered + result[fullEnd:]
	}

	return result, nil
}

func templateBlockEvent(paramsStr, templateContent string) *agent.FencedBlockEvent {
	params := parseTemplateBlockParams(paramsStr)
	block := &agent.FencedBlockEvent{
		Type:       agent.FencedBlockTemplate,
		DataSource: params["data"],
		JSONPath:   params["jsonpath"],
		Limit:      -1,
		Content:    templateContent,
	}
	if limit, err := strconv.Atoi(params["limit"]); err == nil {
		block.Limit = limit
	}
	return block
}

func parseTemplateBlockParams(paramsStr string) map[string]string {
	params := make(map[string]string)
	matches := templateParamRegex.FindAllStringSubmatch(paramsStr, -1)
	for _, m := range matches {
		params[m[1]] = m[2]
	}
	return params
}
