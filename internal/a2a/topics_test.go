// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"strings"
	"testing"
)

func TestDiscoveryTopics(t *testing.T) {
	ns := "myns"
	if got := DiscoveryAgentCardsTopic(ns); got != "myns/a2a/v1/discovery/agentcards" {
		t.Errorf("DiscoveryAgentCardsTopic = %q", got)
	}
	if got := DiscoveryGatewayCardsTopic(ns); got != "myns/a2a/v1/discovery/gatewaycards" {
		t.Errorf("DiscoveryGatewayCardsTopic = %q", got)
	}
	if got := DiscoverySubscription(ns); got != "myns/a2a/v1/discovery/>" {
		t.Errorf("DiscoverySubscription = %q", got)
	}
}

func TestFeedbackTopic(t *testing.T) {
	if got := FeedbackTopic("myns"); got != "myns/sam/v1/feedback" {
		t.Errorf("FeedbackTopic = %q, want myns/sam/v1/feedback", got)
	}
	if got := FeedbackTopic("myns/"); got != "myns/sam/v1/feedback" {
		t.Errorf("FeedbackTopic with trailing slash = %q, want myns/sam/v1/feedback", got)
	}
}

func TestNotifyTopics(t *testing.T) {
	ns := "myns"
	cases := map[string]string{
		NotifyRequestTopic(ns, "trent"):         "myns/sam/v1/notify/request/trent",
		NotifyRequestTopic(ns, "iss/sub*>"):     "myns/sam/v1/notify/request/iss_sub__",
		NotifyRequestTopic(ns, ""):              "myns/sam/v1/notify/request/_",
		NotifyRequestSubscription(ns):           "myns/sam/v1/notify/request/>",
		NotifyDeliverTopic(ns, "slack", "req1"): "myns/sam/v1/notify/deliver/slack/req1",
		NotifyDeliverSubscription(ns, "slack"):  "myns/sam/v1/notify/deliver/slack/>",
		NotifyReplyTopic(ns, "req1"):            "myns/sam/v1/notify/reply/req1",
		NotifyReplySubscription(ns):             "myns/sam/v1/notify/reply/>",
		NotifyResponseTopic(ns, "wf1", "req1"):  "myns/sam/v1/notify/response/wf1/req1",
		NotifyInboxCreatedTopic(ns):             "myns/sam/v1/notify/inboxCreated",
		UserEventTopic(ns):                      "myns/sam/v1/gateway/userEvent",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("notify topic = %q, want %q", got, want)
		}
	}
}

func TestSpendEventTopic(t *testing.T) {
	if got, want := SpendEventTopic("myns/"), "myns/sam/v1/governance/spend"; got != want {
		t.Errorf("SpendEventTopic = %q, want %q", got, want)
	}
}

func TestBudgetStateTopic(t *testing.T) {
	if got, want := BudgetStateTopic("myns/"), "myns/sam/v1/governance/budgetState"; got != want {
		t.Errorf("BudgetStateTopic = %q, want %q", got, want)
	}
}

func TestSpendFiledTopic(t *testing.T) {
	if got, want := SpendFiledTopic("myns/"), "myns/sam/v1/governance/spendFiled"; got != want {
		t.Errorf("SpendFiledTopic = %q, want %q", got, want)
	}
}

func TestBudgetsChangedTopic(t *testing.T) {
	if got, want := BudgetsChangedTopic("myns/"), "myns/sam/v1/governance/budgetsChanged"; got != want {
		t.Errorf("BudgetsChangedTopic = %q, want %q", got, want)
	}
}

func TestToolAuthRegisterTopicIsFixedLiteralNotWildcard(t *testing.T) {
	const want = "myns/sam/v1/toolauth/register"
	if got := ToolAuthRegisterTopic("myns"); got != want {
		t.Errorf("ToolAuthRegisterTopic = %q, want %q", got, want)
	}
	if got := ToolAuthRegisterSubscription("myns"); got != want {
		t.Errorf("ToolAuthRegisterSubscription = %q, want %q", got, want)
	}
	if got := ToolAuthRegisterTopic("myns/"); got != want {
		t.Errorf("trailing-slash namespace = %q, want %q", got, want)
	}
}

func TestAgentRequestTopic(t *testing.T) {
	if got := AgentRequestTopic("ns", "search-agent"); got != "ns/a2a/v1/agent/request/search-agent" {
		t.Errorf("AgentRequestTopic = %q", got)
	}
}

func TestGatewayTopics(t *testing.T) {
	ns, gw, task := "ns", "gw-1", "task-001"
	if got := GatewayStatusTopic(ns, gw, task); got != "ns/a2a/v1/gateway/status/gw-1/task-001" {
		t.Errorf("GatewayStatusTopic = %q", got)
	}
	if got := GatewayResponseTopic(ns, gw, task); got != "ns/a2a/v1/gateway/response/gw-1/task-001" {
		t.Errorf("GatewayResponseTopic = %q", got)
	}
	if got := GatewayStatusSubscription(ns, gw); got != "ns/a2a/v1/gateway/status/gw-1/>" {
		t.Errorf("GatewayStatusSubscription = %q", got)
	}
	if got := GatewayResponseSubscription(ns, gw); got != "ns/a2a/v1/gateway/response/gw-1/>" {
		t.Errorf("GatewayResponseSubscription = %q", got)
	}
}

func TestPeerTopics(t *testing.T) {
	ns, agent, subtask := "ns", "orchestrator", "sub-001"
	if got := PeerStatusTopic(ns, agent, subtask); got != "ns/a2a/v1/agent/status/orchestrator/sub-001" {
		t.Errorf("PeerStatusTopic = %q", got)
	}
	if got := PeerResponseTopic(ns, agent, subtask); got != "ns/a2a/v1/agent/response/orchestrator/sub-001" {
		t.Errorf("PeerResponseTopic = %q", got)
	}
	if got := PeerStatusSubscription(ns, agent); got != "ns/a2a/v1/agent/status/orchestrator/>" {
		t.Errorf("PeerStatusSubscription = %q", got)
	}
	if got := PeerResponseSubscription(ns, agent); got != "ns/a2a/v1/agent/response/orchestrator/>" {
		t.Errorf("PeerResponseSubscription = %q", got)
	}
}

func TestControlTopics(t *testing.T) {
	ns := "myns"
	topics := ControlSubscriptionTopics(ns)
	if len(topics) != 5 {
		t.Fatalf("ControlSubscriptionTopics returned %d topics, want 5", len(topics))
	}
	if topics[0] != "myns/sam/v1/control/get/>" {
		t.Errorf("ControlSubscriptionTopics[0] = %q", topics[0])
	}
	if got := ControlAppsTopic(ns, "get"); got != "myns/sam/v1/control/get/apps" {
		t.Errorf("ControlAppsTopic = %q", got)
	}
	if got := ControlAppTopic(ns, "my-agent", "delete"); got != "myns/sam/v1/control/delete/apps/my-agent" {
		t.Errorf("ControlAppTopic = %q", got)
	}
	if got := ControlAppCustomTopic(ns, "my-agent", "get", "instances/foo"); got != "myns/sam/v1/control/get/apps/my-agent/instances/foo" {
		t.Errorf("ControlAppCustomTopic = %q", got)
	}
}

func TestParseControlTopic(t *testing.T) {
	tests := []struct {
		name       string
		namespace  string
		topic      string
		wantMethod string
		wantRes    string
		wantApp    string
		wantCustom []string
		wantErr    bool
	}{
		{
			name:       "list apps",
			namespace:  "myns",
			topic:      "myns/sam/v1/control/get/apps",
			wantMethod: "get",
			wantRes:    "apps",
		},
		{
			name:       "get single app",
			namespace:  "myns",
			topic:      "myns/sam/v1/control/get/apps/my-agent",
			wantMethod: "get",
			wantRes:    "apps",
			wantApp:    "my-agent",
		},
		{
			name:       "custom endpoint",
			namespace:  "myns",
			topic:      "myns/sam/v1/control/get/apps/awe/instances/foo",
			wantMethod: "get",
			wantRes:    "apps",
			wantApp:    "awe",
			wantCustom: []string{"instances", "foo"},
		},
		{
			name:       "delete app",
			namespace:  "myns",
			topic:      "myns/sam/v1/control/delete/apps/old-agent",
			wantMethod: "delete",
			wantRes:    "apps",
			wantApp:    "old-agent",
		},
		{
			name:      "wrong prefix",
			namespace: "myns",
			topic:     "other/sam/v1/control/get/apps",
			wantErr:   true,
		},
		{
			name:      "too short",
			namespace: "myns",
			topic:     "myns/sam/v1/control/get",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseControlTopic(tt.namespace, tt.topic)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Resource != tt.wantRes {
				t.Errorf("Resource = %q, want %q", got.Resource, tt.wantRes)
			}
			if got.AppName != tt.wantApp {
				t.Errorf("AppName = %q, want %q", got.AppName, tt.wantApp)
			}
			if len(got.CustomPath) != len(tt.wantCustom) {
				t.Errorf("CustomPath = %v, want %v", got.CustomPath, tt.wantCustom)
			} else {
				for i, p := range got.CustomPath {
					if p != tt.wantCustom[i] {
						t.Errorf("CustomPath[%d] = %q, want %q", i, p, tt.wantCustom[i])
					}
				}
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		wantErr   bool
	}{
		{"empty", "", false},
		{"simple", "solace-agent-mesh", false},
		{"with_dot", "team.alpha", false},
		{"with_underscore", "team_alpha", false},
		{"nested_segments", "tenant/env/dev", false},
		{"trailing_slash", "tenant/env/", false},
		{"alphanumeric_mix", "Tenant1.env-2_v3", false},

		{"star_wildcard", "ns/*", true},
		{"hash_wildcard", "ns/>", true},
		{"embedded_star", "n*s", true},

		{"leading_slash", "/ns", true},
		{"traversal_segment", "tenant/../other", true},
		{"traversal_only", "..", true},

		{"colon", "ns:dev", true},
		{"space", "ns dev", true},
		{"backslash", "ns\\dev", true},
		{"hash", "ns#dev", true},
		{"newline", "ns\ndev", true},
		{"non_ascii", "námespace", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNamespace(tt.namespace)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateNamespace(%q) = nil, want error", tt.namespace)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateNamespace(%q) = %v, want nil", tt.namespace, err)
			}
		})
	}
}

func TestQueueName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		subsystem string
		parts     []string
		want      string
	}{
		{"normal", "default", "a2a", []string{"my-agent"}, "default/q/a2a/my-agent"},
		{"trailing slash", "default/", "a2a", []string{"my-agent"}, "default/q/a2a/my-agent"},
		{"leading slash", "/default", "a2a", []string{"my-agent"}, "default/q/a2a/my-agent"},
		{"both slashes", "/default/", "a2a", []string{"my-agent"}, "default/q/a2a/my-agent"},
		{"multiple parts", "default", "gdk", []string{"gateway", "gw-1"}, "default/q/gdk/gateway/gw-1"},
		{"no parts", "default", "platform", nil, "default/q/platform"},
		{"nested namespace", "tenant/env", "a2a", []string{"agent"}, "tenant/env/q/a2a/agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QueueName(tt.namespace, tt.subsystem, tt.parts...); got != tt.want {
				t.Errorf("QueueName(%q, %q, %v) = %q, want %q", tt.namespace, tt.subsystem, tt.parts, got, tt.want)
			}
		})
	}
}

func TestGatewayLogTopics(t *testing.T) {
	topic := GatewayLogTopic("acme/", "gw1", "t-1")
	if topic != "acme/sam/v1/gateway/log/gw1/t-1" {
		t.Fatalf("GatewayLogTopic = %q", topic)
	}
	if sub := GatewayLogSubscription("acme", "gw1"); sub != "acme/sam/v1/gateway/log/gw1/>" {
		t.Fatalf("GatewayLogSubscription = %q", sub)
	}
	if !IsGatewayLogTopic(topic) {
		t.Fatal("IsGatewayLogTopic false for a log topic")
	}
	for _, other := range []string{
		GatewayStatusTopic("acme", "gw1", "t-1"),
		GatewayResponseTopic("acme", "gw1", "t-1"),
	} {
		if IsGatewayLogTopic(other) {
			t.Fatalf("IsGatewayLogTopic true for %q", other)
		}
	}
	if strings.HasPrefix(topic, "acme/a2a/") {
		t.Fatal("log topic is inside the a2a namespace")
	}
}

func TestWhatsAppIngressQueueNames(t *testing.T) {
	if got := WhatsAppIngressQueueName("acme/", "gw1"); got != "acme/q/gdk/whatsapp_ingress/gw1" {
		t.Fatalf("WhatsAppIngressQueueName = %q", got)
	}
	if got := WhatsAppIngressQueueNameV2("acme/", "gw1"); got != "acme/q/gdk/whatsapp_ingress/gw1/v2" {
		t.Fatalf("WhatsAppIngressQueueNameV2 = %q", got)
	}
}

func TestWhatsAppDoorbellTopics(t *testing.T) {
	topic := WhatsAppDoorbellTopic("acme/", "gw1")
	if topic != "acme/sam/v1/gdk/whatsapp/doorbell/gw1" {
		t.Fatalf("WhatsAppDoorbellTopic = %q; the user belongs in the body, never the topic", topic)
	}
	if !IsWhatsAppDoorbellTopic(topic) {
		t.Fatal("IsWhatsAppDoorbellTopic false for a doorbell topic")
	}
	for _, other := range []string{
		"acme/sam/v1/gdk/whatsapp/ingress/gw1",
		GatewayStatusTopic("acme", "gw1", "t-1"),
		GatewayLogTopic("acme", "gw1", "t-1"),
	} {
		if IsWhatsAppDoorbellTopic(other) {
			t.Fatalf("IsWhatsAppDoorbellTopic true for %q", other)
		}
	}
	if strings.HasPrefix(topic, "acme/a2a/") {
		t.Fatal("doorbell topic is inside the a2a namespace")
	}
}

func TestGatewayStreamTaskTopics(t *testing.T) {
	if got, want := GatewayStreamTaskTopic("ns/", "gw", "t1"), "ns/sam/v1/gateway/stream/task/gw/t1"; got != want {
		t.Errorf("GatewayStreamTaskTopic = %q, want %q", got, want)
	}
	if got, want := GatewayStreamTaskSubscription("ns/", "gw"), "ns/sam/v1/gateway/stream/task/gw/>"; got != want {
		t.Errorf("GatewayStreamTaskSubscription = %q, want %q", got, want)
	}
}
