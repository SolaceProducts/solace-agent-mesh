// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func validCfg() map[string]any {
	return map[string]any{
		"access_token":    "tok",
		"phone_number_id": "123",
		"app_secret":      "sec",
		"verify_token":    "vt",
	}
}

func TestParseWhatsAppConfig_Defaults(t *testing.T) {
	c, err := ParseWhatsAppConfig(validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.GraphAPIVersion != defaultGraphAPIVersion {
		t.Errorf("GraphAPIVersion = %q, want default %q", c.GraphAPIVersion, defaultGraphAPIVersion)
	}
	if c.GraphAPIBaseURL != defaultGraphAPIBaseURL {
		t.Errorf("GraphAPIBaseURL = %q, want default %q", c.GraphAPIBaseURL, defaultGraphAPIBaseURL)
	}
}

func TestParseWhatsAppConfig_MissingRequired(t *testing.T) {
	for _, key := range []string{"access_token", "phone_number_id", "app_secret", "verify_token"} {
		cfg := validCfg()
		delete(cfg, key)
		_, err := ParseWhatsAppConfig(cfg)
		if err == nil {
			t.Errorf("missing %q: expected error, got nil", key)
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("missing %q: error %q should name the missing key", key, err.Error())
		}
	}
}

func TestParseWhatsAppConfig_CoercesNumericValues(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
	}{
		{"int, as YAML decodes an unquoted digit string", 120912345678},
		{"float64, as that int arrives after the platform's JSON hop", 1.20912345678e11},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCfg()
			cfg["phone_number_id"] = tt.value
			c, err := ParseWhatsAppConfig(cfg)
			if err != nil {
				t.Fatalf("unexpected error for numeric phone_number_id: %v", err)
			}
			if c.PhoneNumberID != "120912345678" {
				t.Errorf("PhoneNumberID = %q, want %q", c.PhoneNumberID, "120912345678")
			}
		})
	}
}

func TestDefaultMediaSizeCapIsPinned(t *testing.T) {
	t.Parallel()
	if defaultMaxMediaSizeMB != 25 {
		t.Errorf("defaultMaxMediaSizeMB = %d, want 25: changing it changes which files a deployed entrypoint accepts, so it needs a release note, not a silent edit", defaultMaxMediaSizeMB)
	}
}

func TestParseWhatsAppConfig_MediaSizeCap(t *testing.T) {
	c, err := ParseWhatsAppConfig(validCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.MaxMediaSizeMB != defaultMaxMediaSizeMB {
		t.Errorf("MaxMediaSizeMB default = %d, want %d", c.MaxMediaSizeMB, defaultMaxMediaSizeMB)
	}

	cfg := validCfg()
	cfg["max_media_size_mb"] = 25
	c, err = ParseWhatsAppConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.MaxMediaSizeMB != 25 {
		t.Errorf("MaxMediaSizeMB = %d, want 25", c.MaxMediaSizeMB)
	}

	cfg = validCfg()
	cfg["max_media_size_mb"] = 0
	if _, err = ParseWhatsAppConfig(cfg); err == nil {
		t.Fatalf("expected error for max_media_size_mb=0, got nil")
	}
}

func TestParseWhatsAppConfig_TrimsBaseURL(t *testing.T) {
	cfg := validCfg()
	cfg["graph_api_base_url"] = "https://mock.example/"
	c, err := ParseWhatsAppConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.GraphAPIBaseURL != "https://mock.example" {
		t.Errorf("GraphAPIBaseURL = %q, want trailing slash trimmed", c.GraphAPIBaseURL)
	}
}

func TestFinalText(t *testing.T) {
	task := &a2a.Task{}
	task.Status.Message = &a2a.Message{
		Parts: []a2a.Part{
			{Kind: "text", Text: "Hello "},
			{Kind: "data", Data: map[string]any{"x": 1}},
			{Kind: "text", Text: "world"},
		},
	}
	if got := finalText(task); got != "Hello world" {
		t.Errorf("finalText = %q, want %q", got, "Hello world")
	}

	empty := &a2a.Task{}
	if got := finalText(empty); got != "" {
		t.Errorf("finalText(no message) = %q, want empty", got)
	}
}

func TestParseWhatsAppConfig_MaxMediaSize(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"phone_number_id": "555",
			"access_token":    "tok",
			"app_secret":      "secret",
			"verify_token":    "verify",
		}
	}

	t.Run("numeric string is coerced", func(t *testing.T) {
		cfg := base()
		cfg["max_media_size_mb"] = "25"
		c, err := ParseWhatsAppConfig(cfg)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.MaxMediaSizeMB != 25 {
			t.Errorf("MaxMediaSizeMB = %d, want 25", c.MaxMediaSizeMB)
		}
	})

	t.Run("absent uses the default", func(t *testing.T) {
		c, err := ParseWhatsAppConfig(base())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.MaxMediaSizeMB != defaultMaxMediaSizeMB {
			t.Errorf("MaxMediaSizeMB = %d, want default %d", c.MaxMediaSizeMB, defaultMaxMediaSizeMB)
		}
	})

	for _, bad := range []any{"not-a-number", 0, -5, ""} {
		t.Run(fmt.Sprintf("present-but-invalid errors: %v", bad), func(t *testing.T) {
			cfg := base()
			cfg["max_media_size_mb"] = bad
			if _, err := ParseWhatsAppConfig(cfg); err == nil {
				t.Fatalf("expected error for max_media_size_mb=%v, got nil", bad)
			}
		})
	}
}

func TestParseWhatsAppConfig_DisableFileUploads(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		value   any
		present bool
		want    bool
		wantErr bool
	}{
		{name: "absent defaults to uploads enabled", want: false},
		{name: "explicit true", value: true, present: true, want: true},
		{name: "explicit false", value: false, present: true, want: false},
		{name: "quoted true", value: "true", present: true, want: true},
		{name: "quoted false", value: "false", present: true, want: false},
		{name: "unreadable value is rejected", value: "sometimes", present: true, wantErr: true},
		{name: "numeric value is rejected", value: 3, present: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validCfg()
			if tt.present {
				cfg["disable_file_uploads"] = tt.value
			}
			parsed, err := ParseWhatsAppConfig(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseWhatsAppConfig accepted %v, want a startup error", tt.value)
				}
				if !strings.Contains(err.Error(), "disable_file_uploads") {
					t.Errorf("error = %q, want it to name the offending key", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWhatsAppConfig: %v", err)
			}
			if parsed.DisableFileUploads != tt.want {
				t.Errorf("DisableFileUploads = %v, want %v", parsed.DisableFileUploads, tt.want)
			}
		})
	}
}

func TestParseWhatsAppConfig_RunAs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		value   any
		present bool
		want    string
		wantErr bool
	}{
		{name: "absent stays empty"},
		{name: "bare name", value: "ops", present: true, want: "ops"},
		{name: "padded name is trimmed", value: "  ops  ", present: true, want: "ops"},
		{name: "already prefixed passes through", value: "system:ops", present: true, want: "system:ops"},
		{name: "blank string stays empty", value: "   ", present: true, want: ""},
		{name: "a list is rejected", value: []any{"ops"}, present: true, wantErr: true},
		{name: "a map is rejected", value: map[string]any{"name": "ops"}, present: true, wantErr: true},
		{name: "a bare key with no value is rejected", value: nil, present: true, wantErr: true},
		{name: "a bool is rejected", value: true, present: true, wantErr: true},
		{name: "an int is rejected", value: 12345, present: true, wantErr: true},
		{name: "a name spelled no is still a name", value: "no", present: true, want: "no"},
		{name: "a name spelled yes is still a name", value: "yes", present: true, want: "yes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validCfg()
			if tt.present {
				cfg["run_as"] = tt.value
			}
			parsed, err := ParseWhatsAppConfig(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseWhatsAppConfig accepted run_as = %#v; a malformed value must not read as no run_as", tt.value)
				}
				if !strings.Contains(err.Error(), "run_as") {
					t.Errorf("error = %q, want it to name the offending key", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWhatsAppConfig: %v", err)
			}
			if parsed.RunAs != tt.want {
				t.Errorf("RunAs = %q, want %q", parsed.RunAs, tt.want)
			}
		})
	}
}
