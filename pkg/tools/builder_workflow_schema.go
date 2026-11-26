// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

type wfNodeInfo struct {
	ID                   string
	Type                 string
	AgentName            string
	DependsOn            []string
	Input                map[string]any
	OutputSchemaOverride map[string]any
	Cases                []wfSwitchCase
	ItemsExpression      any
	RouteOn              string
	Escalate             string
	EscalationMode       string
	EscalationAnswer     string
}

type wfSwitchCase struct {
	Condition string
	Node      string
}

type wfAgentSchema struct {
	Name         string
	InputSchema  map[string]any
	OutputSchema map[string]any
}

type wfTemplateRef struct {
	NodeID       string
	FieldPath    []string
	Raw          string
	Rescuable    bool
	ViaInputNode bool
}

type wfFindings struct {
	errors   []schema.ValidationError
	warnings []schema.ValidationError
}

func (f *wfFindings) addError(e *schema.ValidationError) {
	if e != nil {
		f.errors = append(f.errors, *e)
	}
}

func (f *wfFindings) addWarning(w *schema.ValidationError) {
	if w != nil {
		f.warnings = append(f.warnings, *w)
	}
}

func (f *wfFindings) merge(other wfFindings) {
	f.errors = append(f.errors, other.errors...)
	f.warnings = append(f.warnings, other.warnings...)
}

var workflowLoadCheck func(name string, appConfig map[string]any) error

func RegisterWorkflowLoadCheck(check func(name string, appConfig map[string]any) error) {
	workflowLoadCheck = check
}

var workflowToolNodeAdvisories func(appConfig map[string]any) []string

func RegisterWorkflowToolNodeAdvisories(report func(appConfig map[string]any) []string) {
	workflowToolNodeAdvisories = report
}

var ErrWorkflowMissingDependency = errors.New("workflow node depends on a non-existent node")

var ErrWorkflowFailsAtRun = errors.New("workflow tool node fails whenever it runs")

func workflowLoadFinding(name string, appCfg map[string]any, schemaErrors []schema.ValidationError) *schema.ValidationError {
	if workflowLoadCheck == nil {
		return nil
	}
	err := workflowLoadCheck(name, appCfg)
	if err == nil || (errors.Is(err, ErrWorkflowMissingDependency) && reportsMissingDependency(schemaErrors)) {
		return nil
	}
	detail := strings.TrimPrefix(err.Error(), fmt.Sprintf("workflow %q: ", name))
	if errors.Is(err, ErrWorkflowFailsAtRun) {
		return &schema.ValidationError{
			Path:    name,
			Message: fmt.Sprintf("workflow '%s' would be refused at save and deploy, because a tool node fails whenever it runs: %s", name, detail),
		}
	}
	return &schema.ValidationError{
		Path: name,
		Message: fmt.Sprintf("workflow '%s' would fail to load when deployed: %s "+
			"(the loader stops at the first problem, so validate again after fixing it)", name, detail),
	}
}

func validateWorkflowSchemas(ctx context.Context, components []map[string]any, toolCtx *tool.Context) wfFindings {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return wfFindings{}
	}
	facade := toolCtx.Facade()

	type compInfo struct {
		name     string
		typ      string
		filename string
	}
	var comps []compInfo
	for _, c := range components {
		name, _ := c["name"].(string)
		typ, _ := c["type"].(string)
		filename, _ := c["filename"].(string)
		if name != "" && typ != "" && !isBlockedComponent(c) {
			comps = append(comps, compInfo{name: name, typ: typ, filename: filename})
		}
	}

	hasWorkflow := false
	for _, c := range comps {
		if c.typ == "workflow" {
			hasWorkflow = true
			break
		}
	}
	if !hasWorkflow {
		return wfFindings{}
	}

	agents := map[string]wfAgentSchema{}
	for _, c := range comps {
		if c.typ != "agent" || c.filename == "" {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, c.filename, nil)
		if err != nil || len(data) == 0 {
			continue
		}
		appCfg := wfExtractAppConfig(data)
		if appCfg == nil {
			continue
		}
		agentName, _ := appCfg["agent_name"].(string)
		if agentName == "" {
			agentName = c.name
		}
		as := wfAgentSchema{Name: agentName}
		if is, ok := appCfg["input_schema"].(map[string]any); ok {
			as.InputSchema = is
		}
		if os, ok := appCfg["output_schema"].(map[string]any); ok {
			as.OutputSchema = os
		}
		agents[agentName] = as
	}

	var findings wfFindings
	for _, c := range comps {
		if c.typ != "workflow" || c.filename == "" {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, c.filename, nil)
		if err != nil || len(data) == 0 {
			findings.errors = append(findings.errors, schema.ValidationError{
				Path:    c.name,
				Message: fmt.Sprintf("workflow '%s': could not load config '%s'", c.name, c.filename),
			})
			continue
		}
		appCfg := wfExtractAppConfig(data)
		if appCfg == nil {
			continue
		}
		schemaFindings := checkWorkflowSchema(c.name, appCfg, agents)
		findings.merge(schemaFindings)
		findings.addError(workflowLoadFinding(c.name, appCfg, schemaFindings.errors))
		findings.merge(workflowToolNodeAdvisoryFindings(c.name, appCfg))
	}
	return findings
}

func workflowToolNodeAdvisoryFindings(name string, appCfg map[string]any) wfFindings {
	var findings wfFindings
	if workflowToolNodeAdvisories == nil {
		return findings
	}
	for _, advisory := range workflowToolNodeAdvisories(appCfg) {
		findings.addWarning(&schema.ValidationError{
			Path:    name,
			Message: fmt.Sprintf("workflow '%s': %s", name, advisory),
		})
	}
	return findings
}

func wfExtractAppConfig(data []byte) map[string]any {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil
		}
	}
	root, ok := wfAsStringMap(doc)
	if !ok {
		return nil
	}
	appsRaw, ok := root["apps"]
	if !ok {
		return nil
	}
	apps, ok := appsRaw.([]any)
	if !ok || len(apps) == 0 {
		return nil
	}
	app, ok := wfAsStringMap(apps[0])
	if !ok {
		return nil
	}
	ac, ok := app["app_config"]
	if !ok {
		return nil
	}
	m, ok := wfAsStringMap(ac)
	if !ok {
		return nil
	}
	return m
}

func checkWorkflowSchema(workflowName string, appConfig map[string]any, agents map[string]wfAgentSchema) wfFindings {
	body := wfWorkflowBody(appConfig)
	nodesRaw, ok := body["nodes"].([]any)
	if !ok || len(nodesRaw) == 0 {
		return wfFindings{}
	}
	nodes, nodeByID := wfParseNodes(nodesRaw)
	workflowInputSchema, _ := wfAsStringMap(wfFirstPresent(body, wfInputSchemaKeys...))
	prefix := fmt.Sprintf("workflow '%s'", workflowName)

	var findings wfFindings
	findings.errors = append(findings.errors, wfCheckDependsOn(prefix, nodes, nodeByID)...)

	refChecker := wfRefChecker{
		prefix:     prefix,
		nodeByID:   nodeByID,
		agents:     agents,
		inputProps: wfSchemaProperties(workflowInputSchema),
	}
	findings.merge(refChecker.checkNodeRefs(nodes))
	findings.errors = append(findings.errors, wfCheckRequiredInputs(prefix, nodes, agents)...)
	findings.errors = append(findings.errors, wfCheckWholeOutputInputs(prefix, nodes, nodeByID, agents)...)
	findings.merge(refChecker.checkOutputMapping(wfFirstPresent(body, wfOutputMappingKeys...)))
	return findings
}

func wfParseNodes(nodesRaw []any) ([]*wfNodeInfo, map[string]*wfNodeInfo) {
	nodes := make([]*wfNodeInfo, 0, len(nodesRaw))
	nodeByID := map[string]*wfNodeInfo{}
	for _, raw := range nodesRaw {
		n := wfParseNodeInfo(raw)
		if n == nil {
			continue
		}
		nodes = append(nodes, n)
		nodeByID[n.ID] = n
	}
	for _, n := range nodes {
		n.EscalationAnswer = wfEscalationAnswer(n, nodeByID)
	}
	return nodes, nodeByID
}

func wfEscalationAnswer(n *wfNodeInfo, nodeByID map[string]*wfNodeInfo) string {
	if n.Type != "decision" || n.Escalate == "" || n.RouteOn == "" || n.EscalationMode == "handoff" {
		return ""
	}
	if target, ok := nodeByID[n.Escalate]; ok && target.Type == "decision" {
		return ""
	}
	return fmt.Sprintf("{{%s.output.%s}}", n.Escalate, n.RouteOn)
}

const dependsOnPathSuffix = ".depends_on"

func reportsMissingDependency(errs []schema.ValidationError) bool {
	return slices.ContainsFunc(errs, func(e schema.ValidationError) bool { return strings.HasSuffix(e.Path, dependsOnPathSuffix) })
}

func wfCheckDependsOn(prefix string, nodes []*wfNodeInfo, nodeByID map[string]*wfNodeInfo) []schema.ValidationError {
	var errs []schema.ValidationError
	for _, n := range nodes {
		for _, dep := range n.DependsOn {
			if _, ok := nodeByID[dep]; !ok {
				errs = append(errs, schema.ValidationError{
					Path:    fmt.Sprintf("%s.node '%s'"+dependsOnPathSuffix, prefix, n.ID),
					Message: fmt.Sprintf("node '%s' depends_on '%s' which does not exist as a workflow node", n.ID, dep),
				})
			}
		}
	}
	return errs
}

func wfCheckRequiredInputs(prefix string, nodes []*wfNodeInfo, agents map[string]wfAgentSchema) []schema.ValidationError {
	var errs []schema.ValidationError
	for _, n := range nodes {
		for _, field := range wfMissingRequiredInputs(n, agents) {
			errs = append(errs, schema.ValidationError{
				Path:    fmt.Sprintf("%s.node '%s'.input", prefix, n.ID),
				Message: fmt.Sprintf("node '%s' missing required input '%s' (from %s input_schema)", n.ID, field, n.AgentName),
			})
		}
	}
	return errs
}

func wfMissingRequiredInputs(n *wfNodeInfo, agents map[string]wfAgentSchema) []string {
	if n.Type != "agent" || n.AgentName == "" {
		return nil
	}
	ac, ok := agents[n.AgentName]
	if !ok || ac.InputSchema == nil {
		return nil
	}
	var missing []string
	for _, field := range wfSchemaRequired(ac.InputSchema) {
		if _, provided := n.Input[field]; !provided {
			missing = append(missing, field)
		}
	}
	return missing
}

func wfCheckWholeOutputInputs(prefix string, nodes []*wfNodeInfo, nodeByID map[string]*wfNodeInfo, agents map[string]wfAgentSchema) []schema.ValidationError {
	var errs []schema.ValidationError
	for _, n := range nodes {
		if n.Type != "agent" || n.AgentName == "" || n.Input == nil {
			continue
		}
		ac, ok := agents[n.AgentName]
		if !ok || ac.InputSchema == nil {
			continue
		}
		inputProps := wfSchemaProperties(ac.InputSchema)
		if inputProps == nil {
			continue
		}
		for key, val := range n.Input {
			errs = append(errs, wfCheckWholeOutputInput(prefix, n, key, val, inputProps, nodeByID, agents)...)
		}
	}
	return errs
}

func wfCheckWholeOutputInput(prefix string, n *wfNodeInfo, key string, val any, inputProps map[string]any, nodeByID map[string]*wfNodeInfo, agents map[string]wfAgentSchema) []schema.ValidationError {
	refs := wfExtractTemplateRefsFromValue(val)
	if len(refs) != 1 || len(refs[0].FieldPath) > 0 {
		return nil
	}
	ref := refs[0]
	if ref.NodeID == "workflow" || ref.NodeID == "_map_item" {
		return nil
	}
	upstream, exists := nodeByID[ref.NodeID]
	if !exists || upstream.Type != "agent" {
		return nil
	}
	upstreamSchema := wfResolveOutputSchema(upstream, agents)
	fieldSchemaMap, ok := wfAsStringMap(inputProps[key])
	if !ok || wfSchemaIsComposite(upstreamSchema) {
		return nil
	}
	upstreamFields, _ := wfDeclaredOutputFields(upstreamSchema)
	if upstreamFields == nil {
		return nil
	}
	var errs []schema.ValidationError
	for _, reqField := range wfSchemaRequired(fieldSchemaMap) {
		if !slices.Contains(upstreamFields, reqField) {
			errs = append(errs, schema.ValidationError{
				Path: fmt.Sprintf("%s.node '%s'.input.%s", prefix, n.ID, key),
				Message: fmt.Sprintf("node '%s' input '%s' ← %s output missing required field '%s'",
					n.ID, key, ref.NodeID, reqField),
			})
		}
	}
	return errs
}

func wfParseNodeInfo(raw any) *wfNodeInfo {
	m, ok := wfAsStringMap(raw)
	if !ok {
		return nil
	}
	n := &wfNodeInfo{
		ID:   wfStrVal(m, "id"),
		Type: wfStrVal(m, "type"),
	}
	if n.ID == "" {
		return nil
	}
	n.AgentName = wfStrVal(m, "agent_name")

	if deps := wfFirstList(m, wfDependsOnKeys...); deps != nil {
		for _, d := range deps {
			if s, ok := d.(string); ok {
				n.DependsOn = append(n.DependsOn, s)
			}
		}
	}
	if inp, ok := m["input"]; ok {
		if im, ok := wfAsStringMap(inp); ok {
			n.Input = im
		}
	}
	if oso, ok := m["output_schema_override"]; ok {
		if om, ok := wfAsStringMap(oso); ok {
			n.OutputSchemaOverride = om
		}
	}
	if cases, ok := m["cases"].([]any); ok {
		for _, c := range cases {
			cm, ok := wfAsStringMap(c)
			if !ok {
				continue
			}
			n.Cases = append(n.Cases, wfSwitchCase{
				Condition: wfStrVal(cm, "condition"),
				Node:      wfStrVal(cm, "node"),
			})
		}
	}
	n.ItemsExpression = wfFirstPresent(m, wfMapItemsKeys...)
	if n.Type == "decision" {
		n.RouteOn, _ = wfFirstPresent(m, wfRouteOnKeys...).(string)
		n.Escalate = wfStrVal(m, "escalate")
		n.EscalationMode, _ = wfFirstPresent(m, wfEscalationModeKeys...).(string)
	}
	return n
}

func wfWorkflowBody(appConfig map[string]any) map[string]any {
	if wf, ok := wfAsStringMap(appConfig["workflow"]); ok {
		if _, hasNodes := wf["nodes"]; hasNodes {
			return wf
		}
	}
	return appConfig
}

func wfStrVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

var wfMapItemsKeys = []string{"items", "with_param", "withParam", "with_items", "withItems", "collection", "source_expression"}

var (
	wfDependsOnKeys      = []string{"depends_on", "dependencies"}
	wfInputSchemaKeys    = []string{"input_schema", "inputSchema"}
	wfOutputMappingKeys  = []string{"output_mapping", "outputMapping"}
	wfRouteOnKeys        = []string{"route_on", "routeOn"}
	wfEscalationModeKeys = []string{"escalation_mode", "escalationMode"}
)

func wfFirstList(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if list, ok := m[k].([]any); ok {
			return list
		}
	}
	return nil
}

func wfFirstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

type wfLocatedRef struct {
	referrer string
	kind     wfRefSiteKind
	ref      wfTemplateRef
}

type wfRefSiteKind int

const (
	wfSiteNodeInput wfRefSiteKind = iota
	wfSiteSwitchCase
	wfSiteMapItems
	wfSiteOutputMapping
	wfSiteEscalationAnswer
)

func (k wfRefSiteKind) wholeOutputUsable() bool {
	return k == wfSiteNodeInput || k == wfSiteOutputMapping
}

func (k wfRefSiteKind) unresolvedEffect() string {
	switch k {
	case wfSiteSwitchCase:
		return "makes the case not match"
	case wfSiteOutputMapping:
		return "leaves the template text in the workflow result"
	case wfSiteEscalationAnswer:
		return "fails the escalate node"
	default:
		return "fails the node"
	}
}

func (k wfRefSiteKind) optionalInputAdvice(fieldName string) string {
	switch k {
	case wfSiteNodeInput, wfSiteOutputMapping:
		return fmt.Sprintf("reference it as '{{workflow.input.%s}}' if it is optional", fieldName)
	case wfSiteSwitchCase:
		return "declare it in properties alone if it is optional, so the case does not match when it is omitted"
	default:
		return ""
	}
}

func wfExtractAllTemplateRefs(n *wfNodeInfo) []wfLocatedRef {
	var refs []wfLocatedRef
	appendRefs := func(referrer string, kind wfRefSiteKind, found []wfTemplateRef) {
		for _, r := range found {
			refs = append(refs, wfLocatedRef{referrer: referrer, kind: kind, ref: r})
		}
	}
	for _, key := range wfMapKeys(n.Input) {
		appendRefs(fmt.Sprintf("node '%s' input '%s'", n.ID, key), wfSiteNodeInput, wfExtractTemplateRefsFromValue(n.Input[key]))
	}
	for _, c := range n.Cases {
		appendRefs(fmt.Sprintf("node '%s' switch case", n.ID), wfSiteSwitchCase, wfExtractTemplateRefsFromString(c.Condition))
	}
	if n.ItemsExpression != nil {
		appendRefs(fmt.Sprintf("node '%s' items", n.ID), wfSiteMapItems, wfExtractTemplateRefsFromValue(n.ItemsExpression))
	}
	if n.EscalationAnswer != "" {
		appendRefs(fmt.Sprintf("node '%s' escalation answer", n.ID), wfSiteEscalationAnswer, wfExtractTemplateRefsFromString(n.EscalationAnswer))
	}
	return refs
}

var wfEmbeddedTplRe = regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)

func wfExtractTemplateRefsFromValue(val any) []wfTemplateRef {
	return wfCollectTemplateRefs(val, false)
}

func wfCollectTemplateRefs(val any, rescuable bool) []wfTemplateRef {
	switch v := val.(type) {
	case string:
		refs := wfExtractTemplateRefsFromString(v)
		for i := range refs {
			refs[i].Rescuable = rescuable
		}
		return refs
	case map[string]any, map[any]any:
		m, _ := wfAsStringMap(v)
		return wfCollectMapTemplateRefs(m, rescuable)
	case []any:
		var refs []wfTemplateRef
		for _, item := range v {
			refs = append(refs, wfCollectTemplateRefs(item, rescuable)...)
		}
		return refs
	default:
		return nil
	}
}

func wfCollectMapTemplateRefs(m map[string]any, rescuable bool) []wfTemplateRef {
	inOperator := rescuable || wfIsOperatorMap(m)
	var refs []wfTemplateRef
	for _, key := range wfMapKeys(m) {
		refs = append(refs, wfCollectTemplateRefs(m[key], inOperator)...)
	}
	return refs
}

func wfIsOperatorMap(m map[string]any) bool {
	if len(m) != 1 {
		return false
	}
	_, coalesce := m["coalesce"]
	_, concat := m["concat"]
	return coalesce || concat
}

func wfExtractTemplateRefsFromString(s string) []wfTemplateRef {
	matches := wfEmbeddedTplRe.FindAllStringSubmatch(s, -1)
	var refs []wfTemplateRef
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		ref := wfParseTemplateRef(m[1], m[0])
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	return refs
}

const wfWorkflowInputNodeID = "workflow_input"

func wfRefPathSegments(path string) (parts []string, viaInputNode bool) {
	path = strings.ReplaceAll(path, "workflow.parameters.", "workflow.input.")
	parts = strings.Split(strings.TrimSpace(path), ".")
	if len(parts) >= 2 && parts[0] == wfWorkflowInputNodeID && parts[1] == "output" {
		return append([]string{"workflow", "input"}, parts[2:]...), true
	}
	return parts, false
}

func wfParseTemplateRef(path, raw string) *wfTemplateRef {
	parts, viaInputNode := wfRefPathSegments(path)
	if len(parts) < 2 {
		return nil
	}

	nodeID := parts[0]

	if nodeID == "workflow" {
		if len(parts) >= 2 && (parts[1] == "input" || parts[1] == "parameters") {
			ref := &wfTemplateRef{NodeID: "workflow", Raw: raw, ViaInputNode: viaInputNode}
			if len(parts) > 2 {
				ref.FieldPath = parts[2:]
			}
			return ref
		}
		return nil
	}

	if parts[1] == "output" {
		ref := &wfTemplateRef{NodeID: nodeID, Raw: raw}
		if len(parts) > 2 {
			ref.FieldPath = parts[2:]
		}
		return ref
	}

	if nodeID == "_map_item" || nodeID == "_map_index" || nodeID == "_loop_iteration" {
		return &wfTemplateRef{NodeID: nodeID, FieldPath: parts[1:], Raw: raw}
	}

	return nil
}

type wfRefSite struct {
	path     string
	referrer string
	kind     wfRefSiteKind
}

type wfRefChecker struct {
	prefix     string
	nodeByID   map[string]*wfNodeInfo
	agents     map[string]wfAgentSchema
	inputProps map[string]any
}

func (c wfRefChecker) checkNodeRefs(nodes []*wfNodeInfo) wfFindings {
	var findings wfFindings
	for _, n := range nodes {
		for _, loc := range wfExtractAllTemplateRefs(n) {
			site := wfRefSite{
				path:     fmt.Sprintf("%s.node '%s'", c.prefix, n.ID),
				referrer: loc.referrer,
				kind:     loc.kind,
			}
			c.check(site, loc.ref, &findings)
		}
	}
	return findings
}

func (c wfRefChecker) checkOutputMapping(outputMapping any) wfFindings {
	mapping, ok := wfAsStringMap(outputMapping)
	if !ok {
		return wfFindings{}
	}
	var findings wfFindings
	for _, key := range wfMapKeys(mapping) {
		site := wfRefSite{
			path:     fmt.Sprintf("%s.output_mapping.%s", c.prefix, key),
			referrer: fmt.Sprintf("output_mapping '%s'", key),
			kind:     wfSiteOutputMapping,
		}
		for _, ref := range wfExtractTemplateRefsFromValue(mapping[key]) {
			c.check(site, ref, &findings)
		}
	}
	return findings
}

func (c wfRefChecker) check(site wfRefSite, ref wfTemplateRef, findings *wfFindings) {
	switch ref.NodeID {
	case "workflow":
		c.checkWorkflowInputRef(site, ref, findings)
	case "_map_item", "_map_index", "_loop_iteration":
	default:
		findings.addError(c.checkNodeRef(site, ref))
	}
}

func (c wfRefChecker) checkNodeRef(site wfRefSite, ref wfTemplateRef) *schema.ValidationError {
	target, exists := c.nodeByID[ref.NodeID]
	if !exists {
		if ref.Rescuable {
			return nil
		}
		return &schema.ValidationError{
			Path:    site.path,
			Message: fmt.Sprintf("%s references non-existent node '%s' in '%s'", site.referrer, ref.NodeID, ref.Raw),
		}
	}
	if len(ref.FieldPath) == 0 {
		return nil
	}
	return c.checkOutputFieldRef(site, ref, target)
}

func (c wfRefChecker) checkWorkflowInputRef(site wfRefSite, ref wfTemplateRef, findings *wfFindings) {
	if len(ref.FieldPath) == 0 || c.inputProps == nil {
		return
	}
	fieldName := ref.FieldPath[0]
	if _, ok := c.inputProps[fieldName]; ok {
		return
	}
	declared := wfMapKeys(c.inputProps)
	notDeclared := fmt.Sprintf("%s references '%s' but '%s' is not in workflow input_schema properties [%s]",
		site.referrer, ref.Raw, fieldName, strings.Join(declared, ", "))
	if intended, ok := wfClosestName(fieldName, declared); ok {
		findings.addError(&schema.ValidationError{
			Path: site.path,
			Message: fmt.Sprintf("%s: did you mean '%s'? If '%s' is a separate input, declare it in input_schema properties",
				notDeclared, intended, fieldName),
		})
		return
	}
	if ref.ViaInputNode {
		findings.addError(&schema.ValidationError{
			Path:    site.path,
			Message: notDeclared + wfInputNodeMissConsequence(site.kind, fieldName),
		})
		return
	}
	findings.addWarning(&schema.ValidationError{Path: site.path, Message: notDeclared})
}

func wfInputNodeMissConsequence(kind wfRefSiteKind, fieldName string) string {
	consequence := fmt.Sprintf(", and read through workflow_input.output an input the caller omits %s: "+
		"declare it in input_schema properties and required if callers must supply it", kind.unresolvedEffect())
	advice := kind.optionalInputAdvice(fieldName)
	if advice == "" {
		return consequence
	}
	return consequence + ", or " + advice
}

func wfClosestName(name string, candidates []string) (string, bool) {
	best, bestDistance := "", wfTypoDistance(name)+1
	for _, candidate := range candidates {
		if d := wfEditDistance(strings.ToLower(name), strings.ToLower(candidate)); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	return best, best != ""
}

func wfTypoDistance(name string) int {
	switch n := utf8.RuneCountInString(name); {
	case n <= 2:
		return 0
	case n <= 5:
		return 1
	default:
		return 2
	}
}

func wfEditDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	d := make([][]int, len(ar)+1)
	for i := range d {
		d[i] = make([]int, len(br)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ar)][len(br)]
}

func (c wfRefChecker) checkOutputFieldRef(site wfRefSite, ref wfTemplateRef, target *wfNodeInfo) *schema.ValidationError {
	if target.Type != "agent" {
		return nil
	}
	outputSchema := wfResolveOutputSchema(target, c.agents)
	if wfSchemaIsComposite(outputSchema) {
		return nil
	}
	fieldName := ref.FieldPath[0]
	if fields, declaredAs := wfDeclaredOutputFields(outputSchema); fields != nil {
		if slices.Contains(fields, fieldName) {
			return nil
		}
		return &schema.ValidationError{
			Path: site.path,
			Message: fmt.Sprintf("%s references '%s' but '%s' not in %s's output schema %s [%s]",
				site.referrer, ref.Raw, fieldName, ref.NodeID, declaredAs, strings.Join(fields, ", ")),
		}
	}
	if target.OutputSchemaOverride != nil {
		return &schema.ValidationError{
			Path: site.path,
			Message: fmt.Sprintf("%s references '%s', but the output_schema_override of node '%s' declares no properties, "+
				"so its output has no guaranteed fields: add '%s' to it and instruct the agent to return it%s",
				site.referrer, ref.Raw, ref.NodeID, fieldName, wfWholeOutputAlternative(site, ref.NodeID)),
		}
	}
	if _, inBuild := c.agents[target.AgentName]; !inBuild {
		return nil
	}
	return &schema.ValidationError{
		Path: site.path,
		Message: fmt.Sprintf("%s references '%s', but node '%s' declares no output schema, "+
			"so its output has no guaranteed fields: declare '%s' in the node's output_schema_override "+
			"(or the output_schema of agent '%s') and instruct the agent to return it%s",
			site.referrer, ref.Raw, ref.NodeID, fieldName, target.AgentName, wfWholeOutputAlternative(site, ref.NodeID)),
	}
}

func wfWholeOutputAlternative(site wfRefSite, nodeID string) string {
	if !site.kind.wholeOutputUsable() {
		return ""
	}
	return fmt.Sprintf(", or map the whole '{{%s.output}}'", nodeID)
}

func wfDeclaredOutputFields(s map[string]any) ([]string, string) {
	props := wfSchemaProperties(s)
	required := wfSchemaRequired(s)
	if props == nil && len(required) == 0 {
		return nil, ""
	}
	fields := append(wfMapKeys(props), required...)
	slices.Sort(fields)
	fields = slices.Compact(fields)
	if props == nil {
		return fields, "required fields"
	}
	return fields, "properties"
}

func wfResolveOutputSchema(n *wfNodeInfo, agents map[string]wfAgentSchema) map[string]any {
	if n.OutputSchemaOverride != nil {
		return n.OutputSchemaOverride
	}
	if n.AgentName != "" {
		if ac, ok := agents[n.AgentName]; ok {
			return ac.OutputSchema
		}
	}
	return nil
}

func wfSchemaProperties(s map[string]any) map[string]any {
	if s == nil {
		return nil
	}
	props, ok := s["properties"]
	if !ok {
		return nil
	}
	m, ok := wfAsStringMap(props)
	if !ok {
		return nil
	}
	return m
}

var wfCompositeSchemaKeys = []string{"allOf", "oneOf", "anyOf", "$ref"}

func wfSchemaIsComposite(s map[string]any) bool {
	for _, k := range wfCompositeSchemaKeys {
		if _, ok := s[k]; ok {
			return true
		}
	}
	return false
}

func wfSchemaRequired(s map[string]any) []string {
	if s == nil {
		return nil
	}
	req, ok := s["required"].([]any)
	if !ok {
		return nil
	}
	var fields []string
	for _, r := range req {
		if str, ok := r.(string); ok {
			fields = append(fields, str)
		}
	}
	return fields
}

func wfMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := range len(keys) {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func wfAsStringMap(v any) (map[string]any, bool) {
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	if m, ok := v.(map[any]any); ok {
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprintf("%v", k)] = val
		}
		return out, true
	}
	return nil, false
}
