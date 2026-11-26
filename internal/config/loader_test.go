// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestYAMLLoaderLoad(t *testing.T) {
	env := map[string]string{
		"BROKER_URL":  "tcps://broker.example.com:55443",
		"BROKER_USER": "admin",
		"BROKER_PASS": "secret",
		"VPN_NAME":    "test-vpn",
	}
	lookup := mockLookup(env)
	loader := &YAMLLoader{Lookup: lookup}

	t.Run("full pipeline: includes + env vars + yaml", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml",
			"broker_url: ${BROKER_URL}\nbroker_username: ${BROKER_USER}\nbroker_password: ${BROKER_PASS}")

		writeTemp(t, dir, "app.yaml", `
shared_config:
  - broker: &broker
      !include shared.yaml

app:
  name: test-app
  broker:
    <<: *broker
    vpn: ${VPN_NAME}
`)

		cfg, err := loader.Load(context.Background(), filepath.Join(dir, "app.yaml"))
		if err != nil {
			t.Fatal(err)
		}

		app := cfg.GetSection("app")
		if app == nil {
			t.Fatal("expected 'app' section")
		}
		if got := app.GetString("name"); got != "test-app" {
			t.Errorf("app.name = %q, want %q", got, "test-app")
		}

		broker := app.GetSection("broker")
		if broker == nil {
			t.Fatal("expected 'app.broker' section")
		}
		if got := broker.GetString("broker_url"); got != "tcps://broker.example.com:55443" {
			t.Errorf("broker_url = %q, want %q", got, "tcps://broker.example.com:55443")
		}
		if got := broker.GetString("broker_username"); got != "admin" {
			t.Errorf("broker_username = %q, want %q", got, "admin")
		}
		if got := broker.GetString("vpn"); got != "test-vpn" {
			t.Errorf("vpn = %q, want %q", got, "test-vpn")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := loader.Load(context.Background(), "/nonexistent/path.yaml")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

func TestYAMLLoaderLoadBytes(t *testing.T) {
	env := map[string]string{"PORT": "8080"}
	loader := &YAMLLoader{Lookup: mockLookup(env)}

	cfg, err := loader.LoadBytes(context.Background(), []byte(`
host: localhost
port: ${PORT}
debug: true
`))
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.GetString("host"); got != "localhost" {
		t.Errorf("host = %q, want %q", got, "localhost")
	}
	if got := cfg.GetInt("port"); got != 8080 {
		t.Errorf("port = %d, want %d", got, 8080)
	}
	if got := cfg.GetBool("debug"); !got {
		t.Error("debug should be true")
	}
}

func TestMapConfig(t *testing.T) {
	loader := &YAMLLoader{Lookup: mockLookup(nil)}

	cfg, err := loader.LoadBytes(context.Background(), []byte(`
string_key: hello
int_key: 42
float_key: 3.14
bool_key: true
duration_string: 30s
duration_int: 5
nested:
  child: value
  number: 10
string_map:
  a: one
  b: two
string_slice:
  - alpha
  - beta
  - gamma
`))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("GetString", func(t *testing.T) {
		if got := cfg.GetString("string_key"); got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
		if got := cfg.GetString("missing"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
		if got := cfg.GetString("int_key"); got != "42" {
			t.Errorf("got %q, want %q", got, "42")
		}
	})

	t.Run("GetString null value", func(t *testing.T) {
		c := FromMap(map[string]any{
			"null_key": nil,
		})
		if got := c.GetString("null_key"); got != "" {
			t.Errorf("expected empty string for null value, got %q", got)
		}
	})

	t.Run("GetInt", func(t *testing.T) {
		if got := cfg.GetInt("int_key"); got != 42 {
			t.Errorf("got %d, want %d", got, 42)
		}
		if got := cfg.GetInt("float_key"); got != 3 {
			t.Errorf("got %d, want %d", got, 3)
		}
		if got := cfg.GetInt("missing"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("GetInt string coercion", func(t *testing.T) {
		c := FromMap(map[string]any{
			"str_num":     "8080",
			"str_invalid": "not-a-number",
		})
		if got := c.GetInt("str_num"); got != 8080 {
			t.Errorf("got %d, want 8080", got)
		}
		if got := c.GetInt("str_invalid"); got != 0 {
			t.Errorf("got %d, want 0 for invalid string", got)
		}
	})

	t.Run("GetBool", func(t *testing.T) {
		if got := cfg.GetBool("bool_key"); !got {
			t.Error("expected true")
		}
		if got := cfg.GetBool("missing"); got {
			t.Error("expected false for missing")
		}
		if got := cfg.GetBool("string_key"); got {
			t.Error("expected false for non-bool")
		}
	})

	t.Run("GetBool string coercion", func(t *testing.T) {
		c := FromMap(map[string]any{
			"str_true":  "true",
			"str_yes":   "yes",
			"str_one":   "1",
			"str_on":    "on",
			"str_ON":    "ON",
			"str_TRUE":  "TRUE",
			"str_false": "false",
			"str_no":    "no",
			"str_off":   "off",
			"str_other": "whatever",
		})
		truthy := []string{"str_true", "str_yes", "str_one", "str_on", "str_ON", "str_TRUE"}
		falsy := []string{"str_false", "str_no", "str_off", "str_other"}
		for _, k := range truthy {
			if got := c.GetBool(k); !got {
				t.Errorf("expected true for %s", k)
			}
		}
		for _, k := range falsy {
			if got := c.GetBool(k); got {
				t.Errorf("expected false for %s", k)
			}
		}
	})

	t.Run("GetBool treats unquoted YAML numbers as truthy, not silently false", func(t *testing.T) {
		c := FromMap(map[string]any{
			"int_one":       int(1),
			"int_zero":      int(0),
			"int_negative":  int(-1),
			"int64_one":     int64(1),
			"int64_zero":    int64(0),
			"float_one":     float64(1.0),
			"float_zero":    float64(0),
			"float_partial": float64(0.5),
		})
		trueCases := []string{"int_one", "int_negative", "int64_one", "float_one", "float_partial"}
		falseCases := []string{"int_zero", "int64_zero", "float_zero"}
		for _, k := range trueCases {
			if got := c.GetBool(k); !got {
				t.Errorf("expected true for %s", k)
			}
		}
		for _, k := range falseCases {
			if got := c.GetBool(k); got {
				t.Errorf("expected false for %s", k)
			}
		}
	})

	t.Run("GetDuration", func(t *testing.T) {
		if got := cfg.GetDuration("duration_string"); got != 30*time.Second {
			t.Errorf("got %v, want 30s", got)
		}
		if got := cfg.GetDuration("duration_int"); got != 5*time.Second {
			t.Errorf("got %v, want 5s", got)
		}
		if got := cfg.GetDuration("missing"); got != 0 {
			t.Errorf("got %v, want 0", got)
		}
	})

	t.Run("GetSection", func(t *testing.T) {
		nested := cfg.GetSection("nested")
		if nested == nil {
			t.Fatal("expected nested section")
		}
		if got := nested.GetString("child"); got != "value" {
			t.Errorf("got %q, want %q", got, "value")
		}
		if got := nested.GetInt("number"); got != 10 {
			t.Errorf("got %d, want %d", got, 10)
		}
		if got := cfg.GetSection("missing"); got != nil {
			t.Error("expected nil for missing section")
		}
	})

	t.Run("GetStringMap", func(t *testing.T) {
		m := cfg.GetStringMap("string_map")
		if m == nil {
			t.Fatal("expected map")
		}
		if m["a"] != "one" || m["b"] != "two" {
			t.Errorf("got %v", m)
		}
	})

	t.Run("GetStringSlice", func(t *testing.T) {
		s := cfg.GetStringSlice("string_slice")
		if len(s) != 3 || s[0] != "alpha" || s[1] != "beta" || s[2] != "gamma" {
			t.Errorf("got %v", s)
		}
	})

	t.Run("GetRaw", func(t *testing.T) {
		if got := cfg.GetRaw("int_key"); got != 42 {
			t.Errorf("got %v, want 42", got)
		}
		if got := cfg.GetRaw("missing"); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("Has", func(t *testing.T) {
		if !cfg.Has("string_key") {
			t.Error("expected Has(string_key) to be true")
		}
		if cfg.Has("missing") {
			t.Error("expected Has(missing) to be false")
		}
	})
}

func TestYAMLAnchorsAndMerges(t *testing.T) {
	loader := &YAMLLoader{Lookup: mockLookup(nil)}

	t.Run("anchor and alias", func(t *testing.T) {
		cfg, err := loader.LoadBytes(context.Background(), []byte(`
defaults: &defaults
  timeout: 30
  retries: 3

service:
  <<: *defaults
  name: my-service
`))
		if err != nil {
			t.Fatal(err)
		}

		svc := cfg.GetSection("service")
		if svc == nil {
			t.Fatal("expected 'service' section")
		}
		if got := svc.GetInt("timeout"); got != 30 {
			t.Errorf("timeout = %d, want 30", got)
		}
		if got := svc.GetInt("retries"); got != 3 {
			t.Errorf("retries = %d, want 3", got)
		}
		if got := svc.GetString("name"); got != "my-service" {
			t.Errorf("name = %q, want %q", got, "my-service")
		}
	})

	t.Run("merge with override", func(t *testing.T) {
		cfg, err := loader.LoadBytes(context.Background(), []byte(`
base: &base
  host: default-host
  port: 8080

override:
  <<: *base
  host: custom-host
`))
		if err != nil {
			t.Fatal(err)
		}

		o := cfg.GetSection("override")
		if o == nil {
			t.Fatal("expected 'override' section")
		}
		if got := o.GetString("host"); got != "custom-host" {
			t.Errorf("host = %q, want %q (override should win)", got, "custom-host")
		}
		if got := o.GetInt("port"); got != 8080 {
			t.Errorf("port = %d, want 8080 (inherited from base)", got)
		}
	})

	t.Run("multiple anchors SAM-style", func(t *testing.T) {
		cfg, err := loader.LoadBytes(context.Background(), []byte(`
shared_config:
  - broker_connection: &broker
      url: tcps://broker:55443
      user: admin
  - model_config: &model
      name: claude-sonnet
      max_tokens: 4096

agent:
  broker:
    <<: *broker
    queue: my-queue
  llm:
    <<: *model
    temperature: 0.7
`))
		if err != nil {
			t.Fatal(err)
		}

		agent := cfg.GetSection("agent")
		if agent == nil {
			t.Fatal("expected 'agent' section")
		}

		broker := agent.GetSection("broker")
		if got := broker.GetString("url"); got != "tcps://broker:55443" {
			t.Errorf("broker.url = %q", got)
		}
		if got := broker.GetString("queue"); got != "my-queue" {
			t.Errorf("broker.queue = %q", got)
		}

		llm := agent.GetSection("llm")
		if got := llm.GetString("name"); got != "claude-sonnet" {
			t.Errorf("llm.name = %q", got)
		}
		if got := llm.GetInt("max_tokens"); got != 4096 {
			t.Errorf("llm.max_tokens = %d", got)
		}
	})
}

func TestLoadBytes_InvalidYAML(t *testing.T) {
	loader := &YAMLLoader{Lookup: mockLookup(nil)}
	_, err := loader.LoadBytes(context.Background(), []byte(`{invalid: yaml: [}`))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadBytes_EmptyContent(t *testing.T) {
	loader := &YAMLLoader{Lookup: mockLookup(nil)}
	cfg, err := loader.LoadBytes(context.Background(), []byte(""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.Has("anything") {
		t.Error("empty config should not have any keys")
	}
}

func TestGetSection_NonMap(t *testing.T) {
	cfg := FromMap(map[string]any{"key": "a string, not a map"})
	if got := cfg.GetSection("key"); got != nil {
		t.Errorf("GetSection on string value should return nil, got %v", got)
	}
}

func TestGetStringSlice_NonSlice(t *testing.T) {
	cfg := FromMap(map[string]any{"key": "a string"})
	if got := cfg.GetStringSlice("key"); got != nil {
		t.Errorf("GetStringSlice on string value should return nil, got %v", got)
	}
}

func TestGetStringMap_NonMap(t *testing.T) {
	cfg := FromMap(map[string]any{"key": "a string"})
	if got := cfg.GetStringMap("key"); got != nil {
		t.Errorf("GetStringMap on string value should return nil, got %v", got)
	}
}

func TestGetDuration_InvalidString(t *testing.T) {
	cfg := FromMap(map[string]any{"key": "not-a-duration"})
	if got := cfg.GetDuration("key"); got != 0 {
		t.Errorf("GetDuration on invalid string should return 0, got %v", got)
	}
}

func TestGetDuration_Float64(t *testing.T) {
	cfg := FromMap(map[string]any{"key": float64(2.5)})
	if got := cfg.GetDuration("key"); got != 2*time.Second {
		t.Errorf("GetDuration(2.5) = %v, want 2s (truncated to int)", got)
	}
}

func TestExpandVarsEnv(t *testing.T) {
	t.Setenv("TEST_CONFIG_VAR", "expanded_value")
	got := ExpandVarsEnv("value=${TEST_CONFIG_VAR}")
	if got != "value=expanded_value" {
		t.Errorf("ExpandVarsEnv = %q, want %q", got, "value=expanded_value")
	}
}

func TestFullPipelineWithRealFiles(t *testing.T) {
	dir := t.TempDir()

	writeTemp(t, dir, "shared/broker.yaml",
		"broker_url: ${BROKER_URL}\nbroker_username: ${BROKER_USER, default}\nbroker_password: ${BROKER_PASS, default}")

	writeTemp(t, dir, "shared/model.yaml",
		"model_name: ${MODEL_NAME, claude-sonnet}\nmax_tokens: 4096")

	writeTemp(t, dir, "app.yaml", `
shared_config:
  - broker: &broker
      !include shared/broker.yaml
  - model: &model
      !include shared/model.yaml

agents:
  - name: web-agent
    broker:
      <<: *broker
      queue: web-agent-queue
    llm:
      <<: *model
`)

	env := map[string]string{
		"BROKER_URL":  "tcps://prod-broker:55443",
		"BROKER_USER": "prod-user",
		"BROKER_PASS": "prod-pass",
	}
	loader := &YAMLLoader{Lookup: mockLookup(env)}

	cfg, err := loader.Load(context.Background(), filepath.Join(dir, "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	agents := cfg.GetRaw("agents")
	agentList, ok := agents.([]any)
	if !ok || len(agentList) == 0 {
		t.Fatal("expected agents list")
	}
	agent, ok := agentList[0].(map[string]any)
	if !ok {
		t.Fatal("expected agent to be a map")
	}

	brokerRaw, ok := agent["broker"].(map[string]any)
	if !ok {
		t.Fatal("expected broker section")
	}

	if brokerRaw["broker_url"] != "tcps://prod-broker:55443" {
		t.Errorf("broker_url = %v", brokerRaw["broker_url"])
	}
	if brokerRaw["broker_username"] != "prod-user" {
		t.Errorf("broker_username = %v", brokerRaw["broker_username"])
	}
	if brokerRaw["queue"] != "web-agent-queue" {
		t.Errorf("queue = %v", brokerRaw["queue"])
	}

	llmRaw, ok := agent["llm"].(map[string]any)
	if !ok {
		t.Fatal("expected llm section")
	}
	if llmRaw["model_name"] != "claude-sonnet" {
		t.Errorf("model_name = %v, want %q (default)", llmRaw["model_name"], "claude-sonnet")
	}
	if llmRaw["max_tokens"] != 4096 {
		t.Errorf("max_tokens = %v", llmRaw["max_tokens"])
	}
}
