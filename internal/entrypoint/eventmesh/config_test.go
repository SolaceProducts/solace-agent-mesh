// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"strings"
	"testing"
)

func TestParseConfig_MinimalValid(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url":      "tcp://localhost:55555",
			"broker_vpn":      "default",
			"broker_username": "user",
			"broker_password": "pass",
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BrokerConfig.BrokerURL != "tcp://localhost:55555" {
		t.Errorf("broker URL = %q, want tcp://localhost:55555", cfg.BrokerConfig.BrokerURL)
	}
	if cfg.BrokerConfig.VPN != "default" {
		t.Errorf("VPN = %q, want default", cfg.BrokerConfig.VPN)
	}

	if cfg.AckPolicy.Mode != "on_receive" {
		t.Errorf("default ack mode = %q, want on_receive", cfg.AckPolicy.Mode)
	}
	if cfg.AckPolicy.TimeoutSeconds != 300 {
		t.Errorf("default timeout = %d, want 300", cfg.AckPolicy.TimeoutSeconds)
	}
	if cfg.AckPolicy.OnFailure.Action != "nack" {
		t.Errorf("default failure action = %q, want nack", cfg.AckPolicy.OnFailure.Action)
	}
}

func TestParseConfig_MissingBrokerConfig(t *testing.T) {
	_, err := ParseConfig(map[string]any{}, false)
	if err == nil {
		t.Fatal("expected error for missing broker config")
	}
}

func TestParseConfig_MissingBrokerURL_Production(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_vpn": "default",
			"dev_mode":   false,
		},
	}, false)
	if err == nil {
		t.Fatal("expected error for missing broker_url in a production (dev_mode=false) config")
	}
}

func TestParseConfig_DevModeNoBrokerURLAllowed(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_vpn":      "default",
			"broker_username": "default",
		},
	}, false)
	if err != nil {
		t.Fatalf("dev-mode config without broker_url should parse; got %v", err)
	}
	if cfg.BrokerConfig.BrokerURL != "" {
		t.Errorf("expected empty broker_url, got %q", cfg.BrokerConfig.BrokerURL)
	}
}

func TestParseConfig_ExplicitDevModeNoBrokerURLAllowed(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"event_mesh_broker_config": map[string]any{"dev_mode": true},
	}, false)
	if err != nil {
		t.Fatalf("explicit dev_mode=true without broker_url should parse; got %v", err)
	}
}

func TestParseConfig_TLSSkipVerify(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  bool
	}{
		{"omitted", nil, false},
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string false", "false", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brokerMap := map[string]any{
				"broker_url": "tcps://broker:55443",
				"broker_vpn": "default",
			}
			if tc.value != nil {
				brokerMap["tls_skip_verify"] = tc.value
			}
			cfg, err := ParseConfig(map[string]any{"event_mesh_broker_config": brokerMap}, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := cfg.BrokerConfig.TLSSkipVerify; got != tc.want {
				t.Errorf("TLSSkipVerify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseConfig_FullConfig(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url":      "tcp://remote:55555",
			"broker_vpn":      "prod",
			"broker_username": "admin",
			"broker_password": "secret",
		},
		"acknowledgment_policy": map[string]any{
			"mode":            "on_completion",
			"timeout_seconds": 60,
			"on_failure": map[string]any{
				"action":       "ack",
				"nack_outcome": "failed",
			},
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "orders",
				"input_expression":  "input.payload:data",
				"target_agent_name": "order-agent",
				"on_success":        "order_response",
				"subscriptions": []any{
					map[string]any{"topic": "orders/>", "qos": 1},
				},
				"forward_context": map[string]any{
					"order_id": "input.payload:id",
				},
			},
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "order_response",
				"topic_expression":   "static:response/orders",
				"payload_expression": "input.payload:text",
				"payload_format":     "json",
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AckPolicy.Mode != "on_completion" {
		t.Errorf("ack mode = %q, want on_completion", cfg.AckPolicy.Mode)
	}
	if cfg.AckPolicy.TimeoutSeconds != 60 {
		t.Errorf("timeout = %d, want 60", cfg.AckPolicy.TimeoutSeconds)
	}
	if cfg.AckPolicy.OnFailure.Action != "ack" {
		t.Errorf("failure action = %q, want ack", cfg.AckPolicy.OnFailure.Action)
	}

	if len(cfg.EventHandlers) != 1 {
		t.Fatalf("event handlers count = %d, want 1", len(cfg.EventHandlers))
	}
	eh := cfg.EventHandlers[0]
	if eh.Name != "orders" {
		t.Errorf("handler name = %q, want orders", eh.Name)
	}
	if eh.TargetAgentName != "order-agent" {
		t.Errorf("target agent = %q, want order-agent", eh.TargetAgentName)
	}
	if eh.OnSuccess != "order_response" {
		t.Errorf("on_success = %q, want order_response", eh.OnSuccess)
	}
	if len(eh.Subscriptions) != 1 || eh.Subscriptions[0].Topic != "orders/>" {
		t.Errorf("subscriptions = %v", eh.Subscriptions)
	}
	if eh.ForwardContext["order_id"] != "input.payload:id" {
		t.Errorf("forward_context = %v", eh.ForwardContext)
	}

	oh, ok := cfg.OutputHandlers["order_response"]
	if !ok {
		t.Fatal("output handler order_response not found")
	}
	if oh.TopicExpression != "static:response/orders" {
		t.Errorf("topic expr = %q", oh.TopicExpression)
	}
	if oh.PayloadFormat != "json" {
		t.Errorf("payload format = %q, want json", oh.PayloadFormat)
	}
}

func TestParseConfig_InvalidOutputHandlerRef(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"name":       "test",
				"on_success": "nonexistent",
			},
		},
	}

	_, err := ParseConfig(appConfig, false)
	if err == nil {
		t.Fatal("expected error for invalid output handler reference")
	}
}

func TestParseConfig_EventHandlerMissingName(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"input_expression": "input.payload:",
			},
		},
	}

	_, err := ParseConfig(appConfig, false)
	if err == nil {
		t.Fatal("expected error for missing handler name")
	}
}

func TestParseConfig_ArtifactProcessing(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "art-handler",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"target_agent_name": "agent1",
				"artifact_processing": map[string]any{
					"extract_artifacts_expression": "input.payload:files",
					"artifact_definition": map[string]any{
						"filename":         "list_item:name",
						"content":          "list_item:data",
						"mime_type":        "list_item:type",
						"content_encoding": "base64",
					},
				},
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eh := cfg.EventHandlers[0]
	if eh.ArtifactProcessing == nil {
		t.Fatal("artifact processing is nil")
	}
	if eh.ArtifactProcessing.ExtractExpression != "input.payload:files" {
		t.Errorf("extract expr = %q", eh.ArtifactProcessing.ExtractExpression)
	}
	if eh.ArtifactProcessing.Definition.Filename != "list_item:name" {
		t.Errorf("filename = %q", eh.ArtifactProcessing.Definition.Filename)
	}
}

func TestParseConfig_PerHandlerAckPolicy(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "test",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"target_agent_name": "agent1",
				"acknowledgment_policy": map[string]any{
					"mode":            "on_completion",
					"timeout_seconds": 120,
				},
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eh := cfg.EventHandlers[0]
	if eh.AckPolicy == nil {
		t.Fatal("per-handler ack policy is nil")
	}
	if eh.AckPolicy.Mode != "on_completion" {
		t.Errorf("mode = %q, want on_completion", eh.AckPolicy.Mode)
	}
	if eh.AckPolicy.TimeoutSeconds != 120 {
		t.Errorf("timeout = %d, want 120", eh.AckPolicy.TimeoutSeconds)
	}
}

func TestParseConfig_OutputTransforms(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "out",
				"topic_expression":   "static:topic",
				"payload_expression": "input.payload:",
				"output_transforms": []any{
					map[string]any{
						"type":              "copy",
						"source_expression": "input.payload:text",
						"dest_expression":   "user_data.result:response",
					},
				},
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oh := cfg.OutputHandlers["out"]
	if len(oh.OutputTransforms) != 1 {
		t.Fatalf("transforms count = %d, want 1", len(oh.OutputTransforms))
	}
	tc := oh.OutputTransforms[0]
	if tc.Type != "copy" {
		t.Errorf("transform type = %q, want copy", tc.Type)
	}
	if tc.SourceExpression != "input.payload:text" {
		t.Errorf("source = %q", tc.SourceExpression)
	}
}

func TestParseConfig_StructuredInvocation(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "structured",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"target_agent_name": "agent1",
				"structured_invocation": map[string]any{
					"input_schema":  map[string]any{"type": "object"},
					"output_schema": map[string]any{"type": "string"},
				},
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	si := cfg.EventHandlers[0].StructuredInvocation
	if si == nil {
		t.Fatal("structured invocation is nil")
	}
	if si.InputSchema["type"] != "object" {
		t.Errorf("input schema = %v", si.InputSchema)
	}
}

func TestParseInvokeConfig_Basic(t *testing.T) {
	m := map[string]any{
		"module":   "my_module",
		"function": "my_func",
	}
	ic := parseInvokeConfig(m)
	if ic.Module != "my_module" {
		t.Errorf("module = %q", ic.Module)
	}
	if ic.Function != "my_func" {
		t.Errorf("function = %q", ic.Function)
	}
}

func TestParseInvokeConfig_WithParams(t *testing.T) {
	m := map[string]any{
		"module":   "mod",
		"function": "fn",
		"params": map[string]any{
			"positional": []any{"arg1", 42},
			"keyword": map[string]any{
				"key1": "val1",
			},
		},
	}
	ic := parseInvokeConfig(m)
	if len(ic.Params.Positional) != 2 {
		t.Errorf("positional count = %d, want 2", len(ic.Params.Positional))
	}
	if ic.Params.Keyword["key1"] != "val1" {
		t.Errorf("keyword.key1 = %v", ic.Params.Keyword["key1"])
	}
}

func TestParseInvokeConfig_NoParams(t *testing.T) {
	m := map[string]any{
		"function": "fn",
	}
	ic := parseInvokeConfig(m)
	if ic.Function != "fn" {
		t.Errorf("function = %q", ic.Function)
	}
	if len(ic.Params.Positional) != 0 {
		t.Errorf("positional = %v", ic.Params.Positional)
	}
}

func TestParseConfig_TransformWithProcessingFunction(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "out",
				"topic_expression":   "static:topic",
				"payload_expression": "input.payload:",
				"output_transforms": []any{
					map[string]any{
						"type":                   "map",
						"source_list_expression": "input.payload:items",
						"dest_list_expression":   "user_data.result:mapped",
						"processing_function": map[string]any{
							"module":   "my_module",
							"function": "process_item",
							"params": map[string]any{
								"positional": []any{"arg1"},
							},
						},
					},
				},
			},
		},
	}

	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	oh := cfg.OutputHandlers["out"]
	if len(oh.OutputTransforms) != 1 {
		t.Fatalf("transforms count = %d, want 1", len(oh.OutputTransforms))
	}
	tc := oh.OutputTransforms[0]
	if tc.ProcessingFunction == nil {
		t.Fatal("processing_function is nil")
	}
	if tc.ProcessingFunction.Module != "my_module" {
		t.Errorf("module = %q", tc.ProcessingFunction.Module)
	}
	if tc.ProcessingFunction.Function != "process_item" {
		t.Errorf("function = %q", tc.ProcessingFunction.Function)
	}
}

func TestParseConfig_SkipBrokerValidation(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{},
	}
	cfg, err := ParseConfig(appConfig, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BrokerConfig.BrokerURL != "" {
		t.Errorf("broker URL = %q, want empty", cfg.BrokerConfig.BrokerURL)
	}
}

func TestParseConfig_InvalidOnErrorRef(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"event_handlers": []any{
			map[string]any{
				"name":          "test",
				"subscriptions": []any{map[string]any{"topic": "in/>"}},
				"on_error":      "nonexistent",
			},
		},
	}

	_, err := ParseConfig(appConfig, false)
	if err == nil {
		t.Fatal("expected error for invalid on_error reference")
	}
}

func TestParseConfig_OutputHandlerMissingName(t *testing.T) {
	appConfig := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
		"output_handlers": []any{
			map[string]any{
				"topic_expression": "static:topic",
			},
		},
	}

	_, err := ParseConfig(appConfig, false)
	if err == nil {
		t.Fatal("expected error for missing output handler name")
	}
}

func TestNormalizeContentEncoding(t *testing.T) {
	valid := []struct{ in, want string }{
		{"", ""},
		{"static:base64", "static:base64"},
		{"static:utf-8", "static:utf-8"},
		{"input.payload:enc", "input.payload:enc"},
		{"list_item:enc", "list_item:enc"},
		{"base64", "static:base64"},
		{"utf-8", "static:utf-8"},
		{"base64url", "static:base64url"},
		{"base64raw", "static:base64raw"},
		{"base64rawurl", "static:base64rawurl"},
	}
	for _, tc := range valid {
		got, err := normalizeContentEncoding(tc.in)
		if err != nil {
			t.Errorf("normalizeContentEncoding(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeContentEncoding(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	invalid := []string{"binary", "gzip", "base65", "hex"}
	for _, enc := range invalid {
		if _, err := normalizeContentEncoding(enc); err == nil {
			t.Errorf("normalizeContentEncoding(%q) expected error, got nil", enc)
		}
	}
}

func TestParseConfig_ArtifactBareBase64RewrittenToStaticAtParse(t *testing.T) {
	appConfig := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"input_expression":  "input.payload:msg",
				"target_agent_name": "Agent",
				"artifact_processing": map[string]any{
					"extract_artifacts_expression": "input.payload:files",
					"artifact_definition": map[string]any{
						"filename":         "list_item:name",
						"content":          "list_item:data",
						"content_encoding": "base64",
					},
				},
			},
		},
	}
	cfg, err := ParseConfig(appConfig, true)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	got := cfg.EventHandlers[0].ArtifactProcessing.Definition.ContentEncoding
	if got != "static:base64" {
		t.Errorf("ContentEncoding = %q, want static:base64", got)
	}
}

func TestParseConfig_RejectsBothSubscriptionsAndExistingQueueName(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                "orders",
				"existing_queue_name": "preprov/q/orders",
				"subscriptions":       []any{map[string]any{"topic": "in/orders"}},
				"target_agent_name":   "Agent",
			},
		},
	}, true)
	if err == nil {
		t.Fatal("ParseConfig: want error when both subscriptions and existing_queue_name are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want 'mutually exclusive'", err)
	}
}

func TestParseConfig_RejectsNeitherSubscriptionsNorExistingQueueName(t *testing.T) {
	_, err := ParseConfig(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "orders",
				"target_agent_name": "Agent",
			},
		},
	}, true)
	if err == nil {
		t.Fatal("ParseConfig: want error when neither subscriptions nor existing_queue_name is set")
	}
	if !strings.Contains(err.Error(), "subscriptions is required") {
		t.Errorf("error = %v, want 'subscriptions is required'", err)
	}
}

func TestParseConfig_ExistingQueueNameParsed(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                "orders",
				"existing_queue_name": "preprov/q/orders",
				"target_agent_name":   "Agent",
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if got := cfg.EventHandlers[0].ExistingQueueName; got != "preprov/q/orders" {
		t.Errorf("ExistingQueueName = %q, want %q", got, "preprov/q/orders")
	}
	if subs := cfg.EventHandlers[0].Subscriptions; len(subs) != 0 {
		t.Errorf("Subscriptions = %v, want empty for an existing_queue_name-only handler", subs)
	}
}

func TestParseConfig_ArtifactInvalidEncoding(t *testing.T) {
	appConfig := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "in/>"}},
				"input_expression":  "input.payload:msg",
				"target_agent_name": "Agent",
				"artifact_processing": map[string]any{
					"extract_artifacts_expression": "input.payload:files",
					"artifact_definition": map[string]any{
						"filename":         "list_item:name",
						"content":          "list_item:data",
						"content_encoding": "binary",
					},
				},
			},
		},
	}
	if _, err := ParseConfig(appConfig, true); err == nil {
		t.Fatal("expected ParseConfig to error on unrecognised content_encoding")
	}
}

func eventFilterConfig(filter any) map[string]any {
	return map[string]any{
		"event_handlers": []any{map[string]any{
			"name":              "orders",
			"subscriptions":     []any{map[string]any{"topic": "in/>"}},
			"target_agent_name": "TestAgent",
			"event_filter":      filter,
		}},
	}
}

func TestParseConfig_EventFilterCompiled(t *testing.T) {
	cfg, err := ParseConfig(eventFilterConfig(largeOrdersFilter), true)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.EventHandlers[0].EventFilter == nil {
		t.Fatal("event_filter was not compiled onto the handler")
	}
}

func TestParseConfig_NullEventFilterIsAbsent(t *testing.T) {
	cfg, err := ParseConfig(eventFilterConfig(nil), true)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.EventHandlers[0].EventFilter != nil {
		t.Fatal("a null event_filter compiled to a filter")
	}
}

func TestParseConfig_EventFilterThatDoesNotCompileIsRefused(t *testing.T) {
	_, err := ParseConfig(eventFilterConfig(map[string]any{
		"function": "and_op",
		"params": map[string]any{"positional": []any{
			true,
			map[string]any{"invoke": map[string]any{"function": "matches_regex", "params": map[string]any{"positional": []any{}}}},
		}},
	}), true)
	if err == nil {
		t.Fatal("ParseConfig accepted an unknown filter function")
	}
	for _, want := range []string{`event handler "orders"`, "event_filter.params.positional[1].invoke.function", `"matches_regex" is not a filter function`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
