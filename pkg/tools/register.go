// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func BuiltinTools() []tool.BuiltinTool {
	builtins := []tool.BuiltinTool{
		&ListArtifactsTool{},
		&LoadArtifactTool{},
		&DeleteArtifactTool{},
		&AppendToArtifactTool{},
		&SearchAndReplaceInArtifactTool{},
		&GrepTool{},
		&WebRequestTool{},
		&GetCurrentDatetimeTool{},
		&CreateImageFromDescriptionTool{},
		&DescribeImageTool{},
		&DescribeAudioTool{},
		&EditImageWithGeminiTool{},
		&GenerateImageWithGeminiTool{},
		&SelectVoiceTool{},
		&TranscribeAudioTool{},
		&AskUserQuestionTool{},
		&QueryDataWithSQLTool{},
		&CreateSQLiteDBTool{},
		&TransformDataWithJMESPathTool{},
		&MergeStructuredDataTool{},
		&ReinterpretArtifactTool{},
		&GoogleWebSearchTool{},
		NewWebSearchTool(nil),
		&DeepResearchTool{},
	}
	return append(builtins, additionalBuiltins()...)
}

func RegisterAll(ts *tool.Set) {
	for _, t := range BuiltinTools() {
		if err := ts.Register(t); err != nil {
			slog.Default().With("logger_name", "tool-registry").Error("failed to register built-in tool", "tool", t.Name(), "error", err)
		}
	}
}
