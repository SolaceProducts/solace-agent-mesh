// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"cmp"
	"sync"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type CustomFieldType struct {
	Type        string
	ItemKind    string
	Description string
	Fields      []TypeFieldSchema
	Example     string
}

func LookupCustomType(t string) (CustomFieldType, bool) {
	for _, c := range customTypeRegistry() {
		if c.Type == t {
			return c, true
		}
	}
	return CustomFieldType{}, false
}

func CustomTypeNames() []string {
	all := customTypeRegistry()
	out := make([]string, 0, len(all))
	for _, c := range all {
		out = append(out, c.Type)
	}
	return out
}

var customTypeRegistry = sync.OnceValue(buildCustomTypeRegistry)

func buildCustomTypeRegistry() []CustomFieldType {
	out := make([]CustomFieldType, 0, 2)
	if c, ok := buildEventRulesCustomType(); ok {
		out = append(out, c)
	}
	out = append(out, buildOperationsCustomType())
	return out
}

func buildEventRulesCustomType() (CustomFieldType, bool) {
	def, ok := platpkg.LookupGatewayType("event_mesh")
	if !ok {
		return CustomFieldType{}, false
	}
	var rulesField platpkg.FieldDef
	found := false
	for _, f := range def.Fields {
		if f.Name == "event_rules" {
			rulesField = f
			found = true
			break
		}
	}
	if !found {
		return CustomFieldType{}, false
	}
	return CustomFieldType{
		Type:        "event_rules",
		ItemKind:    "array<object>",
		Description: "List of event-routing rules. Each rule subscribes to one or more topics and routes messages to a target agent or workflow.",
		Fields:      SortedFields(projectRegistryFields(rulesField.Inner)),
		Example:     eventRulesExample,
	}, true
}

func projectRegistryFields(in []platpkg.FieldDef) []TypeFieldSchema {
	out := make([]TypeFieldSchema, 0, len(in))
	for _, f := range in {
		t := TypeFieldSchema{
			Name:        f.Name,
			Label:       f.Label,
			Type:        f.Type,
			Required:    f.Required,
			Hint:        f.Hint,
			Placeholder: f.Placeholder,
			Default:     f.Default,
			Order:       f.Order,
			Pattern:     f.Pattern,
			MinLen:      lenCapOrUnset(f.MinLen),
			MaxLen:      lenCapOrUnset(f.MaxLen),
			MinValue:    f.MinValue,
			MaxValue:    f.MaxValue,
			Secret:      f.Secret,
			ShownWhen:   f.ConditionalDisplay,
		}
		if !f.UIVisible {
			if t.Hint == "" {
				t.Hint = "[advanced]"
			} else {
				t.Hint = "[advanced] " + t.Hint
			}
		}
		for _, opt := range f.OneOf {
			t.OneOf = append(t.OneOf, TypeFieldOption{
				Value:       opt.Value,
				Label:       cmp.Or(opt.Label, opt.Value),
				Description: opt.Description,
			})
		}
		out = append(out, t)
	}
	return out
}

func lenCapOrUnset(v int) int {
	if v <= 0 {
		return -1
	}
	return v
}

const eventRulesExample = `- name: route_orders
  subscriptions:
    - topic: "events/orders/>"
  targetAgent: Orchestrator
  promptTemplate: "Process this order event from {topic}: {payload}"
  acknowledgmentPolicy:
    mode: on_completion
    timeoutSeconds: 300
  messageFormat: json
  successOutput:
    enabled: true
    topic: "events/orders/responses"
    topicType: static
    # Advanced output knobs (UI doesn't render these — YAML authors only):
    # responseType: custom           # publish customExpression, a sacexpr expression
    # customExpression: "input.payload:data[0]"  # a workflow target's result
    # format: json
    # outputSchema: { ... }          # JSON Schema; payload validated before publish
    # onValidationError: log         # log | drop
    # deliveryMode: guaranteed       # direct (default) | guaranteed: wait for the broker ack
    # headers:                       # Solace headers, named as in the Micro-Integration reference
    #   solace_dmqEligible: true     # dmqEligible, timeToLive and expiration need guaranteed
    #   solace_timeToLive: 30000     # milliseconds
    #   solace_priority: 7           # 0-255
    #   solace_correlationId: "{{text://user_data.forward_context:correlationId}}"  # needs forwardContext: {correlationId: "input.user_properties:correlationId"} on the rule
    # userProperties:                # literal, or a template when it contains {{...}}
    #   source: sam
  errorOutput:
    enabled: true
    topic: "events/orders/errors"
    topicType: static
# Workflow target instead of an agent (separate rule; targetAgent and targetWorkflowName are mutually exclusive):
# - name: route_payments
#   subscriptions:
#     - topic: "events/payments/>"
#   targetWorkflowName: PaymentReview
#   inputExpression: "input.payload:"   # set on every workflow target (omitted = empty input); no promptTemplate
`

func buildOperationsCustomType() CustomFieldType {
	f := func(name, label, typ string, required bool, order float64, hint string) TypeFieldSchema {
		return TypeFieldSchema{Name: name, Label: label, Type: typ, Required: required, Order: order, Hint: hint, MinLen: -1, MaxLen: -1}
	}
	return CustomFieldType{
		Type:        "operations",
		ItemKind:    "array<object>",
		Description: "One or more agent tools that share this event broker connection. Each operation maps a tool's JSON Schema inputs onto a Solace topic and message payload.",
		Fields: SortedFields([]TypeFieldSchema{
			f("tool_name", "Tool name", "string", true, 1, "Agent-facing tool name. Lowercase, digits, underscores; must start with a letter."),
			f("tool_description", "Tool description", "string", true, 2, "What the tool does and when the agent should call it (min 10 chars)."),
			f("topic", "Topic address", "string", true, 3, "Solace topic to publish to. Use {property_name} for dynamic levels sourced from input_schema."),
			f("wait_for_response", "Wait for response", "bool", true, 4, "true for Request-Reply (await a correlated reply); false for fire-and-forget Publish-Subscribe."),
			f("payload_format", "Payload format", "string", true, 5, "One of: json, yaml, text."),
			f("input_schema", "Input schema", "object", true, 6, "JSON Schema object. Add x-solace-payload-path to map a property into the payload; x-solace-context-expression to source it from runtime context."),
			f("qos", "Quality of service", "number", false, 7, "0 (direct) or 1 (guaranteed). Defaults to 1."),
			f("request_expiry_ms", "Request expiry (ms)", "number", false, 8, "[advanced] Reply timeout for Request-Reply, 1000-300000. Ignored when wait_for_response is false."),
			f("reply_mode", "Reply mode", "string", false, 9, "[advanced] Request-Reply only: temp_queue or p2p_inbox."),
		}),
		Example: operationsExample,
	}
}

const operationsExample = `- tool_name: create_replenishment_command
  tool_description: "Create a replenishment command for a store SKU. Use when stock is critically low or a stockout is predicted."
  topic: "retail/ca/store/{store_id}/replenishment/command/requested/v1"
  wait_for_response: false
  payload_format: json
  qos: 1
  input_schema:
    type: object
    required: [store_id, sourceSystem, sku, quantity, priority]
    properties:
      store_id:
        type: string
        description: Store identifier (dynamic topic level, not part of the payload)
      sourceSystem:
        type: string
        description: System or process that originated this event
        x-solace-payload-path: sourceSystem
      sku:
        type: string
        description: Product SKU to replenish
        x-solace-payload-path: sku
      quantity:
        type: integer
        minimum: 1
        description: Requested replenishment quantity
        x-solace-payload-path: quantity
      priority:
        type: string
        enum: [HIGH, MEDIUM, LOW]
        description: Urgency level
        x-solace-payload-path: priority
      freeText:
        type: string
        description: Additional context for the fulfillment team
        x-solace-payload-path: freeText
      correlationId:
        type: string
        description: Links related events across the same operational cycle
        x-solace-payload-path: correlationId
- tool_name: get_store_inventory
  tool_description: "Look up the current on-hand inventory for a SKU at a store before deciding whether replenishment is needed."
  topic: "retail/ca/store/{store_id}/inventory/query/request/v1"
  wait_for_response: true
  request_expiry_ms: 30000
  reply_mode: temp_queue
  qos: 1
  payload_format: json
  input_schema:
    type: object
    required: [store_id, sku]
    properties:
      store_id:
        type: string
        description: Store identifier (dynamic topic level, not part of the payload)
      sku:
        type: string
        description: Product SKU to query
        x-solace-payload-path: sku
`
