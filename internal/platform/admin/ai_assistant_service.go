// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const aiAssistantModelAlias = "general"

const (
	mdFieldSystemPrompt = "`systemPrompt`"
	mdFieldSkills       = "`skills`"
)

const maxSkillsPerResponse = 8

var noopRelease = func() {
}

type toolsetDefinition struct {
	ID          string
	Name        string
	Description string
	Default     bool
}

const builtinVolumeToolsetID = "builtin_volume_tools"

const volumeToolsGuidance = `
**Volume Tools guidance** (only when builtin_volume_tools is selected)
- This toolset gives the agent a persistent, mounted **workspace volume** it can read, write, search, and manage files in; files persist across turns within a session. Select it for agents that work over a set of files, a repository, or a document collection across multiple steps (e.g. "organize my notes", "explore a codebase", "draft and revise documents") — not for one-off single-file hand-offs.
- When you select it, write the systemPrompt so the agent uses the tools explicitly: volume_write and volume_append to create or update files, volume_read to read them back, and volume_list, volume_glob, and volume_grep to explore. All paths are relative to the mounted workspace, and content the agent writes is available to it on later turns.
- The agent also needs a **workspace volume attached** — a volume slot bound to the tools' "workspace" param. This is configured in the builder after generation; note in the systemPrompt that the agent operates within its attached workspace.
- Prefer builtin_artifact_tools when the agent only needs to return individual files to the user; use volume tools when it needs a persistent working directory with many files.
`

func toolsetsInclude(toolsets []toolsetDefinition, id string) bool {
	for _, t := range toolsets {
		if t.ID == id {
			return true
		}
	}
	return false
}

var builtinToolsets = append([]toolsetDefinition{
	{ID: "builtin_artifact_tools", Name: "Artifact Tools", Description: "Create, update, and manage artifacts", Default: true},
	{ID: "builtin_web_request_tools", Name: "Web Request Tools", Description: "Make HTTP requests to external APIs"},
	{ID: "builtin_research_tools", Name: "Research Tools", Description: "Multi-step web research with planning + single-query Google search"},
	{ID: "builtin_file_tools", Name: "File Tools", Description: "Read and write files"},
	{ID: "builtin_image_tools", Name: "Image Tools", Description: "Process and analyze images"},
	{ID: "builtin_time_tools", Name: "Time Tools", Description: "Get current date and time"},
	{ID: "hil_tools", Name: "Human-in-the-Loop Tools", Description: "Request human input during conversations"},
	{ID: "data_analysis", Name: "Data Analysis Tools", Description: "JMESPath transforms, SQL queries, JSON merge, and charting over artifacts"},
	{ID: "builtin_code_execution", Name: "Code Execution", Description: "Run LLM-generated Python over artifacts in a sandbox (pandas/numpy/scikit-learn/matplotlib and more; no network)"},
}, strBackedAIToolsetDefs()...)

func strBackedAIToolsetDefs() []toolsetDefinition {
	strBacked := map[string]bool{
		"builtin_document_tools": true,
		"builtin_diagram_tools":  true,
		"builtin_media_tools":    true,
		"builtin_volume_tools":   true,
	}
	var defs []toolsetDefinition
	for _, e := range builtinToolsetCatalog {
		if strBacked[e.ID] {
			defs = append(defs, toolsetDefinition{ID: e.ID, Name: e.Name, Description: e.Description, Default: e.Default})
		}
	}
	return defs
}

type AIAssistantService struct {
	modelConfigSvc      *ModelConfigService
	agentRepo           *store.AgentRepo
	connectorRepo       *store.ConnectorRepo
	logger              *slog.Logger
	featureRegistry     *features.Registry
	maxInstructionChars int
	resolveLLM          llmClientResolver
	spendPublisher      *spend.Publisher
	budgetGate          *budget.Gate
}

func (s *AIAssistantService) SetSpendPublisher(p *spend.Publisher) {
	s.spendPublisher = p
}

func (s *AIAssistantService) SetBudgetGate(gate *budget.Gate) {
	s.budgetGate = gate
}

func (s *AIAssistantService) SetFeatureRegistry(reg *features.Registry) {
	s.featureRegistry = reg
}

func aiAssistantToolsets(reg *features.Registry) []toolsetDefinition {
	out := make([]toolsetDefinition, 0, len(builtinToolsets))
	for _, ts := range builtinToolsets {
		if flag := builtinToolsetFeatureFlag(ts.ID); flag != "" && (reg == nil || !reg.IsEnabled(flag)) {
			continue
		}
		out = append(out, ts)
	}
	return out
}

func builtinToolsetFeatureFlag(id string) string {
	for _, e := range builtinToolsetCatalog {
		if e.ID == id {
			return e.FeatureFlag
		}
	}
	return ""
}

type llmClientResolver func(ctx context.Context) (client llm.Client, configID string, release func(), err error)

func NewAIAssistantService(modelConfigSvc *ModelConfigService, agentRepo *store.AgentRepo, connectorRepo *store.ConnectorRepo, logger *slog.Logger, maxInstructionChars int) *AIAssistantService {
	s := &AIAssistantService{
		modelConfigSvc:      modelConfigSvc,
		agentRepo:           agentRepo,
		connectorRepo:       connectorRepo,
		logger:              logger,
		maxInstructionChars: maxInstructionChars,
	}
	s.resolveLLM = s.resolveLLMClientFromConfigSvc
	return s
}

func newAIAssistantServiceWithResolver(resolver llmClientResolver, agentRepo *store.AgentRepo, connectorRepo *store.ConnectorRepo, logger *slog.Logger) *AIAssistantService {
	return &AIAssistantService{
		agentRepo:     agentRepo,
		connectorRepo: connectorRepo,
		logger:        logger,
		resolveLLM:    resolver,
	}
}

func (s *AIAssistantService) GenerateAgent(ctx context.Context, req GenerateAgentRequest, userID, clientSurface string) (*GenerateAgentResponse, error) {
	if len(req.Description) < 10 || len(req.Description) > 1000 {
		return nil, &ValidationError{Message: "description must be between 10 and 1000 characters"}
	}

	existingAgents, _ := s.agentRepo.GetAll(ctx)
	agentNames := make([]string, 0, len(existingAgents))
	for _, a := range existingAgents {
		agentNames = append(agentNames, a.Name)
	}

	connectors, _ := s.connectorRepo.GetAll(ctx)
	existingConnectorDescriptions := make([]string, 0, len(connectors))
	for _, c := range connectors {
		desc := ""
		if c.Description != nil {
			desc = *c.Description
		}
		subtype := ""
		if c.Subtype != nil {
			subtype = *c.Subtype
		}
		existingConnectorDescriptions = append(existingConnectorDescriptions,
			fmt.Sprintf("- `%s`: %q (%s/%s) - %s", c.ID, c.Name, c.Type, subtype, desc))
	}

	availableConnectorDescriptions := make([]string, 0)
	if defs, err := LoadConnectorTypeDefinitions(); err == nil {
		for _, def := range defs {
			for _, st := range def.Subtypes {
				availableConnectorDescriptions = append(availableConnectorDescriptions,
					fmt.Sprintf("- **%s** (`%s/%s`): %s", st.Name, def.Type, st.Subtype, st.Description))
			}
		}
	}

	configuredModels := s.listConfiguredModels(ctx)

	allowedToolsets := aiAssistantToolsets(s.featureRegistry)

	systemPrompt := buildAIAssistantSystemPrompt(agentNames, existingConnectorDescriptions, availableConnectorDescriptions, configuredModels, allowedToolsets, s.maxInstructionChars)

	chatReq := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: req.Description},
		},
		MaxTokens: 8192,
	}

	llmClient, configID, releaseClient, err := s.resolveLLM(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseClient()

	chatReq.Spend = directActionSpend(ctx, s.logger, clientSurface, spend.SurfaceAgentGenerator, aiAssistantModelAlias, configID)
	llmClient = spend.NewMeteredClient(llmClient, "", s.spendPublisher, s.budgetGate)

	resp, err := llmClient.ChatCompletion(ctx, chatReq)
	if errors.Is(err, budget.ErrExhausted) {
		return nil, err
	}
	if err != nil {
		return nil, &ExternalServiceError{
			Service: "LLM",
			Message: "agent generation failed: " + llm.UserFacingErrorMessage(err),
			Cause:   err,
		}
	}

	if resp.FinishReason == "max_tokens" {
		s.logger.Warn("agent generation hit max_tokens; output likely truncated",
			"finishReason", resp.FinishReason,
			"contentLen", len(resp.Content))
	}

	result, err := parseGenerateAgentResponse(resp.Content)
	if err != nil {
		s.logger.Error("agent generation: invalid JSON from LLM",
			"finishReason", resp.FinishReason,
			"contentLen", len(resp.Content),
			"error", err)
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}
	if result.Name == "" {
		s.logger.Warn("agent generation: LLM returned empty/incomplete result",
			"finishReason", resp.FinishReason,
			"contentLen", len(resp.Content))
	}

	result.Name = ensureUniqueName(result.Name, agentNames)

	validConnectorIDs := make(map[string]bool)
	for _, c := range connectors {
		validConnectorIDs[c.ID] = true
	}
	validConnectors := make([]string, 0)
	for _, id := range result.Connectors {
		if validConnectorIDs[id] {
			validConnectors = append(validConnectors, id)
		}
	}
	result.Connectors = validConnectors

	result.Toolsets = validateAndDefaultToolsets(result.Toolsets, allowedToolsets)

	result.Skills = sanitizeSkills(result.Skills)

	result.ModelProvider = chooseModelProvider(result.ModelProvider, configuredModels)

	return result, nil
}

type configuredModelHint struct {
	ID          string
	Alias       string
	Description string
}

func (s *AIAssistantService) listConfiguredModels(ctx context.Context) []configuredModelHint {
	if s.modelConfigSvc == nil {
		return nil
	}
	models, err := s.modelConfigSvc.List(ctx)
	if err != nil {
		s.logger.Warn("agent generation: failed to list model configs; falling back to 'general' only", "error", err)
		fallback, fbErr := s.modelConfigSvc.GetByAlias(ctx, aiAssistantModelAlias)
		if fbErr != nil || fallback == nil || fallback.Provider == nil || fallback.ModelName == nil {
			return nil
		}
		return []configuredModelHint{{ID: fallback.ID, Alias: fallback.Alias}}
	}
	hints := make([]configuredModelHint, 0, len(models))
	for _, m := range models {
		if m.Provider == nil || m.ModelName == nil {
			continue
		}
		if m.ModelType == store.ModelTypeDecision {
			continue
		}
		desc := ""
		if m.Description != nil {
			desc = *m.Description
		}
		hints = append(hints, configuredModelHint{ID: m.ID, Alias: m.Alias, Description: desc})
	}
	return hints
}

func chooseModelProvider(picked []string, configured []configuredModelHint) []string {
	if len(configured) == 0 {
		return []string{aiAssistantModelAlias}
	}
	aliasToID := make(map[string]string, len(configured))
	idSet := make(map[string]bool, len(configured))
	var generalID string
	for _, m := range configured {
		aliasToID[m.Alias] = m.ID
		idSet[m.ID] = true
		if m.Alias == aiAssistantModelAlias {
			generalID = m.ID
		}
	}
	out := make([]string, 0, len(picked))
	for _, p := range picked {
		if id, ok := aliasToID[p]; ok {
			out = append(out, id)
		} else if idSet[p] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		if generalID != "" {
			out = append(out, generalID)
		} else {
			out = append(out, configured[0].ID)
		}
	}
	return out
}

func (s *AIAssistantService) resolveLLMClientFromConfigSvc(ctx context.Context) (llm.Client, string, func(), error) {
	rawCfg, err := s.modelConfigSvc.GetByAliasRaw(ctx, aiAssistantModelAlias)
	if err != nil {
		return nil, "", noopRelease, fmt.Errorf("look up %q model config: %w", aiAssistantModelAlias, err)
	}
	if rawCfg == nil {
		return nil, "", noopRelease, &ValidationError{
			Message: fmt.Sprintf("AI assistant requires a configured model for alias %q. Configure it in the Models UI before retrying.", aiAssistantModelAlias),
		}
	}

	bifrostCfg, err := samapp.ParseModelConfig(rawCfg, s.logger)
	if err != nil {
		return nil, "", noopRelease, fmt.Errorf("parse %q model config: %w", aiAssistantModelAlias, err)
	}
	bifrostCfg.Logger = s.logger
	bifrostCfg.Grants = s.modelConfigSvc.GrantTokens()

	client, err := llm.NewBifrostClient(ctx, *bifrostCfg)
	if err != nil {
		if strings.HasPrefix(bifrostCfg.APIKeyEnvVar, "SAM_AWE_LLM_API_KEY_") {
			_ = os.Unsetenv(bifrostCfg.APIKeyEnvVar)
		}
		return nil, "", noopRelease, fmt.Errorf("build Bifrost client for %q: %w", aiAssistantModelAlias, err)
	}
	return client, samapp.ModelConfigID(rawCfg), client.Shutdown, nil
}

func buildAIAssistantSystemPrompt(existingAgentNames, existingConnectorDescriptions, availableConnectorDescriptions []string, configuredModels []configuredModelHint, toolsets []toolsetDefinition, maxInstructionChars int) string {
	var sb strings.Builder

	sb.WriteString(`You are an expert **Agent Architect**. Your goal is to design the core of a new agent based on a user's request. You must follow these rules precisely:

**Rule 1: Naming Convention**
- The ` + "`name`" + ` must be human readable, concise (ideally 2-3 words, max 4), and professional, using spaces instead of PascalCase.
- It must **not** include generic suffixes like "Pro", "Plus", "Bot", or "AI".
- The name must be unique among existing agents.

**Rule 2: System Prompt Best Practices**
- Clearly state the agent's specific role and expertise. Ex: "You are a customer support agent specializing in software troubleshooting."
- Set its communication style and response format. Ex: "Respond in a friendly and professional tone."
- **If the agent requires tools or connectors, explicitly mention how they should be used in its instructions.**
- The ` + mdFieldSystemPrompt + ` MUST only contain instructions for the agent. It MUST NOT contain instructions for the user.

**Rule 3: Connector and Toolset Selection**
- Review the user's request and the lists of available resources.
- **Existing Connector Instances**: If the user's request matches a connector from this list, you MUST add its ID to the ` + "`connectors`" + ` array in your JSON response.
- **Available Connector Types**: If the user's request requires a data source that is NOT in the 'Existing' list, but a type IS available, you should write the ` + mdFieldSystemPrompt + ` assuming such a connector exists. However, you MUST NOT add any ID to the ` + "`connectors`" + ` array for it.
- **Toolsets**: Determine which toolsets the agent needs and add their IDs to the ` + "`toolsets`" + ` array.

**Rule 4: Model Selection**
- Pick exactly one model alias from the **Available Models** list below and emit it as a single-element ` + "`modelProvider`" + ` array (e.g. ` + "`\"modelProvider\": [\"general\"]`" + `).
- Match the alias to the agent's task — e.g. a coding alias for a code-focused agent, a vision alias for an image-focused agent.
- If unsure, pick ` + "`general`" + `. Never invent an alias that is not in the list.

**Available Resources**

`)

	sb.WriteString("**Existing Connector Instances:**\n")
	if len(existingConnectorDescriptions) > 0 {
		for _, desc := range existingConnectorDescriptions {
			sb.WriteString(desc + "\n")
		}
	} else {
		sb.WriteString("None\n")
	}

	sb.WriteString("\n**Available Connector Types:**\n")
	if len(availableConnectorDescriptions) > 0 {
		for _, desc := range availableConnectorDescriptions {
			sb.WriteString(desc + "\n")
		}
	} else {
		sb.WriteString("None\n")
	}

	sb.WriteString("\n**Available Toolsets:**\n")
	for _, ts := range toolsets {
		def := ""
		if ts.Default {
			def = " (enabled by default)"
		}
		fmt.Fprintf(&sb, "- `%s`: \"%s\"%s\n", ts.ID, ts.Description, def)
	}

	if toolsetsInclude(toolsets, builtinVolumeToolsetID) {
		sb.WriteString(volumeToolsGuidance)
	}

	sb.WriteString("\n**Available Models:**\n")
	if len(configuredModels) > 0 {
		for _, m := range configuredModels {
			if m.Description != "" {
				fmt.Fprintf(&sb, "- `%s`: %s\n", m.Alias, m.Description)
			} else {
				fmt.Fprintf(&sb, "- `%s`\n", m.Alias)
			}
		}
	} else {
		sb.WriteString("- `general` (default)\n")
	}

	if len(existingAgentNames) > 0 {
		sb.WriteString("\n**Existing Agent Names (must not conflict):**\n")
		for _, name := range existingAgentNames {
			fmt.Fprintf(&sb, "- %s\n", name)
		}
	}

	fmt.Fprintf(&sb, `
**Rule 5: Skill Generation**
- Skills represent specific use cases the agent can help with — NOT generic tool descriptions.
- Good skill names: "Analyze Sales Trends", "Generate Monthly Reports", "Troubleshoot Network Issues".
- Bad skill names: "Query Database", "Web Search", "Data Analysis".
- Derive skills from **both** the user's description **and** the agent's selected toolsets and connectors. Consider what specific use cases the combination of purpose + capabilities enables. For example, if the user wants a "sales agent" and you selected a SQL connector to a sales database, generate skills like "Analyze Sales Trends" or "Generate Revenue Reports" — not "Query Database".
- Generate 1-%d skills. If the request is too vague to derive meaningful use cases, omit the `+mdFieldSkills+` key entirely.

**Rule 6: Field Generation**
- The generated JSON can have these keys: `+"`name`, `description`, `systemPrompt`, `modelProvider`, `toolsets`, `connectors`"+`, and `+mdFieldSkills+`.
- **Only include a key in the JSON if you can generate a valid, non-empty value that meets all other rules.** `+"`modelProvider`"+` should always be included when models are available.
- Adhere to validation rules: `+"`name`"+` (3-40 chars), `+"`description`"+` (10-1000 chars), `+mdFieldSystemPrompt+` (up to %d chars), `+mdFieldSkills+` (array of `+"`{\"name\", \"description\"}`, `name`"+` 3-40 chars, `+"`description`"+` 10-250 chars, max %d).
- For vague or ambiguous requests, be extremely conservative. Only generate fields where the information is directly implied by the request.
- It is better to omit fields than to invent or guess details.

**Rule 7: Output Format**
- You must generate a raw JSON object with no markdown formatting, no code fences, and no explanation.
- If the user's request is insufficient to generate any valid fields, **return an empty JSON object (`+"`{}`"+`).**

Output JSON schema:
{
  "name": "string (2-4 words, no generic suffixes)",
  "description": "string (brief agent description)",
  "systemPrompt": "string (detailed system prompt for the agent)",
  "modelProvider": ["string (one alias from Available Models)"],
  "toolsets": ["string (toolset IDs to enable)"],
  "skills": [{"name": "string", "description": "string"}],
  "connectors": ["string (existing connector instance IDs)"]
}
`, maxSkillsPerResponse, maxInstructionChars, maxSkillsPerResponse)

	return sb.String()
}

func previewString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseGenerateAgentResponse(content string) (*GenerateAgentResponse, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
			content = strings.Join(lines, "\n")
		}
	}
	content = strings.TrimSpace(content)

	if content == "{}" || content == "" {
		return &GenerateAgentResponse{}, nil
	}

	var result GenerateAgentResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w", err)
	}
	return &result, nil
}

func sanitizeSkills(skills []SkillSuggestion) []SkillSuggestion {
	seen := make(map[string]bool)
	var out []SkillSuggestion
	for _, s := range skills {
		name := strings.TrimSpace(s.Name)
		desc := strings.TrimSpace(s.Description)
		if len(name) < 3 || len(desc) < 10 {
			continue
		}
		if len(name) > 40 {
			name = name[:40]
		}
		if len(desc) > 250 {
			desc = desc[:250]
		}
		normalized := strings.ToLower(name)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, SkillSuggestion{Name: name, Description: desc})
		if len(out) >= maxSkillsPerResponse {
			break
		}
	}
	return out
}

func validateAndDefaultToolsets(toolsets []string, allowed []toolsetDefinition) []string {
	validIDs := make(map[string]bool, len(allowed))
	for _, ts := range allowed {
		validIDs[ts.ID] = true
	}

	seen := make(map[string]bool)
	var result []string

	for _, id := range schema.CanonicalBuiltinGroups(toolsets) {
		if validIDs[id] && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}

	for _, ts := range allowed {
		if ts.Default && !seen[ts.ID] {
			result = append(result, ts.ID)
		}
	}

	return result
}

func ensureUniqueName(name string, existing []string) string {
	existingSet := make(map[string]bool)
	for _, n := range existing {
		existingSet[strings.ToLower(n)] = true
	}

	if !existingSet[strings.ToLower(name)] {
		return name
	}

	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s %d", name, i)
		if !existingSet[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return name
}
