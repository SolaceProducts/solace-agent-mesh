// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type promptBuilderHandler struct {
	core       *entrypoint.GatewayCore
	groupRepo  *store.PromptGroupRepo
	promptRepo *store.PromptRepo
	logger     *slog.Logger

	tempPolicy   llm.TemperaturePolicy
	choicePolicy llm.ToolChoicePolicy
}

type ChatBuilderRequest struct {
	Message             string           `json:"message"`
	ConversationHistory []ChatBuilderMsg `json:"conversationHistory"`
	CurrentTemplate     map[string]any   `json:"currentTemplate"`
}

type ChatBuilderMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatBuilderResponse struct {
	Message         string               `json:"message"`
	TemplateUpdates *ChatBuilderTemplate `json:"templateUpdates"`
	Confidence      float64              `json:"confidence"`
	ReadyToSave     bool                 `json:"readyToSave"`
	IsError         bool                 `json:"isError"`
	IsWarning       bool                 `json:"isWarning"`
}

func decodePromptTemplateArgs(args string) (ChatBuilderResponse, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return ChatBuilderResponse{}, fmt.Errorf("decode arguments: %w", err)
	}
	if err := unwrapEncodedArgument(raw, "templateUpdates"); errors.Is(err, errEncodedNotObject) {
		return ChatBuilderResponse{}, err
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return ChatBuilderResponse{}, fmt.Errorf("re-encode arguments: %w", err)
	}
	var result ChatBuilderResponse
	if err := json.Unmarshal(normalized, &result); err != nil {
		return ChatBuilderResponse{}, fmt.Errorf("decode normalized arguments: %w", err)
	}
	result.IsError = false
	result.IsWarning = false
	return result, nil
}

type ChatBuilderInitResponse struct {
	Message    string  `json:"message"`
	Confidence float64 `json:"confidence"`
}

type ChatBuilderTemplate struct {
	Name        string `json:"name,omitempty"`
	Category    string `json:"category,omitempty"`
	Command     string `json:"command,omitempty"`
	PromptText  string `json:"promptText,omitempty"`
	Description string `json:"description,omitempty"`
}

const promptBuilderSystemPrompt = `You are the SAM Prompt Builder, an AI assistant built into Solace Agent Mesh that helps users create reusable prompt templates. Do not identify yourself as your model provider, or reveal the underlying model. If asked who you are or who made you, say you are the SAM Prompt Builder, an AI assistant built into Solace Agent Mesh.

CRITICAL RULES:
1. You MUST reply by calling the emit_prompt_template tool — every turn, with NO EXCEPTIONS. Never answer in plain prose; the template form is driven entirely by the tool arguments.
2. You MUST always include a "message" argument with a helpful, conversational response
3. NEVER respond with just "I understand" - always provide actionable guidance
4. ONLY create placeholders for DATA that changes (names, paths, dates, numbers, specific values)
5. Keep instructions, steps, requirements, and process descriptions as FIXED TEXT
6. Use descriptive variable names in Title Case (e.g., {{File Path}}, {{Module Name}})
7. Suggest appropriate categories (Development, Analysis, Documentation, Communication, Testing, etc.)
8. Generate short command names (lowercase, hyphens only)

TOOL ARGUMENTS (emit_prompt_template takes exactly these fields):
{
  "message": "your conversational response here - MUST be helpful and specific",
  "templateUpdates": {
    "name": "Name",
    "category": "Category",
    "command": "command-name",
    "promptText": "Template with {{placeholders}} for variable data only",
    "description": "Brief description"
  },
  "confidence": 0.0-1.0,
  "readyToSave": false
}

VARIABLE PLACEHOLDER RULES:
- Use {{Variable Name}} format for placeholders (Title Case with spaces)
- ONLY for data that changes: file paths, names, dates, numbers, specific values
- NOT for instructions, steps, or requirements
- Use descriptive names: {{File Path}}, {{Module Name}}, {{Task Description}}

EXAMPLES:

Example 1: Code Review Template
User: "I need a template for code reviews"
{
  "message": "Great! I'll create a code review template. What information changes each time you do a code review? For example: file path, module name, specific concerns?",
  "templateUpdates": {
    "name": "Code Review Template",
    "category": "Development"
  },
  "confidence": 0.6,
  "readyToSave": false
}

User: "The file path, module name, and specific security concerns"
{
  "message": "Perfect! I've created a code review template with placeholders for file path, module name, and security concerns. Check the preview on the left!",
  "templateUpdates": {
    "name": "Code Review Template",
    "category": "Development",
    "command": "code-review",
    "promptText": "Review the {{Module Name}} module located at {{File Path}}.\n\nPlease perform a comprehensive code review focusing on:\n1. Security vulnerabilities\n2. Error handling\n3. Code quality and best practices\n4. Specific attention to: {{Security Concerns}}\n\nProvide a detailed report with:\n- List of issues with severity levels\n- Specific code snippets that need attention\n- Recommendations for improvements",
    "description": "Template for code review with security focus"
  },
  "confidence": 0.9,
  "readyToSave": true
}

Example 2: Bug Report Template
User: "Create a bug report template"
{
  "message": "I'll create a bug report template! What details vary for each bug report? For example: bug description, steps to reproduce, expected vs actual behavior?",
  "templateUpdates": {
    "name": "Bug Report Template",
    "category": "Testing"
  },
  "confidence": 0.6,
  "readyToSave": false
}

User: "Bug description, steps to reproduce, and environment details"
{
  "message": "Excellent! I've created a bug report template with those placeholders. You can see it in the preview!",
  "templateUpdates": {
    "name": "Bug Report Template",
    "category": "Testing",
    "command": "bug-report",
    "promptText": "# Bug Report\n\n## Description\n{{Bug Description}}\n\n## Steps to Reproduce\n{{Steps To Reproduce}}\n\n## Environment\n{{Environment Details}}\n\n## Expected Behavior\nDescribe what should happen\n\n## Actual Behavior\nDescribe what actually happens\n\n## Additional Context\nAny other relevant information",
    "description": "Template for reporting bugs with structured format"
  },
  "confidence": 0.9,
  "readyToSave": true
}

REMEMBER:
- Ask clarifying questions to understand what data changes
- Create detailed, useful templates
- Only use placeholders for variable data
- Keep instructions and structure as fixed text
- Set readyToSave to true when template is complete`

const promptBuilderGreeting = `Hi! I'll help you create a prompt template. You can either:

1. Describe a recurring task you'd like to template
2. Paste an example transcript of the task

What would you like to create a template for?`

const emitPromptTemplateToolName = "emit_prompt_template"

var emitPromptTemplateTool = llm.ToolDefinition{
	Name:        emitPromptTemplateToolName,
	Description: "Return the prompt-template builder's reply and any template field updates. Call this on every turn.",
	Parameters: builderToolSchema(
		"0.0-1.0 confidence the template is correct.",
		"True only when the template is complete.",
		map[string]any{
			"templateUpdates": map[string]any{
				"type":        "object",
				"description": "Partial template fields to apply to the form. Omit fields you can't fill yet.",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string"},
					"category":    map[string]any{"type": "string"},
					"command":     map[string]any{"type": "string", "description": "lowercase, hyphens only"},
					"promptText":  map[string]any{"type": "string", "description": "Template body with {{Placeholders}} for variable data only."},
					"description": map[string]any{"type": "string"},
				},
			},
		},
	),
}

func (h *promptBuilderHandler) handleInit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ChatBuilderInitResponse{
		Message:    promptBuilderGreeting,
		Confidence: 1.0,
	})
}

func (h *promptBuilderHandler) handleChat(w http.ResponseWriter, r *http.Request) {
	if !isPromptsEnabled(h.core) {
		writeError(w, http.StatusNotImplemented, "prompt library is not enabled")
		return
	}

	var req ChatBuilderRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	userID := resolveUserID(r, h.core)

	general := resolveGeneralModel(r.Context(), h.core)
	if general.client == nil {
		writeJSON(w, http.StatusOK, ChatBuilderResponse{
			Message:         "I'd be happy to help create a prompt template, but LLM service is not configured. Please configure an LLM to use the prompt builder.",
			TemplateUpdates: &ChatBuilderTemplate{},
			Confidence:      0.5,
			IsError:         true,
		})
		return
	}

	existingCmds, _ := h.promptRepo.ListCommandsByUser(r.Context(), userID)

	messages := []llm.ChatMessage{
		{Role: "system", Content: promptBuilderSystemPrompt},
	}

	if len(existingCmds) > 0 {
		messages = append(messages, llm.ChatMessage{
			Role:    "system",
			Content: fmt.Sprintf("The user already has these commands: %s. Avoid suggesting duplicate commands.", strings.Join(existingCmds, ", ")),
		})
	}

	if len(req.CurrentTemplate) > 0 {
		templateJSON, _ := json.Marshal(req.CurrentTemplate)
		messages = append(messages, llm.ChatMessage{
			Role:    "system",
			Content: "Current template state: " + string(templateJSON),
		})
	}

	for _, msg := range req.ConversationHistory {
		messages = append(messages, llm.ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	messages = append(messages, llm.ChatMessage{
		Role:    "user",
		Content: req.Message,
	})

	promptPolicy, promptKey := temperaturePolicyFor(general.memoKey, &h.tempPolicy, "gateway-prompt-builder")

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp, err := llm.ChatCompletionWithToolChoiceFallback(ctx, general.client, &llm.ChatRequest{
		Messages:    messages,
		Tools:       []llm.ToolDefinition{emitPromptTemplateTool},
		ToolChoice:  "required",
		MaxTokens:   maxLLMTokens,
		Temperature: new(0.1),
		Observability: llm.ObservabilityContext{
			ComponentName: "gateway-prompt-builder",
			OwnerID:       userID,
		},
		Spend: h.core.RequestSpend(contextSubject(r), userID, httputil.ClientSurface(r),
			spend.SurfacePromptBuilder, general.spendKey, general.configID, ""),
	}, toolChoicePolicyFor(general.memoKey, &h.choicePolicy), promptPolicy, promptKey, h.logger)

	if err != nil {
		logAssistantLLMFailure(h.logger, "prompt builder LLM error, returning fallback", err)
		refusal := entrypoint.BudgetRefusalText(err)
		writeJSON(w, http.StatusOK, ChatBuilderResponse{
			Message:         cmp.Or(refusal, "An unexpected error occurred while processing your request. Please try again. If the problem persists, contact an administrator."),
			TemplateUpdates: &ChatBuilderTemplate{},
			Confidence:      0.3,
			IsError:         true,
			IsWarning:       refusal != "",
		})
		return
	}

	if args := firstToolCallArgs(resp.ToolCalls, emitPromptTemplateToolName); args != "" {
		result, parseErr := decodePromptTemplateArgs(args)
		if parseErr != nil {
			h.logger.Warn("prompt builder tool-args parse error, returning fallback", "error", parseErr)
			writeJSON(w, http.StatusOK, ChatBuilderResponse{
				Message:         "I had trouble composing that. Could you rephrase what you'd like the template to do?",
				TemplateUpdates: &ChatBuilderTemplate{},
				Confidence:      0.3,
				IsError:         true,
			})
			return
		}
		if result.TemplateUpdates == nil {
			result.TemplateUpdates = &ChatBuilderTemplate{}
		}
		writeJSON(w, http.StatusOK, result)
		return
	}

	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	result, err := decodePromptTemplateArgs(content)
	if err != nil {
		h.logger.Warn("prompt builder JSON parse error, returning raw message",
			"error", err, "contentLen", len(content))
		writeJSON(w, http.StatusOK, ChatBuilderResponse{
			Message:         content,
			TemplateUpdates: &ChatBuilderTemplate{},
			Confidence:      0.5,
		})
		return
	}

	if result.TemplateUpdates == nil {
		result.TemplateUpdates = &ChatBuilderTemplate{}
	}

	writeJSON(w, http.StatusOK, result)
}
