// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/timeutil"
)

type WorkflowConfig struct {
	Name        string
	DisplayName string
	Namespace   string
	Description string
	Version     string

	InputSchema   map[string]any
	OutputSchema  map[string]any
	Nodes         []*NodeConfig
	OutputMapping map[string]any

	Skills []map[string]any

	Tools []any

	Connectors []any

	supersededBodyTools []string

	Toolsets []string

	WorkflowTimeout    time.Duration
	DefaultNodeTimeout time.Duration

	CardPublishIntervalSeconds int

	FailFast      bool
	MaxCallDepth  int
	RetryStrategy *RetryStrategy
	OnExit        *ExitHandler

	HILDatabaseURL string

	Decision *DecisionProviderConfig
}

type DecisionProviderConfig struct {
	Provider              string
	ModelAlias            string
	APIBase               string
	APIKey                string
	Model                 string
	TimeoutSeconds        int
	MaxStateBytes         int
	MaxImages             int
	MaxImageBytes         int
	MaxMedia              int
	MaxMediaBytes         int
	ReplacedWorkflowBlock bool
}

type NodeConfig struct {
	ID        string
	Type      string
	DependsOn []string

	When    string
	Timeout time.Duration
	Input   map[string]any

	Retry *RetryStrategy

	Instruction string

	AgentName            string
	AgentInstanceName    string
	InputSchemaOverride  map[string]any
	OutputSchemaOverride map[string]any

	Cases       []SwitchCase
	DefaultCase string

	BodyNode      string
	Condition     string
	MaxIterations int
	Delay         time.Duration

	ItemsExpression  any
	TemplateNode     string
	MaxItems         int
	ConcurrencyLimit int

	WorkflowName string

	WaitDuration time.Duration
	Signal       string

	ToolName  string
	Connector string
	Tool      string

	Questions       []decision.Question
	RouteOn         string
	Routes          map[string]string
	MinConfidence   float64
	Escalate        string
	EscalationMode  string
	Default         string
	OnProviderError string
	Model           string
	Images          []string
	ImageResolution string
	Documents       []string
	Audio           []string
	Video           []string
	VideoResolution string

	implicitWaits []string
}

func (n *NodeConfig) waitsFor() []string {
	if len(n.implicitWaits) == 0 {
		return n.DependsOn
	}
	return append(slices.Clone(n.DependsOn), n.implicitWaits...)
}

type SwitchCase struct {
	Condition string
	Node      string
}

type RetryStrategy struct {
	Limit       int
	RetryPolicy string
	Backoff     *BackoffStrategy
}

type BackoffStrategy struct {
	Duration    time.Duration
	Factor      float64
	MaxDuration time.Duration
}

type ExitHandler struct {
	Always    string
	OnSuccess string
	OnFailure string
	OnCancel  string
}

type DependencyGraph struct {
	Nodes      map[string]*NodeConfig
	Forward    map[string][]string
	Reverse    map[string][]string
	InnerNodes map[string]bool
	Order      []string
}

const DefaultNodeTimeout = 5 * time.Minute

const workflowErrFmt = "workflow %q: %w"

func ParseWorkflowConfig(name, namespace string, raw map[string]any) (*WorkflowConfig, error) {
	cfg := &WorkflowConfig{
		Name:                       name,
		Namespace:                  namespace,
		Version:                    "1.0.0",
		FailFast:                   true,
		MaxCallDepth:               3,
		CardPublishIntervalSeconds: 60,
		DefaultNodeTimeout:         DefaultNodeTimeout,
	}

	cfg.Description = getString(raw, "description")
	cfg.DisplayName = getStringAlt(raw, "display_name", "displayName")
	if v := getString(raw, "version"); v != "" {
		cfg.Version = v
	}

	cfg.InputSchema = getMap(raw, "input_schema", "inputSchema")
	cfg.OutputSchema = getMap(raw, "output_schema", "outputSchema")
	cfg.OutputMapping = getMap(raw, "output_mapping", "outputMapping")

	if cfg.OutputMapping == nil {
		return nil, fmt.Errorf("workflow %q: output_mapping is required", name)
	}

	cfg.Skills = getMapSlice(raw, "skills")

	cfg.Tools, _ = getAny(raw, "tools").([]any)
	cfg.Connectors, _ = getAny(raw, "connectors").([]any)

	if v := getAny(raw, "workflow_timeout", "workflowTimeout"); v != nil {
		if s, ok := v.(string); ok {
			d, err := timeutil.ParseDuration(s)
			if err != nil {
				return nil, fmt.Errorf("workflow %q: invalid workflow_timeout %q: %w", name, s, err)
			}
			cfg.WorkflowTimeout = d
		}
	}
	if v := getAny(raw, "default_node_timeout", "defaultNodeTimeout"); v != nil {
		if s, ok := v.(string); ok {
			d, err := timeutil.ParseDuration(s)
			if err != nil {
				return nil, fmt.Errorf("workflow %q: invalid default_node_timeout %q: %w", name, s, err)
			}
			cfg.DefaultNodeTimeout = d
		}
	}

	if v := getAny(raw, "fail_fast", "failFast"); v != nil {
		if b, ok := v.(bool); ok {
			cfg.FailFast = b
		}
	}
	if v := getAny(raw, "max_call_depth", "maxCallDepth"); v != nil {
		cfg.MaxCallDepth = toInt(v)
	}

	if retryRaw := getMap(raw, "retry_strategy", "retryStrategy"); retryRaw != nil {
		rs, err := parseRetryStrategy(retryRaw)
		if err != nil {
			return nil, fmt.Errorf(workflowErrFmt, name, err)
		}
		cfg.RetryStrategy = rs
	}

	if decisionRaw := getMap(raw, "decision"); decisionRaw != nil {
		provider, err := ParseDecisionProvider(decisionRaw)
		if err != nil {
			return nil, fmt.Errorf(workflowErrFmt, name, err)
		}
		cfg.Decision = provider
	}

	if v := getAny(raw, "card_publish_interval_seconds", "cardPublishIntervalSeconds"); v != nil {
		cfg.CardPublishIntervalSeconds = toInt(v)
	}

	if exitRaw := getAny(raw, "on_exit", "onExit"); exitRaw != nil {
		eh, err := parseExitHandler(exitRaw)
		if err != nil {
			return nil, fmt.Errorf(workflowErrFmt, name, err)
		}
		cfg.OnExit = eh
	}

	nodesRaw, ok := raw["nodes"].([]any)
	if !ok || len(nodesRaw) == 0 {
		return nil, fmt.Errorf("workflow %q: at least one node is required", name)
	}

	for i, nodeRaw := range nodesRaw {
		nodeMap, ok := nodeRaw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("workflow %q: node[%d] is not a map", name, i)
		}
		node, err := parseNodeConfig(nodeMap, cfg.DefaultNodeTimeout)
		if err != nil {
			return nil, fmt.Errorf("workflow %q: node[%d]: %w", name, i, err)
		}
		cfg.Nodes = append(cfg.Nodes, node)
	}

	if err := validateDurableWaits(cfg); err != nil {
		return nil, fmt.Errorf(workflowErrFmt, name, err)
	}

	return cfg, nil
}

func (cfg *WorkflowConfig) MergeAppConfigToolsAndConnectors(tools, connectors []any) {
	cfg.Tools, cfg.supersededBodyTools = mergeToolEntries(tools, cfg.Tools)
	cfg.Connectors = appConfigFirst(connectors, cfg.Connectors)
}

func mergeToolEntries(appConfig, body []any) (merged []any, superseded []string) {
	if len(appConfig) == 0 {
		return body, nil
	}

	claimed := make(map[string]bool, len(appConfig))
	for _, entry := range appConfig {
		if name, ok := builtinToolName(entry); ok {
			claimed[name] = true
		}
	}

	merged = make([]any, 0, len(appConfig)+len(body))
	merged = append(merged, appConfig...)
	for _, entry := range body {
		if name, ok := builtinToolName(entry); ok && claimed[name] {
			superseded = append(superseded, name)
			continue
		}
		merged = append(merged, entry)
	}
	return merged, superseded
}

func builtinToolName(entry any) (string, bool) {
	m, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}
	if toolType, _ := m["tool_type"].(string); toolType != "builtin" {
		return "", false
	}
	name, _ := m["tool_name"].(string)
	return name, name != ""
}

func appConfigFirst(appConfig, body []any) []any {
	if len(appConfig) == 0 {
		return body
	}
	merged := make([]any, 0, len(appConfig)+len(body))
	merged = append(merged, appConfig...)
	return append(merged, body...)
}

func parseNodeConfig(raw map[string]any, defaultTimeout time.Duration) (*NodeConfig, error) {
	node := &NodeConfig{}

	node.ID = getString(raw, "id")
	if node.ID == "" {
		return nil, fmt.Errorf("node missing 'id'")
	}

	node.Type = getString(raw, "type")
	if node.Type == "" {
		return nil, fmt.Errorf("node %q missing 'type'", node.ID)
	}

	if deps := getStringSlice(raw, "depends_on", "dependencies"); deps != nil {
		node.DependsOn = deps
	}

	node.When = getString(raw, "when")
	node.Instruction = getString(raw, "instruction")
	node.Input = getMap(raw, "input")

	if v := getString(raw, "timeout"); v != "" {
		d, err := timeutil.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("node %q: invalid timeout %q: %w", node.ID, v, err)
		}
		node.Timeout = d
	} else {
		node.Timeout = defaultTimeout
	}

	if retryRaw := getMap(raw, "retry_strategy", "retryStrategy"); retryRaw != nil {
		rs, err := parseRetryStrategy(retryRaw)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.ID, err)
		}
		node.Retry = rs
	}

	node.InputSchemaOverride = getMap(raw, "input_schema_override")
	node.OutputSchemaOverride = getMap(raw, "output_schema_override")

	if err := parseNodeTypeFields(node, raw); err != nil {
		return nil, err
	}

	return node, nil
}

func parseNodeTypeFields(node *NodeConfig, raw map[string]any) error {
	switch node.Type {
	case "agent":
		node.AgentName = getString(raw, "agent_name")
		if node.AgentName == "" {
			return fmt.Errorf("agent node %q missing 'agent_name'", node.ID)
		}
		node.AgentInstanceName = agentKindInstanceNameHint(raw)
	case "workflow":
		node.WorkflowName = getString(raw, "workflow_name")
		if node.WorkflowName == "" {
			return fmt.Errorf("workflow node %q missing 'workflow_name'", node.ID)
		}
	case "tool":
		node.ToolName = getStringAlt(raw, "tool_name", "toolName")
		node.Connector = getString(raw, "connector")
		node.Tool = getString(raw, "tool")
		if node.ToolName == "" {
			if node.Connector != "" {
				return fmt.Errorf("tool node %q: 'connector' (%q) is a platform reference resolved to a concrete 'tool_name' at deploy time; deploy through the platform or set 'tool_name' directly", node.ID, node.Connector)
			}
			return fmt.Errorf("tool node %q missing 'tool_name'", node.ID)
		}
	case "switch":
		return parseSwitchNode(node, raw)
	case "loop":
		return parseLoopNode(node, raw)
	case "map":
		return parseMapNode(node, raw)
	case "decision":
		return parseDecisionNode(node, raw)
	case NodeTypeTimer:
		return parseTimerNode(node, raw)
	case NodeTypeWait:
		return parseWaitNode(node, raw)
	default:
		return fmt.Errorf("node %q: unknown type %q", node.ID, node.Type)
	}
	return nil
}

func agentKindInstanceNameHint(raw map[string]any) string {
	hint := getString(raw, "agent_instance_name")
	if hint == "" {
		return ""
	}
	if _, kind, ok := a2a.IDFromInstanceName(hint); ok && kind == a2a.KindAgent {
		return hint
	}
	return ""
}

func parseSwitchNode(node *NodeConfig, raw map[string]any) error {
	casesRaw, ok := raw["cases"].([]any)
	if !ok || len(casesRaw) == 0 {
		return fmt.Errorf("switch node %q requires at least one case", node.ID)
	}
	for i, caseRaw := range casesRaw {
		caseMap, ok := caseRaw.(map[string]any)
		if !ok {
			return fmt.Errorf("switch node %q: case[%d] is not a map", node.ID, i)
		}
		sc := SwitchCase{
			Condition: getStringAlt(caseMap, "condition", "when"),
			Node:      getStringAlt(caseMap, "node", "then"),
		}
		if sc.Condition == "" {
			return fmt.Errorf("switch node %q: case[%d] missing 'condition'", node.ID, i)
		}
		if sc.Node == "" {
			return fmt.Errorf("switch node %q: case[%d] missing 'node'", node.ID, i)
		}
		node.Cases = append(node.Cases, sc)
	}
	node.DefaultCase = getString(raw, "default")
	return nil
}

func parseLoopNode(node *NodeConfig, raw map[string]any) error {
	node.BodyNode = getString(raw, "node")
	if node.BodyNode == "" {
		return fmt.Errorf("loop node %q missing 'node'", node.ID)
	}
	node.Condition = getString(raw, "condition")
	if node.Condition == "" {
		return fmt.Errorf("loop node %q missing 'condition'", node.ID)
	}
	node.MaxIterations = 100
	if v := getAny(raw, "max_iterations", "maxIterations"); v != nil {
		node.MaxIterations = toInt(v)
	}
	if v := getString(raw, "delay"); v != "" {
		d, err := timeutil.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("loop node %q: invalid delay %q: %w", node.ID, v, err)
		}
		node.Delay = d
	}
	return nil
}

func parseMapNode(node *NodeConfig, raw map[string]any) error {
	node.TemplateNode = getString(raw, "node")
	if node.TemplateNode == "" {
		return fmt.Errorf("map node %q missing 'node'", node.ID)
	}

	items := getAny(raw, "items")
	withParam := getAny(raw, "with_param", "withParam")
	withItems := getAny(raw, "with_items", "withItems")

	count := 0
	if items != nil {
		count++
	}
	if withParam != nil {
		count++
	}
	if withItems != nil {
		count++
	}
	if count == 0 {
		return fmt.Errorf("map node %q requires one of: 'items', 'withParam', or 'withItems'", node.ID)
	}
	if count > 1 {
		return fmt.Errorf("map node %q accepts only one of: 'items', 'withParam', or 'withItems'", node.ID)
	}

	switch {
	case items != nil:
		node.ItemsExpression = items
	case withParam != nil:
		node.ItemsExpression = withParam
	default:
		node.ItemsExpression = withItems
	}

	node.MaxItems = 100
	if v := getAny(raw, "max_items", "maxItems"); v != nil {
		node.MaxItems = toInt(v)
	}
	if v := getAny(raw, "concurrency_limit", "concurrencyLimit"); v != nil {
		node.ConcurrencyLimit = toInt(v)
	}
	return nil
}

func parseRetryStrategy(raw map[string]any) (*RetryStrategy, error) {
	rs := &RetryStrategy{
		Limit:       3,
		RetryPolicy: "OnFailure",
	}

	if v := getAny(raw, "limit"); v != nil {
		rs.Limit = toInt(v)
	}
	if v := getStringAlt(raw, "retry_policy", "retryPolicy"); v != "" {
		rs.RetryPolicy = v
	}

	if backoffRaw := getMap(raw, "backoff"); backoffRaw != nil {
		bs := &BackoffStrategy{
			Duration: 1 * time.Second,
			Factor:   2.0,
		}
		if v := getString(backoffRaw, "duration"); v != "" {
			d, err := timeutil.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("invalid backoff duration %q: %w", v, err)
			}
			bs.Duration = d
		}
		if v := getAny(backoffRaw, "factor"); v != nil {
			bs.Factor = toFloat(v)
		}
		if v := getStringAlt(backoffRaw, "max_duration", "maxDuration"); v != "" {
			d, err := timeutil.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("invalid backoff max_duration %q: %w", v, err)
			}
			bs.MaxDuration = d
		}
		rs.Backoff = bs
	}

	return rs, nil
}

func parseExitHandler(raw any) (*ExitHandler, error) {
	switch v := raw.(type) {
	case string:
		return &ExitHandler{Always: v}, nil
	case map[string]any:
		return &ExitHandler{
			Always:    getString(v, "always"),
			OnSuccess: getStringAlt(v, "on_success", "onSuccess"),
			OnFailure: getStringAlt(v, "on_failure", "onFailure"),
			OnCancel:  getStringAlt(v, "on_cancel", "onCancel"),
		}, nil
	default:
		return nil, fmt.Errorf("on_exit must be a string or map, got %T", raw)
	}
}

func exitHandlerNodeIDs(eh *ExitHandler) []string {
	if eh == nil {
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
	for _, id := range []string{eh.Always, eh.OnSuccess, eh.OnFailure, eh.OnCancel} {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func BuildDependencyGraph(nodes []*NodeConfig) (*DependencyGraph, error) {
	g := &DependencyGraph{
		Nodes:      make(map[string]*NodeConfig, len(nodes)),
		Forward:    make(map[string][]string),
		Reverse:    make(map[string][]string),
		InnerNodes: make(map[string]bool),
	}

	for _, n := range nodes {
		if _, dup := g.Nodes[n.ID]; dup {
			return nil, fmt.Errorf("duplicate node ID %q", n.ID)
		}
		g.Nodes[n.ID] = n
	}
	addEscalationRerouteWaits(g.Nodes)

	for _, n := range nodes {
		if err := g.addDependencyEdges(n); err != nil {
			return nil, err
		}

		for _, target := range n.branchTargets() {
			if err := validateBranchDependency(g.Nodes, n.ID, target.nodeID, target.field); err != nil {
				return nil, err
			}
		}
		if err := validateEscalationReroute(g.Nodes, n); err != nil {
			return nil, err
		}

		if err := g.registerIterationBody(n); err != nil {
			return nil, err
		}
	}

	order, err := topologicalSort(nodes, g.Reverse)
	if err != nil {
		return nil, err
	}
	g.Order = order

	return g, nil
}

func (g *DependencyGraph) addDependencyEdges(n *NodeConfig) error {
	for _, dep := range n.waitsFor() {
		if _, ok := g.Nodes[dep]; !ok {
			return &missingDependencyError{node: n.ID, dependency: dep}
		}
		g.Forward[dep] = append(g.Forward[dep], n.ID)
		g.Reverse[n.ID] = append(g.Reverse[n.ID], dep)
	}
	return nil
}

type missingDependencyError struct{ node, dependency string }

func (e *missingDependencyError) Error() string {
	return fmt.Sprintf("node %q depends on non-existent node %q", e.node, e.dependency)
}

func (g *DependencyGraph) registerIterationBody(n *NodeConfig) error {
	var role, bodyID string
	switch n.Type {
	case "loop":
		role, bodyID = "body", n.BodyNode
	case "map":
		role, bodyID = "template", n.TemplateNode
	default:
		return nil
	}
	body, ok := g.Nodes[bodyID]
	if !ok {
		return fmt.Errorf("%s node %q references non-existent node %q", n.Type, n.ID, bodyID)
	}
	if err := validateIterationBody(n.ID, n.Type, role, body); err != nil {
		return err
	}
	g.InnerNodes[bodyID] = true
	return nil
}

func validateIterationBody(parentID, parentType, role string, body *NodeConfig) error {
	switch body.Type {
	case NodeTypeTimer, NodeTypeWait:
		return fmt.Errorf("%s node %q %s %q is a %s node; a %s %s cannot wait, so place the %s node before or after the %s",
			parentType, parentID, role, body.ID, body.Type, parentType, role, body.Type, parentType)
	case "agent":
		if strings.TrimSpace(body.AgentName) == "" {
			return fmt.Errorf("%s node %q %s %q is an agent node with no `agent` name", parentType, parentID, role, body.ID)
		}
	case "workflow":
		if strings.TrimSpace(body.WorkflowName) == "" {
			return fmt.Errorf("%s node %q %s %q is a workflow node with no `workflow_name`", parentType, parentID, role, body.ID)
		}
	case "tool":
		if strings.TrimSpace(body.ToolName) == "" {
			return fmt.Errorf("%s node %q %s %q is a tool node with no `tool` name", parentType, parentID, role, body.ID)
		}
	case "decision":
		if body.RouteOn != "" {
			return fmt.Errorf("%s node %q %s %q is a decision node with route_on; a %s %s runs once per item and cannot select branches, so drop the routing and branch on its answers after the %s",
				parentType, parentID, role, body.ID, parentType, role, parentType)
		}
	}
	return nil
}

func validateBranchDependency(nodes map[string]*NodeConfig, parentID, targetID, branchName string) error {
	target, ok := nodes[targetID]
	if !ok {
		return fmt.Errorf("node %q references non-existent %s %q", parentID, branchName, targetID)
	}

	if slices.Contains(target.DependsOn, parentID) {
		return nil
	}

	return fmt.Errorf(
		"logic error: node %q routes to %q (%s), but %q does not list %q in depends_on; "+
			"fix: add 'depends_on: [%s]' to node %q",
		parentID, targetID, branchName, targetID, parentID, parentID, targetID,
	)
}

func ValidateExitHandler(eh *ExitHandler, g *DependencyGraph) error {
	if eh == nil {
		return nil
	}
	refs := []struct {
		field, nodeID string
	}{
		{"always", eh.Always},
		{"on_success", eh.OnSuccess},
		{"on_failure", eh.OnFailure},
		{"on_cancel", eh.OnCancel},
	}
	for _, r := range refs {
		if r.nodeID == "" {
			continue
		}
		node, ok := g.Nodes[r.nodeID]
		if !ok {
			return fmt.Errorf("onExit.%s references non-existent node %q", r.field, r.nodeID)
		}
		if node.Type == "decision" {
			return fmt.Errorf("onExit.%s node %q is a decision node, which the exit-handler chain cannot run; on_exit supports agent, workflow and tool nodes", r.field, r.nodeID)
		}
	}
	return nil
}

func CheckUnreachableNodes(g *DependencyGraph, exitHandler *ExitHandler) error {
	exempt := make(map[string]bool)
	if exitHandler != nil {
		for _, id := range []string{exitHandler.Always, exitHandler.OnSuccess, exitHandler.OnFailure, exitHandler.OnCancel} {
			if id != "" {
				exempt[id] = true
			}
		}
	}

	reachable := make(map[string]bool)
	var queue []string
	for id, node := range g.Nodes {
		if len(node.waitsFor()) == 0 && !g.InnerNodes[id] {
			queue = append(queue, id)
		}
	}

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		for _, next := range g.Forward[id] {
			if !reachable[next] {
				queue = append(queue, next)
			}
		}
	}

	var unreachable []string
	for id := range g.Nodes {
		if !reachable[id] && !g.InnerNodes[id] && !exempt[id] {
			unreachable = append(unreachable, id)
		}
	}

	if len(unreachable) > 0 {
		return fmt.Errorf("unreachable nodes detected: %v", unreachable)
	}
	return nil
}

func topologicalSort(nodes []*NodeConfig, reverse map[string][]string) ([]string, error) {
	inDegree := make(map[string]int, len(nodes))
	for _, n := range nodes {
		inDegree[n.ID] = len(reverse[n.ID])
	}

	var queue []string
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	forward := make(map[string][]string)
	for _, n := range nodes {
		for _, dep := range n.waitsFor() {
			forward[dep] = append(forward[dep], n.ID)
		}
	}

	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)

		for _, next := range forward[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) != len(nodes) {
		var cycleNodes []string
		for _, n := range nodes {
			if inDegree[n.ID] > 0 {
				cycleNodes = append(cycleNodes, n.ID)
			}
		}
		return nil, fmt.Errorf("dependency cycle detected among nodes: %v", cycleNodes)
	}

	return order, nil
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprint(v)
	}
	return s
}

func getStringAlt(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := getString(m, k); v != "" {
			return v
		}
	}
	return ""
}

func getStringSlice(m map[string]any, keys ...string) []string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch s := v.(type) {
		case []any:
			result := make([]string, len(s))
			for i, item := range s {
				result[i] = fmt.Sprint(item)
			}
			return result
		case []string:
			return s
		}
	}
	return nil
}

func getMap(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			return sub
		}
	}
	return nil
}

func getMapSlice(m map[string]any, key string) []map[string]any {
	v, ok := m[key]
	if !ok {
		return nil
	}
	slice, ok := v.([]any)
	if !ok {
		return nil
	}
	var result []map[string]any
	for _, item := range slice {
		if sub, ok := item.(map[string]any); ok {
			result = append(result, sub)
		}
	}
	return result
}

func getAny(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
