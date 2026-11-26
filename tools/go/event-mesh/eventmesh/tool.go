// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const defaultToolName = "event_mesh_request"

const baseInstructions = `Send a request into a Solace event mesh and (optionally) wait for a correlated reply.

## When to use
- Forwarding structured requests to a backend microservice over a Solace broker
- Publish-Subscribe messages for asynchronous downstream processing
- Patterns where the response is correlated by reply-to topic, not by message body

## Output
- For request/reply: the decoded reply payload returned to the LLM under data.payload
- For Publish-Subscribe: an acknowledgement that the message was published`

type AnyParams map[string]any

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(defaultToolName,
			"Sends a request to a Solace broker and optionally waits for a correlated reply.",
			handle,
			sdk.WithInstructions(baseInstructions),
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{Key: "tool_name", Type: "string", Required: true, Description: "The name of the tool as it will be invoked by the LLM."},
				sdk.ConfigSchemaField{Key: "description", Type: "string", Description: "A description of what this tool does."},
				sdk.ConfigSchemaField{Key: "topic", Type: "string", Required: true, Description: "Topic address to publish to. Supports {property_name} dynamic topic levels."},
				sdk.ConfigSchemaField{Key: "wait_for_response", Type: "boolean", Default: true, Description: "Whether to use Request-Reply. Set false to use Publish-Subscribe."},
			),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func dynamicSchema(raw map[string]any) *sdk.SchemaOverride {
	cfg, err := LoadConfig(raw)
	if err != nil {
		return nil
	}
	override := &sdk.SchemaOverride{}
	if cfg.ToolName != "" && cfg.ToolName != defaultToolName {
		override.Name = cfg.ToolName
	}
	if cfg.Description != "" {
		override.Description = cfg.Description
	}

	props := map[string]any{}
	required := []string{}
	if rawProperties, ok := cfg.InputSchema["properties"].(map[string]any); ok {
		props = rawProperties
	}
	if rawRequired, ok := cfg.InputSchema["required"].([]any); ok {
		for _, value := range rawRequired {
			if name, ok := value.(string); ok {
				required = append(required, name)
			}
		}
	}
	for _, p := range cfg.Parameters {
		if p.ContextExpression != "" {
			delete(props, p.Name)
			required = slices.DeleteFunc(required, func(name string) bool { return name == p.Name })
			continue
		}
		if _, exists := props[p.Name]; exists {
			continue
		}
		prop := map[string]any{"type": jsonSchemaType(p.Type)}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if len(p.Enum) > 0 {
			prop["enum"] = p.Enum
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}
	override.Parameters = &sdk.JSONSchema{
		Type:       "object",
		Properties: props,
		Required:   required,
	}
	return override
}

func jsonSchemaType(t string) string {
	switch t {
	case "string", "integer", "number", "boolean":
		return t
	default:
		return "string"
	}
}

func handle(ctx context.Context, params AnyParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	return process(ctx, configMapFrom(tc), params, newContextView(tc))
}

func process(ctx context.Context, cfgMap map[string]any, params AnyParams, view *contextView) (*sdk.Result, error) {
	cfg, err := LoadConfig(cfgMap)
	if err != nil {
		return sdk.Error(fmt.Sprintf("Configuration error: %v", err)), nil
	}

	payload, resolved := buildPayloadAndResolveParams(cfg.Parameters, params, view)

	topic, err := fillTopicTemplate(cfg.Topic, resolved)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	if topic == "" {
		return sdk.Error("Configuration error: Resulting topic is empty. Please define a 'topic' in the tool's configuration."), nil
	}

	body, err := encodePayload(payload, cfg.EventMeshConfig.PayloadFormat, cfg.EventMeshConfig.PayloadEncoding)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	b, err := newBroker(ctx, cfg.EventMeshConfig.BrokerConfig)
	if err != nil {
		return sdk.Error(fmt.Sprintf("broker initialization failed: %v", err)), nil
	}
	if err := b.Connect(ctx); err != nil {
		return sdk.Error(fmt.Sprintf("broker connection failed: %v", err)), nil
	}
	defer func() { _ = b.Disconnect(context.Background()) }()

	msg := &broker.Message{Payload: body}

	if cfg.ShouldWaitForResponse() {
		timeout := time.Duration(cfg.EventMeshConfig.RequestExpiryMs) * time.Millisecond
		var reply *broker.Message
		var replyErr error
		if cfg.EffectiveReplyMode() == replyModeP2PInbox {
			reply, replyErr = b.RequestReplyInbox(ctx, topic, msg, timeout)
		} else {
			replyTopic := fmt.Sprintf("sam-go/event-mesh-tool/reply/%s", idgen.New())
			requestID := idgen.New()
			msg.ReplyTo = replyTopic
			msg.CorrelationID = requestID
			if err := injectSACReplyHeaders(msg, replyTopic, requestID, cfg.EventMeshConfig); err != nil {
				return sdk.Error(fmt.Sprintf("encoding reply metadata: %v", err)), nil
			}
			reply, replyErr = requestWithQoS(ctx, b, topic, msg, timeout, cfg.EffectiveQoS())
		}
		if replyErr != nil {
			slog.Default().With("logger_name", "event-mesh").Error("event-mesh request failed", "topic", topic, "tool", cfg.ToolName, "error", replyErr)
			return sdk.Error("Request failed. No response received."), nil
		}
		decoded := decodePayload(reply.Payload, cfg.EventMeshConfig.PayloadFormat, cfg.EventMeshConfig.PayloadEncoding)
		return sdk.OK("Request processed successfully.",
			sdk.WithData(map[string]any{"payload": decoded})), nil
	}

	if err := publishWithQoS(ctx, b, topic, msg, cfg.EffectiveQoS()); err != nil {
		slog.Default().With("logger_name", "event-mesh").Error("event-mesh publish failed", "topic", topic, "tool", cfg.ToolName, "error", err)
		return sdk.Error(fmt.Sprintf("publish failed: %v", err)), nil
	}
	return sdk.OK("Request sent asynchronously."), nil
}

func publishWithQoS(ctx context.Context, b broker.Broker, topic string, msg *broker.Message, qos int) error {
	if qos == 0 {
		return b.Publish(ctx, topic, msg)
	}
	return b.PublishGuaranteed(ctx, topic, msg)
}

func injectSACReplyHeaders(msg *broker.Message, replyTopic, requestID string, em EventMeshCfg) error {
	if msg.UserProperties == nil {
		msg.UserProperties = map[string]string{}
	}
	topicKey := em.UserPropertiesReplyTopicKey
	if topicKey == "" {
		topicKey = defaultReplyTopicKey
	}
	metaKey := em.UserPropertiesReplyMetaKey
	if metaKey == "" {
		metaKey = defaultReplyMetadataKey
	}
	stack := []map[string]string{{"request_id": requestID, "response_topic": replyTopic}}
	encoded, err := json.Marshal(stack)
	if err != nil {
		return fmt.Errorf("marshal reply metadata: %w", err)
	}
	msg.UserProperties[topicKey] = replyTopic
	msg.UserProperties[metaKey] = string(encoded)
	return nil
}

func requestWithQoS(ctx context.Context, b broker.Broker, topic string, msg *broker.Message, timeout time.Duration, qos int) (*broker.Message, error) {
	if msg.ReplyTo == "" {
		return nil, fmt.Errorf("message has no ReplyTo topic")
	}
	replyQueue, err := b.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("create reply subscription: %w", err)
	}
	defer func() { _ = b.DeleteQueue(replyQueue.Name()) }()

	if err := b.Subscribe(replyQueue, msg.ReplyTo); err != nil {
		return nil, fmt.Errorf("subscribe reply: %w", err)
	}

	if err := publishWithQoS(ctx, b, topic, msg, qos); err != nil {
		return nil, fmt.Errorf("publish request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return b.Receive(timeoutCtx, replyQueue)
}

func configMapFrom(tc *sdk.ToolContext) map[string]any {
	keys := []string{
		"tool_name",
		"description",
		"topic",
		"wait_for_response",
		"parameters",
		"event_mesh_config",
	}
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := tc.GetConfig(k); ok {
			out[k] = v
		}
	}
	return out
}
