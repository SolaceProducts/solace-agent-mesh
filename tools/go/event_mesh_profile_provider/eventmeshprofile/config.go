// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultRequestTimeout = 25 * time.Second

	maxRequestTimeoutSeconds = 30

	defaultPayloadKey = "email"

	requestIDPlaceholder = "{request_id}"
)

type Config struct {
	BrokerURL      string `json:"broker_url"`
	BrokerVPN      string `json:"broker_vpn"`
	BrokerUsername string `json:"broker_username"`
	BrokerPassword string `json:"broker_password"`
	TLSSkipVerify  bool   `json:"tls_skip_verify,omitempty"`

	RequestTopic        string `json:"request_topic"`
	ResponseTopicPrefix string `json:"response_topic_prefix"`

	PayloadKey string `json:"payload_key,omitempty"`

	RequestTimeoutSeconds int `json:"request_timeout_seconds,omitempty"`

	FieldMappingConfig FieldMapping `json:"field_mapping_config,omitempty"`
}

func LoadConfig(tc *sdk.ToolContext) (*Config, string) {
	warnUnknownConfigKeys(tc)
	raw := configMap(tc)
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Sprintf("event_mesh_profile_provider: marshal tool_config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Sprintf("event_mesh_profile_provider: parse tool_config: %v", err)
	}
	if msg := cfg.validateAndDefault(); msg != "" {
		return nil, msg
	}
	return &cfg, ""
}

var recognizedConfigKeys = []string{
	"broker_url",
	"broker_vpn",
	"broker_username",
	"broker_password",
	"tls_skip_verify",
	"request_topic",
	"response_topic_prefix",
	"payload_key",
	"request_timeout_seconds",
	"field_mapping_config",
}

func configMap(tc *sdk.ToolContext) map[string]any {
	out := make(map[string]any, len(recognizedConfigKeys))
	for _, k := range recognizedConfigKeys {
		if v, ok := tc.GetConfig(k); ok {
			out[k] = v
		}
	}
	return out
}

func warnUnknownConfigKeys(tc *sdk.ToolContext) {
	known := make(map[string]struct{}, len(recognizedConfigKeys))
	for _, k := range recognizedConfigKeys {
		known[k] = struct{}{}
	}
	var unknown []string
	for _, k := range tc.ConfigKeys() {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		logger().Warn("unknown tool_config keys ignored", "keys", unknown, "recognized", recognizedConfigKeys)
	}
}

func (c *Config) validateAndDefault() string {
	c.BrokerURL = strings.TrimSpace(c.BrokerURL)
	c.BrokerVPN = strings.TrimSpace(c.BrokerVPN)
	c.RequestTopic = strings.TrimSpace(c.RequestTopic)
	c.ResponseTopicPrefix = strings.TrimSpace(c.ResponseTopicPrefix)
	c.PayloadKey = strings.TrimSpace(c.PayloadKey)

	if c.PayloadKey == "" {
		c.PayloadKey = defaultPayloadKey
	}

	if c.BrokerURL == "" {
		return "broker_url is required in tool_config."
	}
	if c.BrokerVPN == "" {
		return "broker_vpn is required in tool_config."
	}
	if c.BrokerUsername == "" {
		return "broker_username is required in tool_config."
	}
	if c.BrokerPassword == "" {
		return "broker_password is required in tool_config."
	}
	if c.RequestTopic == "" {
		return "request_topic is required in tool_config."
	}
	if !strings.Contains(c.RequestTopic, requestIDPlaceholder) {
		return fmt.Sprintf("request_topic must contain %s placeholder.", requestIDPlaceholder)
	}
	if c.ResponseTopicPrefix == "" {
		return "response_topic_prefix is required in tool_config."
	}
	if c.RequestTimeoutSeconds > maxRequestTimeoutSeconds {
		return fmt.Sprintf("request_timeout_seconds must be at most %d seconds.", maxRequestTimeoutSeconds)
	}
	if strings.Contains(c.ResponseTopicPrefix, requestIDPlaceholder) {
		return fmt.Sprintf("response_topic_prefix must not contain %s; the per-call request_id is appended as a suffix.", requestIDPlaceholder)
	}
	if topicsOverlap(c.RequestTopic, c.ResponseTopicPrefix) {
		return "response_topic_prefix must not overlap request_topic; the reply subscription would match the outbound publish."
	}
	if msg := c.FieldMappingConfig.Validate(); msg != "" {
		return msg
	}
	return ""
}

func topicsOverlap(requestTopic, responseTopicPrefix string) bool {
	literal, _, _ := strings.Cut(requestTopic, requestIDPlaceholder)
	literal = strings.Trim(literal, "/")
	prefix := strings.Trim(responseTopicPrefix, "/")
	if literal == "" || prefix == "" {
		return false
	}
	reqLevels := strings.Split(literal, "/")
	respLevels := strings.Split(prefix, "/")
	if len(respLevels) < len(reqLevels) {
		return false
	}
	for i, lvl := range reqLevels {
		if respLevels[i] != lvl {
			return false
		}
	}
	return true
}

func (c *Config) RequestTimeout() time.Duration {
	if c.RequestTimeoutSeconds > 0 {
		return time.Duration(c.RequestTimeoutSeconds) * time.Second
	}
	return defaultRequestTimeout
}

func (c *Config) BuildRequestTopic(requestID string) string {
	return strings.ReplaceAll(c.RequestTopic, requestIDPlaceholder, requestID)
}

func (c *Config) BuildReplyTopic(requestID string) string {
	prefix := strings.TrimRight(c.ResponseTopicPrefix, "/")
	return prefix + "/" + requestID
}

var configSchema = []sdk.ConfigSchemaField{
	{Key: "broker_url", Type: "string", Description: "Solace broker URL the profile-lookup responder listens on.", Required: true},
	{Key: "broker_vpn", Type: "string", Description: "Solace broker VPN name.", Required: true},
	{Key: "broker_username", Type: "string", Description: "Solace broker username.", Required: true},
	{Key: "broker_password", Type: "string", Description: "Solace broker password.", Required: true, Secret: true},
	{Key: "tls_skip_verify", Type: "boolean", Description: "Skip TLS verification when connecting to the broker. Test only.", Default: false},
	{Key: "request_topic", Type: "string", Description: "Topic template the request is published to; must contain {request_id}.", Required: true},
	{Key: "response_topic_prefix", Type: "string", Description: "Prefix appended with {request_id} to derive the per-call reply topic.", Required: true},
	{Key: "payload_key", Type: "string", Description: "JSON field name the lookup_value is wrapped as in the outbound payload.", Default: defaultPayloadKey},
	{Key: "request_timeout_seconds", Type: "integer", Description: "Per-call broker round-trip timeout in seconds (max 30).", Default: 25},
	{Key: "field_mapping_config", Type: "object", Description: "Reply-to-Profile transformation pipeline. See FieldMapping struct."},
}
