// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFromEnv(t *testing.T) {
	for envVar := range knownKeyEnvVars {
		t.Setenv(envVar, "")
	}
	for _, envVar := range knownBaseURLEnvVars {
		t.Setenv(envVar, "")
	}

	if got := DetectFromEnv(); len(got) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(got))
	}

	t.Setenv("OPENAI_API_KEY", "sk-test-key-abc123")

	candidates := DetectFromEnv()
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Provider != "openai" {
		t.Errorf("expected openai, got %q", c.Provider)
	}
	if c.EnvVarName != "OPENAI_API_KEY" {
		t.Errorf("expected OPENAI_API_KEY, got %q", c.EnvVarName)
	}
	if c.Key != "sk-test-key-abc123" {
		t.Errorf("expected sk-test-key-abc123, got %q", c.Key)
	}
	if c.BaseURL != "" {
		t.Errorf("expected empty base URL, got %q", c.BaseURL)
	}

	t.Setenv("OPENAI_API_BASE", "https://my-proxy.com/v1")

	candidates = DetectFromEnv()
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].BaseURL != "https://my-proxy.com" {
		t.Errorf("expected https://my-proxy.com (v1 stripped), got %q", candidates[0].BaseURL)
	}
}

func TestDetectFromDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envFile, []byte(`
# Comment
OPENAI_API_KEY="sk-from-dotenv"
OTHER_VAR=something
OPENAI_API_BASE="https://litellm.local:4000/v1"
`), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("OPENAI_API_KEY", "")

	candidates := DetectFromDotEnv(envFile)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if c.Provider != "openai" {
		t.Errorf("expected openai, got %q", c.Provider)
	}
	if c.Key != "sk-from-dotenv" {
		t.Errorf("expected sk-from-dotenv, got %q", c.Key)
	}
	if c.BaseURL != "https://litellm.local:4000" {
		t.Errorf("expected https://litellm.local:4000, got %q", c.BaseURL)
	}
}

func TestDetectFromDotEnvSkipsExistingEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envFile, []byte(`OPENAI_API_KEY=sk-from-dotenv`), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("OPENAI_API_KEY", "sk-from-env")

	candidates := DetectFromDotEnv(envFile)
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates (env var already set), got %d", len(candidates))
	}
}

func TestParsePastedInput(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantProvider string
		wantBaseURL  string
		wantErr      bool
	}{
		{
			name:         "raw openai key",
			input:        "sk-proj-abcdefghij1234567890",
			wantProvider: "openai",
		},
		{
			name:         "raw anthropic key",
			input:        "sk-ant-api03-abcdefghij1234567890",
			wantProvider: "anthropic",
		},
		{
			name:         "raw google key",
			input:        "AIzaSyAbcdefghij1234567890",
			wantProvider: "google",
		},
		{
			name:         "raw groq key",
			input:        "gsk_abcdefghij1234567890abcdefghij",
			wantProvider: "groq",
		},
		{
			name:         "raw deepseek key",
			input:        "dsk-abcdefghij1234567890abcdefghij",
			wantProvider: "openai",
		},
		{
			name:         "env var assignment",
			input:        `OPENAI_API_KEY=sk-proj-abcdefghij1234567890`,
			wantProvider: "openai",
		},
		{
			name:         "export statement",
			input:        `export ANTHROPIC_API_KEY="sk-ant-abcdefghij1234567890"`,
			wantProvider: "anthropic",
		},
		{
			name:         "curl command with bearer",
			input:        `curl -H "Authorization: Bearer sk-proj-abcdefghij1234567890" https://my-litellm.com/v1/chat/completions`,
			wantProvider: "openai",
			wantBaseURL:  "https://my-litellm.com",
		},
		{
			name:         "key with URL",
			input:        "sk-proj-abcdefghij1234567890 https://api.proxy.com/v1",
			wantProvider: "openai",
			wantBaseURL:  "https://api.proxy.com",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no key found",
			input:   "hello world",
			wantErr: true,
		},
		{
			name:         "json snippet",
			input:        `{"api_key": "sk-proj-abcdefghij1234567890", "base_url": "https://example.com"}`,
			wantProvider: "openai",
			wantBaseURL:  "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParsePastedInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Provider != tt.wantProvider {
				t.Errorf("provider: got %q, want %q", c.Provider, tt.wantProvider)
			}
			if tt.wantBaseURL != "" && c.BaseURL != tt.wantBaseURL {
				t.Errorf("baseURL: got %q, want %q", c.BaseURL, tt.wantBaseURL)
			}
			if c.Key == "" {
				t.Error("expected non-empty key")
			}
			if c.MaskedKey == "" {
				t.Error("expected non-empty masked key")
			}
		})
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sk-proj-abcdefghij1234567890", "sk-pro******"},
		{"short12345", "short1******"},
		{"sk-ant-api03-very-long-key-here", "sk-ant******"},
		{"tiny", "ti******"},
	}

	for _, tt := range tests {
		got := MaskKey(tt.input)
		if got != tt.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDefaultModelForProvider(t *testing.T) {
	tests := map[string]string{
		"openai":    "gpt-5.6-sol",
		"anthropic": "claude-sonnet-5",
		"google":    "gemini-3.6-flash",
		"groq":      "llama-3.3-70b-versatile",
		"unknown":   "gpt-4o",
	}

	for provider, want := range tests {
		if got := DefaultModelForProvider(provider); got != want {
			t.Errorf("DefaultModelForProvider(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := map[string]string{
		"https://api.openai.com/v1":  "https://api.openai.com",
		"https://api.openai.com/v1/": "https://api.openai.com",
		"https://api.openai.com":     "https://api.openai.com",
		"https://proxy.local:4000/":  "https://proxy.local:4000",
	}

	for input, want := range tests {
		if got := normalizeBaseURL(input); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
}
