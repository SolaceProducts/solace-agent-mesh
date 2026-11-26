// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	logUsedCardName       = "MCP tool name owned by another agent, using the full card name instead; two component ids end in the same hex digits"
	logNameUnavailable    = "MCP tools omitted: no available name could be built"
	logUnnameable         = "MCP tools omitted: no provider-safe name can be built from the agent and skill names"
	logIntraCardCollision = "MCP tool names collide within one agent card, only the first skill is reachable"
	logClampedToLimit     = "MCP tool names truncated to the provider limit; tool-name filter patterns must match the truncated form"
	logFilteredOut        = "MCP tools omitted: excluded by this entrypoint's include_tools/exclude_tools patterns"
)

func (a *Adapter) registerAgentTools(card *a2a.AgentCard) {
	var registered []string
	report := toolNameReport{logger: a.logger, gatewayID: a.gatewayID(), agentName: card.Name}
	prefix := toolNamePrefix(card)
	displayName := agentDisplayName(card)

	for _, skill := range card.Skills {
		resolved := a.resolveToolName(prefix, card.Name, &skill)
		toolName := resolved.name

		if !a.shouldRegisterTool(card.Name, skill.Name, toolName) {
			report.filteredOut.addTool(toolName, skill.Name, skill.ID)
			continue
		}

		if resolved.outcome == toolNameUnrepresentable {
			report.unnameable.addSkill(skill.Name, skill.ID)
			continue
		}
		if resolved.outcome == toolNameUnavailable {
			report.omitted.addContested(toolName, resolved.preferredOwner)
			continue
		}
		if slices.Contains(registered, toolName) {
			report.collided.addTool(toolName, skill.Name, skill.ID)
			continue
		}

		if resolved.outcome == toolNameFromCardName {
			report.fellBackToCardName.addContested(toolName, resolved.preferredOwner)
		}
		if resolved.clamped {
			report.truncated.addTool(toolName, skill.Name, skill.ID)
		}

		a.mcpServer.DeleteTools(toolName)

		options := []mcplib.ToolOption{
			mcplib.WithDescription(formatSkillDescription(&skill, displayName)),
			mcplib.WithTitleAnnotation(toolTitle(displayName, &skill)),
			mcplib.WithString("message",
				mcplib.Description("The message or request to send to the agent"),
				mcplib.Required()),
		}
		if a.cfg.EnableFileInput {
			options = append(options, filesArgOption(a.cfg))
		}
		tool := mcplib.NewTool(toolName, options...)

		a.mcpServer.AddTool(tool, func(ctx context.Context, request mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return a.handleToolCall(ctx, toolName, request)
		})

		a.toolMu.Lock()
		a.toolMap[toolName] = toolMapping{
			agentName:  card.Name,
			skillID:    skill.ID,
			isWorkflow: a2a.AgentTypeFromCard(*card) == "workflow",
		}
		a.toolMu.Unlock()

		registered = append(registered, toolName)
	}

	report.emit()

	a.toolMu.Lock()
	a.agentTools[card.Name] = registered
	a.toolMu.Unlock()

	if len(registered) > 0 {
		a.logger.Debug("registered MCP tools for agent",
			"agentName", card.Name,
			"tools", len(registered),
		)
	}
}

type toolNameReport struct {
	logger    *slog.Logger
	gatewayID string
	agentName string

	fellBackToCardName reportedNames
	omitted            reportedNames
	unnameable         reportedNames
	collided           reportedNames
	truncated          reportedNames
	filteredOut        reportedNames
}

type reportedNames struct {
	toolNames  []string
	skillNames []string
	skillIDs   []string
	skills     int
	owners     map[string]struct{}
}

func (r *reportedNames) addSkill(skillName, skillID string) {
	r.skillNames = append(r.skillNames, skillName)
	r.skillIDs = append(r.skillIDs, skillID)
	r.skills++
}

func (r *reportedNames) addTool(toolName, skillName, skillID string) {
	r.toolNames = append(r.toolNames, toolName)
	r.skillNames = append(r.skillNames, skillName)
	r.skillIDs = append(r.skillIDs, skillID)
	r.skills++
}

func (r *reportedNames) addContested(toolName, owner string) {
	r.toolNames = append(r.toolNames, toolName)
	r.skills++
	if owner == "" {
		return
	}
	if r.owners == nil {
		r.owners = make(map[string]struct{})
	}
	r.owners[owner] = struct{}{}
}

func (r *reportedNames) populated() bool {
	return len(r.toolNames) > 0 || len(r.skillNames) > 0
}

func (r *reportedNames) skillCount() int {
	return r.skills
}

func (r *reportedNames) conflictingAgentNames() []string {
	return slices.Sorted(maps.Keys(r.owners))
}

func (r *toolNameReport) emit() {
	if r.filteredOut.populated() {
		r.logger.Debug(logFilteredOut,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"toolNames", r.filteredOut.toolNames,
			"skillNames", r.filteredOut.skillNames,
			"skillIDs", r.filteredOut.skillIDs,
			"skillCount", r.filteredOut.skillCount(),
		)
	}
	if r.fellBackToCardName.populated() {
		r.logger.Warn(logUsedCardName,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"toolNames", r.fellBackToCardName.toolNames,
			"conflictingAgentNames", r.fellBackToCardName.conflictingAgentNames(),
		)
	}
	if r.omitted.populated() {
		r.logger.Warn(logNameUnavailable,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"toolNames", r.omitted.toolNames,
			"conflictingAgentNames", r.omitted.conflictingAgentNames(),
		)
	}
	if r.unnameable.populated() {
		r.logger.Warn(logUnnameable,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"skillNames", r.unnameable.skillNames,
			"skillIDs", r.unnameable.skillIDs,
			"skillCount", r.unnameable.skillCount(),
		)
	}
	if r.collided.populated() {
		r.logger.Warn(logIntraCardCollision,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"toolNames", r.collided.toolNames,
			"skillNames", r.collided.skillNames,
			"skillIDs", r.collided.skillIDs,
			"skillCount", r.collided.skillCount(),
		)
	}
	if r.truncated.populated() {
		r.logger.Warn(logClampedToLimit,
			"gatewayID", r.gatewayID,
			"agentName", r.agentName,
			"toolNames", r.truncated.toolNames,
			"skillNames", r.truncated.skillNames,
			"skillIDs", r.truncated.skillIDs,
			"skillCount", r.truncated.skillCount(),
		)
	}
}

const maxToolNameLen = 64

type toolNameOutcome int

const (
	toolNameFromPrefix toolNameOutcome = iota
	toolNameFromCardName
	toolNameUnavailable
	toolNameUnrepresentable
)

type resolvedToolName struct {
	name           string
	outcome        toolNameOutcome
	clamped        bool
	preferredOwner string
}

func (a *Adapter) resolveToolName(prefix, cardName string, skill *a2a.AgentSkill) resolvedToolName {
	preferred, clamped, ok := composeToolName(prefix, skill)
	if !ok {
		return resolvedToolName{outcome: toolNameUnrepresentable}
	}
	available, owner := a.toolNameAvailableTo(preferred, cardName)
	if available {
		return resolvedToolName{name: preferred, outcome: toolNameFromPrefix, clamped: clamped}
	}
	if fallback, fallbackClamped, ok := composeToolName(cardName, skill); ok {
		if fallbackAvailable, _ := a.toolNameAvailableTo(fallback, cardName); fallbackAvailable {
			return resolvedToolName{
				name:           fallback,
				outcome:        toolNameFromCardName,
				clamped:        fallbackClamped,
				preferredOwner: owner,
			}
		}
	}
	return resolvedToolName{
		name:           preferred,
		outcome:        toolNameUnavailable,
		clamped:        clamped,
		preferredOwner: owner,
	}
}

func composeToolName(prefix string, skill *a2a.AgentSkill) (name string, clamped, ok bool) {
	segment := skill.Name
	if !hasASCIIAlphanumeric(segment) {
		segment = skill.ID
	}
	if !hasASCIIAlphanumeric(prefix) || !hasASCIIAlphanumeric(segment) {
		return "", false, false
	}
	full := sanitizeToolName(prefix + "_" + segment)
	name = clampToolName(full)
	return name, name != full, true
}

func (a *Adapter) toolNameAvailableTo(toolName, cardName string) (available bool, owner string) {
	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	mapping, taken := a.toolMap[toolName]
	if !taken || mapping.agentName == cardName {
		return true, ""
	}
	return false, mapping.agentName
}

func clampToolName(name string) string {
	if len(name) <= maxToolNameLen {
		return name
	}
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(name))
	suffix := fmt.Sprintf("_%08x", digest.Sum32())

	budget := maxToolNameLen - len(suffix)
	head := make([]byte, 0, budget)
	for _, r := range name {
		if len(head)+utf8.RuneLen(r) > budget {
			break
		}
		head = utf8.AppendRune(head, r)
	}
	return strings.TrimRight(string(head), "_") + suffix
}

func toolNamePrefix(card *a2a.AgentCard) string {
	id, kind, ok := a2a.IDFromInstanceName(card.Name)
	if !ok {
		return card.Name
	}
	return string(kind) + "_" + uuidTail(id)
}

const uuidTailLen = 8

func uuidTail(id string) string {
	digits := strings.ReplaceAll(id, "-", "")
	if len(digits) <= uuidTailLen {
		return digits
	}
	return digits[len(digits)-uuidTailLen:]
}

func agentDisplayName(card *a2a.AgentCard) string {
	if name := strings.TrimSpace(a2a.DisplayNameFromCard(*card)); name != "" {
		return name
	}
	return card.Name
}

func isASCIIAlphanumeric(r rune) bool {
	return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func hasASCIIAlphanumeric(s string) bool {
	for _, r := range strings.ToLower(s) {
		if isASCIIAlphanumeric(r) {
			return true
		}
	}
	return false
}

func (a *Adapter) deregisterAgentTools(agentName string) {
	a.toolMu.Lock()
	tools := a.agentTools[agentName]
	delete(a.agentTools, agentName)
	for _, name := range tools {
		delete(a.toolMap, name)
	}
	a.toolMu.Unlock()

	if len(tools) > 0 {
		a.mcpServer.DeleteTools(tools...)
		a.logger.Debug("deregistered MCP tools for agent",
			"agentName", agentName,
			"tools", len(tools),
		)
	}
}

func (a *Adapter) shouldRegisterTool(agentName, skillName, toolName string) bool {
	names := []string{agentName, skillName, toolName}

	for i := range a.cfg.compiledExcludeExact {
		if a.cfg.compiledExcludeExact[i].matches(names) {
			return false
		}
	}
	for i := range a.cfg.compiledIncludeExact {
		if a.cfg.compiledIncludeExact[i].matches(names) {
			return true
		}
	}
	for i := range a.cfg.compiledExcludeRegex {
		if a.cfg.compiledExcludeRegex[i].matches(names) {
			return false
		}
	}
	for i := range a.cfg.compiledIncludeRegex {
		if a.cfg.compiledIncludeRegex[i].matches(names) {
			return true
		}
	}

	if len(a.cfg.compiledIncludeExact) == 0 && len(a.cfg.compiledIncludeRegex) == 0 {
		return true
	}
	return false
}

func sanitizeToolName(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))

	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		if isASCIIAlphanumeric(r) {
			sb.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore && sb.Len() > 0 {
			sb.WriteByte('_')
			prevUnderscore = true
		}
	}

	result := strings.TrimRight(sb.String(), "_")
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "tool_" + result
	}
	if result == "" {
		result = unnamedToolPlaceholder
	}
	return result
}

const unnamedToolPlaceholder = "unnamed_tool"

func formatSkillDescription(skill *a2a.AgentSkill, agentDisplayName string) string {
	var sb strings.Builder

	if agentDisplayName != "" {
		fmt.Fprintf(&sb, "Agent: %s\n\n", agentDisplayName)
	}
	sb.WriteString(skill.Description)

	if len(skill.Examples) > 0 {
		sb.WriteString("\n\nExamples:")
		for _, ex := range skill.Examples[:min(3, len(skill.Examples))] {
			fmt.Fprintf(&sb, "\n- %s", ex)
		}
	}

	if len(skill.InputModes) > 0 {
		fmt.Fprintf(&sb, "\n\nInput modes: %s", strings.Join(skill.InputModes, ", "))
	}
	if len(skill.OutputModes) > 0 {
		fmt.Fprintf(&sb, "\nOutput modes: %s", strings.Join(skill.OutputModes, ", "))
	}
	if len(skill.Tags) > 0 {
		fmt.Fprintf(&sb, "\nTags: %s", strings.Join(skill.Tags, ", "))
	}

	return sb.String()
}

func toolTitle(agentDisplayName string, skill *a2a.AgentSkill) string {
	label := strings.TrimSpace(skill.Name)
	if label == "" {
		label = skill.ID
	}
	if agentDisplayName == "" {
		return label
	}
	return agentDisplayName + ": " + label
}
