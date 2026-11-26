// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"encoding/json"
	"fmt"
)

type Config struct {
	ToolName        string         `json:"tool_name"`
	Description     string         `json:"description,omitempty"`
	Topic           string         `json:"topic"`
	WaitForResponse *bool          `json:"wait_for_response,omitempty"`
	Parameters      []Parameter    `json:"parameters,omitempty"`
	InputSchema     map[string]any `json:"input_schema,omitempty"`
	EventMeshConfig EventMeshCfg   `json:"event_mesh_config"`
}

type Parameter struct {
	Name              string `json:"name"`
	Type              string `json:"type,omitempty"`
	Required          bool   `json:"required,omitempty"`
	Description       string `json:"description,omitempty"`
	Enum              []any  `json:"enum,omitempty"`
	Default           any    `json:"default,omitempty"`
	PayloadPath       string `json:"payload_path,omitempty"`
	ContextExpression string `json:"context_expression,omitempty"`

	defaultPresent bool
}

func (p *Parameter) UnmarshalJSON(data []byte) error {
	type alias Parameter
	if err := json.Unmarshal(data, (*alias)(p)); err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	_, p.defaultPresent = raw["default"]
	return nil
}

func (p Parameter) HasDefault() bool {
	return p.defaultPresent
}

type EventMeshCfg struct {
	BrokerConfig                BrokerCfg `json:"broker_config"`
	RequestExpiryMs             int       `json:"request_expiry_ms,omitempty"`
	PayloadFormat               string    `json:"payload_format,omitempty"`
	PayloadEncoding             string    `json:"payload_encoding,omitempty"`
	UserPropertiesReplyTopicKey string    `json:"user_properties_reply_topic_key,omitempty"`
	UserPropertiesReplyMetaKey  string    `json:"user_properties_reply_metadata_key,omitempty"`

	QoS       *int   `json:"qos,omitempty"`
	ReplyMode string `json:"reply_mode,omitempty"`
}

const (
	replyModeTempQueue = "temp_queue"
	replyModeP2PInbox  = "p2p_inbox"
)

const (
	defaultReplyTopicKey    = "__solace_ai_connector_broker_request_response_topic__"
	defaultReplyMetadataKey = "__solace_ai_connector_broker_request_reply_metadata__"
)

type BrokerCfg struct {
	BrokerURL      string `json:"broker_url"`
	BrokerUsername string `json:"broker_username"`
	BrokerPassword string `json:"broker_password"`
	BrokerVPN      string `json:"broker_vpn"`
	TLSSkipVerify  bool   `json:"tls_skip_verify,omitempty"`
}

func LoadConfig(raw map[string]any) (*Config, error) {
	if raw == nil {
		return nil, fmt.Errorf("tool_config is required for the Event Mesh tool")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshaling tool_config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing tool_config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.EventMeshConfig.RequestExpiryMs == 0 {
		c.EventMeshConfig.RequestExpiryMs = 60000
	}
	if c.EventMeshConfig.PayloadFormat == "" {
		c.EventMeshConfig.PayloadFormat = "json"
	}
	if c.EventMeshConfig.PayloadEncoding == "" {
		c.EventMeshConfig.PayloadEncoding = "utf-8"
	}
	for i := range c.Parameters {
		if c.Parameters[i].Type == "" {
			c.Parameters[i].Type = "string"
		}
	}
}

func (c *Config) validate() error {
	if c.ToolName == "" {
		return fmt.Errorf("tool_name is required")
	}
	if c.Topic == "" {
		return fmt.Errorf("topic is required")
	}
	switch c.EventMeshConfig.PayloadFormat {
	case "json", "yaml", "text":
	default:
		return fmt.Errorf("event_mesh_config.payload_format must be json|yaml|text, got %q", c.EventMeshConfig.PayloadFormat)
	}
	switch c.EventMeshConfig.PayloadEncoding {
	case "utf-8", "base64":
	default:
		return fmt.Errorf("event_mesh_config.payload_encoding must be utf-8|base64, got %q", c.EventMeshConfig.PayloadEncoding)
	}
	if c.EventMeshConfig.BrokerConfig.BrokerURL == "" {
		return fmt.Errorf("event_mesh_config.broker_config.broker_url is required")
	}
	if q := c.EventMeshConfig.QoS; q != nil && *q != 0 && *q != 1 {
		return fmt.Errorf("event_mesh_config.qos must be 0 or 1, got %d", *q)
	}
	switch c.EventMeshConfig.ReplyMode {
	case "", replyModeTempQueue, replyModeP2PInbox:
	default:
		return fmt.Errorf("event_mesh_config.reply_mode must be %q or %q, got %q", replyModeTempQueue, replyModeP2PInbox, c.EventMeshConfig.ReplyMode)
	}
	seen := map[string]struct{}{}
	for i, p := range c.Parameters {
		if p.Name == "" {
			return fmt.Errorf("parameters[%d] is missing the required 'name' key", i)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("duplicate parameter name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		switch p.Type {
		case "string", "boolean", "integer", "number":
		default:
			return fmt.Errorf("parameters[%s].type must be string|boolean|integer|number, got %q", p.Name, p.Type)
		}
	}
	return nil
}

func (c *Config) ShouldWaitForResponse() bool {
	if c.WaitForResponse == nil {
		return true
	}
	return *c.WaitForResponse
}

func (c *Config) EffectiveQoS() int {
	if c.EventMeshConfig.QoS == nil {
		return 1
	}
	return *c.EventMeshConfig.QoS
}

func (c *Config) EffectiveReplyMode() string {
	if c.EventMeshConfig.ReplyMode == "" {
		return replyModeTempQueue
	}
	return c.EventMeshConfig.ReplyMode
}
