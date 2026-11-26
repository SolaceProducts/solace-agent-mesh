// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const chatCompletionsPath = "/chat/completions"

func ChatCompletionsEndpoint(baseURL string) (string, bool) {
	return chatEndpointSibling(baseURL, chatCompletionsPath)
}

func ChatCompletionsRoot(baseURL string) (string, bool) {
	return chatEndpointSibling(baseURL, "")
}

func OpenAIModelsURL(baseURL string) string {
	if models, ok := chatEndpointSibling(baseURL, "/models"); ok {
		return models
	}
	return strings.TrimRight(TrimTrailingV1(baseURL), "/") + "/v1/models"
}

func chatEndpointSibling(baseURL, path string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	root, ok := strings.CutSuffix(strings.TrimRight(u.Path, "/"), chatCompletionsPath)
	if !ok {
		return "", false
	}
	u.Path, u.RawPath = root+path, ""
	return u.String(), true
}

func (c *BifrostClient) chatURLPath(model string) (string, bool) {
	if path := c.azureGateway.pathFor(model); path != "" {
		return path, true
	}
	if c.chatEndpoint == "" {
		return "", false
	}
	if c.wireProvider(model) == schemas.OpenRouter {
		return "", true
	}
	return c.chatEndpoint, true
}
