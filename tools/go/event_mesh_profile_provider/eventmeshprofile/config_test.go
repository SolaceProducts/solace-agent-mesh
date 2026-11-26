// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func validConfig() map[string]any {
	return map[string]any{
		"broker_url":            "tcp://broker:55555",
		"broker_vpn":            "default",
		"broker_username":       "u",
		"broker_password":       "p",
		"request_topic":         "test/req/v1/{request_id}",
		"response_topic_prefix": "test/reply/v1/",
		"payload_key":           "email",
	}
}

func loadConfigWith(cfg map[string]any) (*Config, string) {
	tc := &sdk.ToolContext{}
	tc.SetConfigForTest(cfg)
	return LoadConfig(tc)
}

func TestLoadConfig_HappyPath(t *testing.T) {
	t.Parallel()
	c, errMsg := loadConfigWith(validConfig())
	if errMsg != "" {
		t.Fatalf("LoadConfig: %s", errMsg)
	}
	if c.BrokerURL != "tcp://broker:55555" || c.PayloadKey != "email" {
		t.Errorf("unexpected config: %+v", c)
	}
}

func TestLoadConfig_PayloadKeyDefaultsToEmail(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	delete(cfg, "payload_key")
	c, errMsg := loadConfigWith(cfg)
	if errMsg != "" {
		t.Fatalf("LoadConfig: %s", errMsg)
	}
	if c.PayloadKey != "email" {
		t.Errorf("payload_key default: got %q, want %q", c.PayloadKey, "email")
	}
}

func TestLoadConfig_RequiredFieldErrors(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"broker_url", "broker_vpn", "broker_username", "broker_password",
		"request_topic", "response_topic_prefix",
	} {
		t.Run("missing_"+key, func(t *testing.T) {
			cfg := validConfig()
			delete(cfg, key)
			_, errMsg := loadConfigWith(cfg)
			if !strings.Contains(errMsg, key) {
				t.Errorf("missing %s: error %q should mention the key", key, errMsg)
			}
		})
	}
}

func TestLoadConfig_RequestTopicMustContainPlaceholder(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg["request_topic"] = "test/req/v1/no-placeholder"
	_, errMsg := loadConfigWith(cfg)
	if !strings.Contains(errMsg, requestIDPlaceholder) {
		t.Errorf("request_topic without placeholder: error %q should mention %s", errMsg, requestIDPlaceholder)
	}
}

func TestLoadConfig_ResponseTopicPrefixMustNotContainPlaceholder(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg["response_topic_prefix"] = "test/reply/v1/{request_id}"
	_, errMsg := loadConfigWith(cfg)
	if !strings.Contains(errMsg, "response_topic_prefix") || !strings.Contains(errMsg, requestIDPlaceholder) {
		t.Errorf("response_topic_prefix with placeholder: error %q should mention the constraint", errMsg)
	}
}

func TestLoadConfig_TopicsOverlap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		reqTopic    string
		respPrefix  string
		wantOverlap bool
	}{
		{"exact level overlap", "ACME/req/v1/{request_id}", "ACME/req/v1/", true},
		{"prefix overlap then deeper", "ACME/req/{request_id}", "ACME/req/replies", true},
		{"adjacent-but-distinct level (no overlap)", "acme/req-{request_id}", "acme/req-replies", false},
		{"disjoint trees", "ACME/req/{request_id}", "OTHER/reply/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg["request_topic"] = tt.reqTopic
			cfg["response_topic_prefix"] = tt.respPrefix
			_, errMsg := loadConfigWith(cfg)
			gotOverlap := strings.Contains(errMsg, "overlap")
			if gotOverlap != tt.wantOverlap {
				t.Errorf("overlap detection for %q + %q: got overlap=%v, want %v (error %q)",
					tt.reqTopic, tt.respPrefix, gotOverlap, tt.wantOverlap, errMsg)
			}
		})
	}
}

func TestLoadConfig_RequestTimeoutCap(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg["request_timeout_seconds"] = maxRequestTimeoutSeconds + 1
	_, errMsg := loadConfigWith(cfg)
	if !strings.Contains(errMsg, "request_timeout_seconds") {
		t.Errorf("over-cap timeout: error %q should mention request_timeout_seconds", errMsg)
	}
}

func TestLoadConfig_UnknownKeysWarned(t *testing.T) {
	t.Parallel()
	cfg := validConfig()
	cfg["payload_keey"] = "email"
	if _, errMsg := loadConfigWith(cfg); errMsg != "" {
		t.Fatalf("typo key shouldn't break LoadConfig: %s", errMsg)
	}
	for _, k := range []string{"payload_keey"} {
		found := false
		for _, known := range recognizedConfigKeys {
			if known == k {
				found = true
			}
		}
		if found {
			t.Errorf("typo key %q must not be in recognizedConfigKeys", k)
		}
	}
}

func TestRequestTimeout_Default(t *testing.T) {
	t.Parallel()
	c := &Config{}
	if got := c.RequestTimeout(); got != defaultRequestTimeout {
		t.Errorf("RequestTimeout default: got %v, want %v", got, defaultRequestTimeout)
	}
}

func TestRequestTimeout_Configured(t *testing.T) {
	t.Parallel()
	c := &Config{RequestTimeoutSeconds: 17}
	if got := c.RequestTimeout(); got != 17*time.Second {
		t.Errorf("RequestTimeout 17s: got %v", got)
	}
}

func TestBuildRequestTopic_Substitutes(t *testing.T) {
	t.Parallel()
	c := &Config{RequestTopic: "x/{request_id}/y"}
	if got := c.BuildRequestTopic("abc"); got != "x/abc/y" {
		t.Errorf("got %q, want %q", got, "x/abc/y")
	}
}

func TestBuildReplyTopic_Appends(t *testing.T) {
	t.Parallel()
	c := &Config{ResponseTopicPrefix: "x/reply/"}
	if got := c.BuildReplyTopic("abc"); got != "x/reply/abc" {
		t.Errorf("got %q, want %q", got, "x/reply/abc")
	}
	c2 := &Config{ResponseTopicPrefix: "x/reply"}
	if got := c2.BuildReplyTopic("abc"); got != "x/reply/abc" {
		t.Errorf("got %q, want %q", got, "x/reply/abc")
	}
}
