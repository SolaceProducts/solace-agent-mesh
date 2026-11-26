// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type Config struct {
	BrokerConfig   DataPlaneBrokerConfig
	AckPolicy      AckPolicy
	EventHandlers  []*EventHandlerConfig
	OutputHandlers map[string]*OutputHandlerConfig
}

type DataPlaneBrokerConfig struct {
	BrokerURL     string
	VPN           string
	Username      string
	Password      string
	TLSSkipVerify bool
}

type AckPolicy struct {
	Mode           string
	OnFailure      AckFailurePolicy
	TimeoutSeconds int
}

type AckFailurePolicy struct {
	Action      string
	NackOutcome string
}

type EventHandlerConfig struct {
	Name          string
	Subscriptions []SubscriptionConfig

	ExistingQueueName string

	InputExpression string

	EventFilter *sacexpr.Predicate

	TargetAgentName           string
	TargetAgentNameExpression string
	TargetWorkflowName        string
	TargetWorkflowNameExpr    string

	OnSuccess string
	OnError   string

	UserIdentityExpression string
	DefaultUserIdentity    string
	RunAs                  string

	PayloadFormat   string
	PayloadEncoding string

	ForwardContext map[string]string

	ArtifactProcessing *ArtifactProcessingConfig

	AckPolicy *AckPolicy

	StructuredInvocation *StructuredInvocationConfig
}

type SubscriptionConfig struct {
	Topic string `yaml:"topic"`
	QoS   int    `yaml:"qos"`
}

type ArtifactProcessingConfig struct {
	ExtractExpression string
	Definition        ArtifactDefinition
}

type ArtifactDefinition struct {
	Filename        string
	Content         string
	MimeType        string
	ContentEncoding string
}

type StructuredInvocationConfig struct {
	InputSchema  map[string]any
	OutputSchema map[string]any
}

func (si *StructuredInvocationConfig) hasSchema() bool {
	return si != nil && (si.InputSchema != nil || si.OutputSchema != nil)
}

func (h *EventHandlerConfig) SendsStructuredRequest() bool {
	return !targetsWorkflow(h) && h.StructuredInvocation.hasSchema()
}

type OutputHandlerConfig struct {
	Name                      string
	TopicExpression           string
	PayloadExpression         string
	PayloadFormat             string
	PayloadEncoding           string
	MaxFileSizeForBase64Bytes int
	OutputTransforms          []sacexpr.TransformConfig
	OutputSchema              map[string]any
	OnValidationError         string
	DeliveryMode              string
	Headers                   []HeaderSetting
	UserProperties            []UserPropertySetting
}

func ParseConfig(appConfig map[string]any, skipBrokerValidation bool) (*Config, error) {
	cfg := &Config{
		AckPolicy: AckPolicy{
			Mode:           "on_receive",
			OnFailure:      AckFailurePolicy{Action: "nack", NackOutcome: "rejected"},
			TimeoutSeconds: 300,
		},
		OutputHandlers: make(map[string]*OutputHandlerConfig),
	}

	brokerMap, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if brokerMap == nil && !skipBrokerValidation {
		return nil, fmt.Errorf("event_mesh_broker_config is required")
	}
	if brokerMap != nil {
		cfg.BrokerConfig = DataPlaneBrokerConfig{
			BrokerURL:     getString(brokerMap, "broker_url"),
			VPN:           getString(brokerMap, "broker_vpn"),
			Username:      getString(brokerMap, "broker_username"),
			Password:      getString(brokerMap, "broker_password"),
			TLSSkipVerify: getLenientBool(brokerMap, "tls_skip_verify"),
		}
		if cfg.BrokerConfig.BrokerURL == "" && !skipBrokerValidation && !samapp.IsDevMode(brokerMap) {
			return nil, fmt.Errorf("event_mesh_broker_config.broker_url is required")
		}
	}

	if ackMap, ok := appConfig["acknowledgment_policy"].(map[string]any); ok {
		parseAckPolicy(ackMap, &cfg.AckPolicy)
	}

	handlersRaw, _ := appConfig["event_handlers"].([]any)
	for i, raw := range handlersRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("event_handlers[%d]: expected map", i)
		}
		handler, err := parseEventHandler(m)
		if err != nil {
			return nil, fmt.Errorf("event_handlers[%d]: %w", i, err)
		}
		cfg.EventHandlers = append(cfg.EventHandlers, handler)
	}

	outputsRaw, _ := appConfig["output_handlers"].([]any)
	for i, raw := range outputsRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("output_handlers[%d]: expected map", i)
		}
		handler, err := parseOutputHandler(m)
		if err != nil {
			return nil, fmt.Errorf("output_handlers[%d]: %w", i, err)
		}
		cfg.OutputHandlers[handler.Name] = handler
	}

	for _, eh := range cfg.EventHandlers {
		if eh.OnSuccess != "" {
			if _, ok := cfg.OutputHandlers[eh.OnSuccess]; !ok {
				return nil, fmt.Errorf("event handler %q references unknown output handler %q (on_success)", eh.Name, eh.OnSuccess)
			}
		}
		if eh.OnError != "" {
			if _, ok := cfg.OutputHandlers[eh.OnError]; !ok {
				return nil, fmt.Errorf("event handler %q references unknown output handler %q (on_error)", eh.Name, eh.OnError)
			}
		}
	}

	return cfg, nil
}

func parseEventHandler(m map[string]any) (*EventHandlerConfig, error) {
	h := &EventHandlerConfig{
		Name:                      getString(m, "name"),
		ExistingQueueName:         getString(m, "existing_queue_name"),
		InputExpression:           getString(m, "input_expression"),
		TargetAgentName:           getString(m, "target_agent_name"),
		TargetAgentNameExpression: getString(m, "target_agent_name_expression"),
		TargetWorkflowName:        getString(m, "target_workflow_name"),
		TargetWorkflowNameExpr:    getString(m, "target_workflow_name_expression"),
		OnSuccess:                 getString(m, "on_success"),
		OnError:                   getString(m, "on_error"),
		UserIdentityExpression:    getString(m, "user_identity_expression"),
		DefaultUserIdentity:       getString(m, "default_user_identity"),
		RunAs:                     getString(m, "run_as"),
		PayloadFormat:             getString(m, "payload_format"),
		PayloadEncoding:           getString(m, "payload_encoding"),
	}

	if h.Name == "" {
		return nil, fmt.Errorf("event handler 'name' is required")
	}

	subsRaw, _ := m["subscriptions"].([]any)
	for _, s := range subsRaw {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		h.Subscriptions = append(h.Subscriptions, SubscriptionConfig{
			Topic: getString(sm, "topic"),
			QoS:   getInt(sm, "qos"),
		})
	}

	switch {
	case h.ExistingQueueName != "" && len(h.Subscriptions) > 0:
		return nil, fmt.Errorf("event handler %q: existing_queue_name and subscriptions are mutually exclusive", h.Name)
	case h.ExistingQueueName == "" && len(h.Subscriptions) == 0:
		return nil, fmt.Errorf("event handler %q: subscriptions is required (or set existing_queue_name)", h.Name)
	}

	if raw := m[eventFilterKey]; raw != nil {
		filter, err := sacexpr.CompilePredicate(eventFilterKey, raw)
		if err != nil {
			return nil, fmt.Errorf("event handler %q: %w", h.Name, err)
		}
		h.EventFilter = filter
	}

	if fc, ok := m["forward_context"].(map[string]any); ok {
		h.ForwardContext = make(map[string]string, len(fc))
		for k, v := range fc {
			if s, ok := v.(string); ok {
				h.ForwardContext[k] = s
			}
		}
	}

	if ap, ok := m["artifact_processing"].(map[string]any); ok {
		h.ArtifactProcessing = &ArtifactProcessingConfig{
			ExtractExpression: getString(ap, "extract_artifacts_expression"),
		}
		if def, ok := ap["artifact_definition"].(map[string]any); ok {
			enc, err := normalizeContentEncoding(getString(def, "content_encoding"))
			if err != nil {
				return nil, fmt.Errorf("artifact_definition: content_encoding: %w", err)
			}
			h.ArtifactProcessing.Definition = ArtifactDefinition{
				Filename:        getString(def, "filename"),
				Content:         getString(def, "content"),
				MimeType:        getString(def, "mime_type"),
				ContentEncoding: enc,
			}
		}
	}

	if ackMap, ok := m["acknowledgment_policy"].(map[string]any); ok {
		h.AckPolicy = &AckPolicy{
			Mode:           "on_receive",
			OnFailure:      AckFailurePolicy{Action: "nack", NackOutcome: "rejected"},
			TimeoutSeconds: 300,
		}
		parseAckPolicy(ackMap, h.AckPolicy)
	}

	if si, ok := m["structured_invocation"].(map[string]any); ok {
		h.StructuredInvocation = &StructuredInvocationConfig{
			InputSchema:  getMap(si, "input_schema"),
			OutputSchema: getMap(si, "output_schema"),
		}
	}

	return h, nil
}

const eventFilterKey = "event_filter"

func parseOutputHandler(m map[string]any) (*OutputHandlerConfig, error) {
	h := &OutputHandlerConfig{
		Name:                      getString(m, "name"),
		TopicExpression:           getString(m, "topic_expression"),
		PayloadExpression:         getString(m, "payload_expression"),
		PayloadFormat:             getString(m, "payload_format"),
		PayloadEncoding:           getString(m, "payload_encoding"),
		MaxFileSizeForBase64Bytes: getInt(m, "max_file_size_for_base64_bytes"),
		OnValidationError:         getString(m, "on_validation_error"),
	}

	if h.Name == "" {
		return nil, fmt.Errorf("output handler 'name' is required")
	}
	if err := parseOutputDelivery(m, h); err != nil {
		return nil, fmt.Errorf("output handler %q: %w", h.Name, err)
	}

	h.OutputSchema = getMap(m, "output_schema")

	if transforms, ok := m["output_transforms"].([]any); ok {
		for _, t := range transforms {
			if tm, ok := t.(map[string]any); ok {
				h.OutputTransforms = append(h.OutputTransforms, parseTransformConfig(tm))
			}
		}
	}

	return h, nil
}

func parseAckPolicy(m map[string]any, policy *AckPolicy) {
	if mode := getString(m, "mode"); mode != "" {
		policy.Mode = mode
	}
	if ts := getInt(m, "timeout_seconds"); ts > 0 {
		policy.TimeoutSeconds = ts
	}
	if onFail, ok := m["on_failure"].(map[string]any); ok {
		if action := getString(onFail, "action"); action != "" {
			policy.OnFailure.Action = action
		}
		if outcome := getString(onFail, "nack_outcome"); outcome != "" {
			policy.OnFailure.NackOutcome = outcome
		}
	}
}

func parseTransformConfig(m map[string]any) sacexpr.TransformConfig {
	tc := sacexpr.TransformConfig{
		Type:                 getString(m, "type"),
		SourceExpression:     getString(m, "source_expression"),
		DestExpression:       getString(m, "dest_expression"),
		SourceListExpression: getString(m, "source_list_expression"),
		DestListExpression:   getString(m, "dest_list_expression"),
		SourceProperty:       getString(m, "source_property"),
		DestProperty:         getString(m, "dest_property"),
		AllowMissingSource:   getBool(m, "allow_missing_source"),
	}
	tc.SourceValue = m["source_value"]
	tc.InitialValue = m["initial_value"]

	if pf, ok := m["processing_function"].(map[string]any); ok {
		tc.ProcessingFunction = parseInvokeConfig(pf)
	}
	if af, ok := m["accumulator_function"].(map[string]any); ok {
		tc.AccumulatorFunction = parseInvokeConfig(af)
	}
	if ff, ok := m["filter_function"].(map[string]any); ok {
		tc.FilterFunction = parseInvokeConfig(ff)
	}

	return tc
}

func parseInvokeConfig(m map[string]any) *sacexpr.InvokeConfig {
	ic := &sacexpr.InvokeConfig{
		Module:   getString(m, "module"),
		Function: getString(m, "function"),
	}
	if params, ok := m["params"].(map[string]any); ok {
		if pos, ok := params["positional"].([]any); ok {
			ic.Params.Positional = pos
		}
		if kw, ok := params["keyword"].(map[string]any); ok {
			ic.Params.Keyword = kw
		}
	}
	return ic
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func normalizeContentEncoding(enc string) (string, error) {
	if enc == "" || strings.Contains(enc, ":") {
		return enc, nil
	}
	switch strings.ToLower(enc) {
	case "utf-8", "base64", "base64url", "base64raw", "base64rawurl":
		return "static:" + enc, nil
	default:
		return "", fmt.Errorf("content_encoding %q is unrecognised; use utf-8, base64, base64url, base64raw, or base64rawurl", enc)
	}
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return 0
}

func getBool(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func getLenientBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func getMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
