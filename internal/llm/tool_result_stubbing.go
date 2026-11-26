// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"slices"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

const geminiToolResultStubBatchBytes = 24 * 1024

func ToolResultStubBatchBytes(provider, model string) int {
	if schemas.ModelProvider(provider) == schemas.Gemini || isGeminiModel(model) {
		return geminiToolResultStubBatchBytes
	}
	return 0
}

func isGeminiModel(model string) bool {
	return slices.Contains(strings.FieldsFunc(strings.ToLower(model), isModelIDSeparator), "gemini")
}
