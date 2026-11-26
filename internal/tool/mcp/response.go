// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/maximhq/bifrost/core/schemas"
)

type ProcessingConfig struct {
	EnableIntelligentProcessing bool
	EnableTextFormatDetection   bool
	EnableContentParsing        bool
	FallbackToRawOnError        bool
	MaxContentItems             int
	MaxSingleItemSizeMB         int
}

func DefaultProcessingConfig() ProcessingConfig {
	return ProcessingConfig{
		EnableIntelligentProcessing: true,
		EnableTextFormatDetection:   true,
		EnableContentParsing:        true,
		FallbackToRawOnError:        true,
		MaxContentItems:             50,
		MaxSingleItemSizeMB:         100,
	}
}

func convertResponse(resp *schemas.BifrostMCPResponse, toolName string, procCfg ProcessingConfig) *tool.Result {
	if resp == nil {
		return tool.Error("nil MCP response")
	}

	content := extractResponseContent(resp)

	if content == "" {
		return tool.OK("MCP tool returned empty response")
	}

	if !procCfg.EnableIntelligentProcessing {
		return tool.OK(content)
	}

	items := extractContentItems(content, toolName)
	if len(items) == 0 {
		return tool.OK(content)
	}

	items = enforceContentCaps(items, procCfg.MaxContentItems, procCfg.MaxSingleItemSizeMB)

	dataObjects := make([]tool.DataObject, 0, len(items))
	for _, item := range items {
		if do := contentItemToDataObject(item, procCfg); do != nil {
			dataObjects = append(dataObjects, *do)
		}
	}

	msg := fmt.Sprintf("MCP tool %q returned %d content item(s)", toolName, len(dataObjects))
	if len(dataObjects) == 1 && dataObjects[0].MIMEType == "text/plain" && len(dataObjects[0].Content) < 2048 {
		msg = string(dataObjects[0].Content)
		return tool.OK(msg)
	}

	opts := []tool.ResultOption{}
	if len(dataObjects) > 0 {
		opts = append(opts, tool.WithDataObjects(dataObjects...))
	}

	return tool.OK(msg, opts...)
}

func contentItemToDataObject(item ContentItem, procCfg ProcessingConfig) *tool.DataObject {
	do := &tool.DataObject{
		Name:     item.Filename,
		MIMEType: item.MIMEType,
	}

	if len(item.Metadata) > 0 {
		do.Metadata = make(map[string]any, len(item.Metadata))
		for k, v := range item.Metadata {
			do.Metadata[k] = v
		}
	}

	switch item.Type {
	case ContentTypeText:
		do.Content = []byte(item.Text)
		do.Disposition = tool.DispositionAuto
	case ContentTypeImage, ContentTypeAudio:
		do.Content = item.Data
		do.Disposition = tool.DispositionArtifact
	case ContentTypeResource:
		if item.Text != "" {
			do.Content = []byte(item.Text)
		} else if item.Data != nil {
			do.Content = item.Data
		}
		do.Disposition = tool.DispositionAuto
		if item.URI != "" {
			if do.Metadata == nil {
				do.Metadata = make(map[string]any)
			}
			do.Metadata["source_uri"] = item.URI
		}
	default:
		return nil
	}

	return do
}

func extractResponseContent(resp *schemas.BifrostMCPResponse) string {
	if resp.ChatMessage == nil || resp.ChatMessage.Content == nil {
		return ""
	}

	content := resp.ChatMessage.Content
	if content.ContentStr != nil {
		return *content.ContentStr
	}

	if len(content.ContentBlocks) > 0 {
		if b, err := json.Marshal(content.ContentBlocks); err == nil {
			return string(b)
		}
	}

	return ""
}

func marshalArgs(args map[string]any) (string, error) {
	if len(args) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
