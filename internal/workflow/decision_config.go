// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
)

const (
	DecisionProviderTypeSafe       = decision.ProviderTypeSafe
	DecisionProviderOpenAI         = decision.ProviderOpenAI
	DecisionProviderGoogleAIStudio = decision.ProviderGoogleAIStudio
)

const (
	onProviderErrorFail     = "fail"
	onProviderErrorEscalate = "escalate"
)

var ErrEscalationChoiceRemoved = errors.New("escalation_choice was replaced by escalation_mode")

const (
	EscalationModeReroute = "reroute"
	EscalationModeHandoff = "handoff"
)

func ParseDecisionProvider(raw map[string]any) (*DecisionProviderConfig, error) {
	cfg := &DecisionProviderConfig{
		Provider:       getString(raw, "provider"),
		ModelAlias:     getStringAlt(raw, "model_alias", "modelAlias"),
		APIBase:        getStringAlt(raw, "api_base", "apiBase"),
		APIKey:         getStringAlt(raw, "api_key", "apiKey"),
		Model:          getString(raw, "model"),
		TimeoutSeconds: toInt(getAny(raw, "timeout_seconds", "timeoutSeconds")),
		MaxStateBytes:  toInt(getAny(raw, "max_state_bytes", "maxStateBytes")),
		MaxImages:      toInt(getAny(raw, "max_images", "maxImages")),
		MaxImageBytes:  toInt(getAny(raw, "max_image_bytes", "maxImageBytes")),
		MaxMedia:       toInt(getAny(raw, "max_media", "maxMedia")),
		MaxMediaBytes:  toInt(getAny(raw, "max_media_bytes", "maxMediaBytes")),
	}
	if cfg.Provider == "" {
		cfg.Provider = DecisionProviderTypeSafe
	}
	if supported := providers.Supported(); !slices.Contains(supported, cfg.Provider) {
		return nil, fmt.Errorf("decision: unknown provider %q (supported: %s)", cfg.Provider, strings.Join(supported, ", "))
	}
	if cfg.ModelAlias != "" && (cfg.APIBase != "" || cfg.APIKey != "" || cfg.Model != "") {
		return nil, fmt.Errorf("decision: model_alias %q names a registered decision model, which carries its own endpoint, key and model; remove api_base, api_key and model", cfg.ModelAlias)
	}
	if cfg.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("decision: timeout_seconds must not be negative, got %d", cfg.TimeoutSeconds)
	}
	if cfg.MaxStateBytes < 0 {
		return nil, fmt.Errorf("decision: max_state_bytes must not be negative, got %d", cfg.MaxStateBytes)
	}
	if cfg.MaxImages < 0 || cfg.MaxImages > decision.MaxImagesPerRequest {
		return nil, fmt.Errorf("decision: max_images must be between 0 (the default, %d) and %d, got %d",
			decision.DefaultMaxImages, decision.MaxImagesPerRequest, cfg.MaxImages)
	}
	if cfg.MaxImageBytes < 0 || cfg.MaxImageBytes > decision.MaxImageBytesLimit {
		return nil, fmt.Errorf("decision: max_image_bytes must be between 0 (the default, %d) and %d, got %d",
			decision.DefaultMaxImageBytes, decision.MaxImageBytesLimit, cfg.MaxImageBytes)
	}
	if cfg.MaxMedia < 0 || cfg.MaxMedia > decision.MaxMediaPerRequest {
		return nil, fmt.Errorf("decision: max_media must be between 0 (the default, %d) and %d, got %d",
			decision.DefaultMaxMedia, decision.MaxMediaPerRequest, cfg.MaxMedia)
	}
	if cfg.MaxMediaBytes < 0 || cfg.MaxMediaBytes > decision.MaxMediaBytesLimit {
		return nil, fmt.Errorf("decision: max_media_bytes must be between 0 (the default, %d) and %d, got %d",
			decision.DefaultMaxMediaBytes, decision.MaxMediaBytesLimit, cfg.MaxMediaBytes)
	}
	return cfg, nil
}

func parseDecisionNode(node *NodeConfig, raw map[string]any) error {
	if node.Retry != nil {
		return fmt.Errorf("decision node %q: retry_strategy is not supported; the provider already retries rate-limit and overload responses", node.ID)
	}
	questionsRaw, _ := coerceYAMLScalarKeysToStrings(getAny(raw, "questions")).(map[string]any)
	questions, err := parseDecisionQuestions(node.ID, questionsRaw)
	if err != nil {
		return err
	}
	node.Questions = questions
	node.RouteOn = getStringAlt(raw, "route_on", "routeOn")
	if getAny(raw, "escalation_choice", "escalationChoice") != nil {
		return fmt.Errorf("decision node %q: %w; remove it, since the default escalation_mode: reroute reads the escalate target's answer from its route_on field, and set escalation_mode: handoff if the escalate target should end the routing instead", node.ID, ErrEscalationChoiceRemoved)
	}
	node.Escalate = getString(raw, "escalate")
	node.EscalationMode = getStringAlt(raw, "escalation_mode", "escalationMode")
	node.Default = getString(raw, "default")
	node.OnProviderError = getStringAlt(raw, "on_provider_error", "onProviderError")
	node.Model = getString(raw, "model")
	if err := parseDecisionMedia(node, raw); err != nil {
		return err
	}
	if v := getAny(raw, "min_confidence", "minConfidence"); v != nil {
		node.MinConfidence = toFloat(v)
		if node.MinConfidence <= 0 || node.MinConfidence > 1 {
			return fmt.Errorf("decision node %q: min_confidence must be in (0, 1], got %v", node.ID, v)
		}
	}
	if routesRaw, _ := coerceYAMLScalarKeysToStrings(getAny(raw, "routes")).(map[string]any); routesRaw != nil {
		node.Routes = make(map[string]string, len(routesRaw))
		for option, target := range routesRaw {
			targetID, ok := target.(string)
			if !ok || targetID == "" {
				return fmt.Errorf("decision node %q: routes.%s must name a target node", node.ID, option)
			}
			node.Routes[option] = targetID
		}
	}
	return validateDecisionRouting(node)
}

func parseDecisionQuestions(nodeID string, raw map[string]any) ([]decision.Question, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("decision node %q requires at least one entry under 'questions'", nodeID)
	}
	questions := make([]decision.Question, 0, len(raw))
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		qMap, ok := raw[key].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("decision node %q: questions.%s is not a map", nodeID, key)
		}
		q, err := parseDecisionQuestion(key, qMap)
		if err != nil {
			return nil, fmt.Errorf("decision node %q: questions.%s: %w", nodeID, key, err)
		}
		questions = append(questions, q)
	}
	return questions, nil
}

func parseDecisionQuestion(key string, raw map[string]any) (decision.Question, error) {
	q := decision.Question{
		Key:          key,
		Type:         decision.QuestionType(getString(raw, "type")),
		Instructions: strings.TrimSpace(getString(raw, "instructions")),
	}
	if q.Instructions == "" {
		return q, errors.New("'instructions' is required")
	}
	criteria, err := parseCriteria(q.Type, getAny(raw, "criteria"))
	if err != nil {
		return q, err
	}
	q.Criteria = criteria
	if err := q.Validate(); err != nil {
		return q, err
	}
	return q, nil
}

func parseCriteria(qType decision.QuestionType, raw any) ([]decision.Criterion, error) {
	switch v := coerceYAMLScalarKeysToStrings(raw).(type) {
	case nil:
		return nil, nil
	case map[string]any:
		if qType == decision.TypeScore {
			return nil, errors.New("score 'criteria' must be a list, lowest level first (e.g. `- low: ...`), because a map does not keep its order")
		}
		criteria := make([]decision.Criterion, 0, len(v))
		for _, key := range slices.Sorted(maps.Keys(v)) {
			criteria = append(criteria, decision.Criterion{Key: key, Description: getString(v, key)})
		}
		return criteria, nil
	case []any:
		criteria := make([]decision.Criterion, 0, len(v))
		seen := make(map[string]bool, len(v))
		for i, item := range v {
			entry, ok := coerceYAMLScalarKeysToStrings(item).(map[string]any)
			if !ok || len(entry) != 1 {
				return nil, fmt.Errorf("criteria[%d] must be a single `option: description` entry", i)
			}
			for key := range entry {
				if seen[key] {
					return nil, fmt.Errorf("criteria[%d] repeats option %q", i, key)
				}
				seen[key] = true
				criteria = append(criteria, decision.Criterion{Key: key, Description: getString(entry, key)})
			}
		}
		return criteria, nil
	default:
		return nil, fmt.Errorf("'criteria' must be a map or a list, got %T", raw)
	}
}

func coerceYAMLScalarKeysToStrings(raw any) any {
	m, ok := raw.(map[any]any)
	if !ok {
		return raw
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[fmt.Sprint(k)] = v
	}
	return out
}

func validateDecisionRouting(node *NodeConfig) error {
	if err := validateDecisionEnums(node); err != nil {
		return err
	}

	routing := node.RouteOn != "" || len(node.Routes) > 0 || node.Default != "" || node.MinConfidence > 0 ||
		node.Escalate != "" || node.OnProviderError == onProviderErrorEscalate || node.EscalationMode != ""
	if !routing {
		return nil
	}
	routeOn, err := routeOnChoiceQuestion(node)
	if err != nil {
		return err
	}
	if len(node.Routes) == 0 {
		return fmt.Errorf("decision node %q has route_on but no routes", node.ID)
	}
	for _, option := range slices.Sorted(maps.Keys(node.Routes)) {
		if !routeOn.HasCriterion(option) {
			return fmt.Errorf("decision node %q: routes.%s is not an option of question %q (options: %s)",
				node.ID, option, node.RouteOn, strings.Join(routeOn.CriterionKeys(), ", "))
		}
	}
	needsEscalate := node.MinConfidence > 0 || node.OnProviderError == onProviderErrorEscalate
	if needsEscalate && node.Escalate == "" {
		return fmt.Errorf("decision node %q sets min_confidence or on_provider_error: escalate but names no escalate target", node.ID)
	}
	if node.Escalate != "" && !needsEscalate {
		return fmt.Errorf("decision node %q names escalate %q, which nothing reaches; set min_confidence or on_provider_error: escalate", node.ID, node.Escalate)
	}
	if unrouted := unroutedOptions(routeOn, node.Routes); node.Default == "" && len(unrouted) > 0 {
		return fmt.Errorf("decision node %q: question %q has no route for option(s) %s and the node has no default; route every option or set default",
			node.ID, node.RouteOn, strings.Join(unrouted, ", "))
	}
	return validateEscalationMode(node)
}

func routeOnChoiceQuestion(node *NodeConfig) (decision.Question, error) {
	if node.RouteOn == "" {
		return decision.Question{}, fmt.Errorf("decision node %q routes (routes, default, escalate or min_confidence is set) but has no route_on naming the choice question to route on", node.ID)
	}
	routeOn, ok := node.decisionQuestion(node.RouteOn)
	if !ok {
		return decision.Question{}, fmt.Errorf("decision node %q: route_on %q is not one of its questions", node.ID, node.RouteOn)
	}
	if routeOn.Type != decision.TypeChoice {
		return decision.Question{}, fmt.Errorf("decision node %q: route_on %q is a %s question; only a choice question can select a branch", node.ID, node.RouteOn, routeOn.Type)
	}
	return routeOn, nil
}

func validateDecisionEnums(node *NodeConfig) error {
	switch node.OnProviderError {
	case "", onProviderErrorFail, onProviderErrorEscalate:
	default:
		return fmt.Errorf("decision node %q: on_provider_error must be %q or %q, got %q",
			node.ID, onProviderErrorFail, onProviderErrorEscalate, node.OnProviderError)
	}
	switch node.EscalationMode {
	case "", EscalationModeReroute, EscalationModeHandoff:
	default:
		return fmt.Errorf("decision node %q: escalation_mode must be %q or %q, got %q",
			node.ID, EscalationModeReroute, EscalationModeHandoff, node.EscalationMode)
	}
	return nil
}

func validateEscalationMode(node *NodeConfig) error {
	if node.EscalationMode != "" && node.Escalate == "" {
		return fmt.Errorf("decision node %q sets escalation_mode but names no escalate target", node.ID)
	}
	if !node.reroutesEscalation() {
		return nil
	}
	for _, target := range node.routedTargets() {
		if target.nodeID == node.Escalate {
			return fmt.Errorf("decision node %q: %s is the escalate target %q, whose answer reroutes the escalation; route to another node, or set escalation_mode: handoff so an escalation ends at %q",
				node.ID, target.field, node.Escalate, node.Escalate)
		}
	}
	return nil
}

func (n *NodeConfig) reroutesEscalation() bool {
	return n.Type == "decision" && n.Escalate != "" && n.EscalationMode != EscalationModeHandoff
}

func (n *NodeConfig) resolvedEscalationMode() string {
	if n.Escalate == "" {
		return ""
	}
	if n.reroutesEscalation() {
		return EscalationModeReroute
	}
	return EscalationModeHandoff
}

func EscalationAnswerTemplate(n *NodeConfig, escalateNode *NodeConfig) string {
	if escalateNode != nil && escalateNode.Type == "decision" {
		return fmt.Sprintf("{{%s.output.answers.%s.choice}}", n.Escalate, n.RouteOn)
	}
	return fmt.Sprintf("{{%s.output.%s}}", n.Escalate, n.RouteOn)
}

func validateDecisionEscalateTarget(n, escalateNode *NodeConfig, handoffHint string) error {
	q, asked := escalateNode.decisionQuestion(n.RouteOn)
	if !asked || q.Type != decision.TypeChoice {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so its escalate decision node %q must ask a choice question named %q; %s",
			n.ID, n.Escalate, n.RouteOn, handoffHint)
	}
	routeOn, _ := n.decisionQuestion(n.RouteOn)
	for _, option := range q.CriterionKeys() {
		if !routeOn.HasCriterion(option) {
			return fmt.Errorf("decision node %q reroutes on its escalation's answer, but its escalate decision node %q can answer %q, which is not an option of %q (options: %s); %s",
				n.ID, n.Escalate, option, n.RouteOn, strings.Join(routeOn.CriterionKeys(), ", "), handoffHint)
		}
	}
	if escalateNode.Escalate != "" {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so its escalate decision node %q must not escalate itself (it names escalate %q); %s",
			n.ID, n.Escalate, escalateNode.Escalate, handoffHint)
	}
	return nil
}

func rerouteWaitClosesCycle(nodes map[string]*NodeConfig, n *NodeConfig) (string, bool) {
	routes := make(map[string]bool)
	for _, target := range n.routedTargets() {
		routes[target.nodeID] = true
	}
	seen := map[string]bool{}
	stack := []string{n.Escalate}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node, ok := nodes[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		for _, dep := range node.waitsFor() {
			if routes[dep] {
				return dep, true
			}
			stack = append(stack, dep)
		}
	}
	return "", false
}

var escalationTargetTypes = []string{"agent", "workflow", "tool", "decision"}

func addEscalationRerouteWaits(nodes map[string]*NodeConfig) {
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		n := nodes[id]
		if _, exists := nodes[n.Escalate]; !exists || !n.reroutesEscalation() {
			continue
		}
		for _, target := range n.routedTargets() {
			targetNode, ok := nodes[target.nodeID]
			if !ok || slices.Contains(targetNode.waitsFor(), n.Escalate) {
				continue
			}
			targetNode.implicitWaits = append(targetNode.implicitWaits, n.Escalate)
		}
	}
}

func validateEscalationReroute(nodes map[string]*NodeConfig, n *NodeConfig) error {
	if !n.reroutesEscalation() {
		return nil
	}
	const handoffHint = "or set escalation_mode: handoff so the escalation ends there"
	if escalateNode, ok := nodes[n.Escalate]; ok {
		if err := validateRerouteTargetType(n, escalateNode, handoffHint); err != nil {
			return err
		}
	}
	if route, closes := rerouteWaitClosesCycle(nodes, n); closes {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so %q waits for its escalate target %q, which itself depends on %q: that would be a dependency cycle; %s",
			n.ID, route, n.Escalate, route, handoffHint)
	}
	if parent, role := iterationParent(nodes, n.Escalate); parent != nil {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so its escalate target %q must run in the main flow; it is the %s of %s node %q, whose completions never reach the routing; %s",
			n.ID, n.Escalate, role, parent.Type, parent.ID, handoffHint)
	}
	if other, field := otherRouterOf(nodes, n.ID, n.Escalate); other != "" {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so its escalate target %q must be its own; node %q also routes to it (%s), and could skip it before %q's escalation answers; %s",
			n.ID, n.Escalate, other, field, n.ID, handoffHint)
	}
	return nil
}

func validateRerouteTargetType(n, escalateNode *NodeConfig, handoffHint string) error {
	if !slices.Contains(escalationTargetTypes, escalateNode.Type) {
		return fmt.Errorf("decision node %q reroutes on its escalation's answer, so its escalate target %q must be one of %s, got %s; %s",
			n.ID, n.Escalate, strings.Join(escalationTargetTypes, ", "), escalateNode.Type, handoffHint)
	}
	if escalateNode.Type == "decision" {
		return validateDecisionEscalateTarget(n, escalateNode, handoffHint)
	}
	return nil
}

func otherRouterOf(nodes map[string]*NodeConfig, routerID, targetID string) (string, string) {
	for _, other := range slices.Sorted(maps.Keys(nodes)) {
		if other == routerID {
			continue
		}
		for _, target := range nodes[other].branchTargets() {
			if target.nodeID == targetID {
				return other, target.field
			}
		}
	}
	return "", ""
}

func iterationParent(nodes map[string]*NodeConfig, nodeID string) (*NodeConfig, string) {
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		switch parent := nodes[id]; {
		case parent.Type == "loop" && parent.BodyNode == nodeID:
			return parent, "body"
		case parent.Type == "map" && parent.TemplateNode == nodeID:
			return parent, "template"
		}
	}
	return nil, ""
}

func validateDecisionTargetsOutsideExitHandlers(nodes []*NodeConfig, eh *ExitHandler) error {
	if eh == nil {
		return nil
	}
	handlers := exitHandlerNodeIDs(eh)
	for _, n := range nodes {
		if n.Type != "decision" {
			continue
		}
		for _, target := range n.decisionTargets() {
			if slices.Contains(handlers, target.nodeID) {
				return fmt.Errorf("decision node %q: %s %q is an on_exit handler, which only the exit-handler chain runs; route to a node in the main flow",
					n.ID, target.field, target.nodeID)
			}
		}
	}
	return nil
}

func (n *NodeConfig) routedTargets() []branchTarget {
	return slices.DeleteFunc(n.decisionTargets(), func(t branchTarget) bool { return t.field == "escalate" })
}

func unroutedOptions(q decision.Question, routes map[string]string) []string {
	return slices.DeleteFunc(q.CriterionKeys(), func(option string) bool {
		_, routed := routes[option]
		return routed
	})
}

func (n *NodeConfig) decisionQuestion(key string) (decision.Question, bool) {
	for _, q := range n.Questions {
		if q.Key == key {
			return q, true
		}
	}
	return decision.Question{}, false
}

type branchTarget struct {
	field  string
	nodeID string
}

func (n *NodeConfig) branchTargets() []branchTarget {
	switch n.Type {
	case "switch":
		return n.switchTargets()
	case "decision":
		return n.decisionTargets()
	default:
		return nil
	}
}

func (n *NodeConfig) switchTargets() []branchTarget {
	targets := make([]branchTarget, 0, len(n.Cases)+1)
	for i, sc := range n.Cases {
		targets = append(targets, branchTarget{field: fmt.Sprintf("cases[%d].node", i), nodeID: sc.Node})
	}
	if n.DefaultCase != "" {
		targets = append(targets, branchTarget{field: "default", nodeID: n.DefaultCase})
	}
	return targets
}

func (n *NodeConfig) decisionTargets() []branchTarget {
	var targets []branchTarget
	for _, option := range slices.Sorted(maps.Keys(n.Routes)) {
		targets = append(targets, branchTarget{field: "routes." + option, nodeID: n.Routes[option]})
	}
	if n.Escalate != "" {
		targets = append(targets, branchTarget{field: "escalate", nodeID: n.Escalate})
	}
	if n.Default != "" {
		targets = append(targets, branchTarget{field: "default", nodeID: n.Default})
	}
	return targets
}
