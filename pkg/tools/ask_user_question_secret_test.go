// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func askCtxWithArtifacts(svc *mockArtifactService, reached *bool) *tool.Context {
	toolCtx := newTestToolCtx(svc)
	//nolint:staticcheck // deprecated in favour of tool.Pending(); still the reach probe here.
	toolCtx.WaitForUserInput = func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
		*reached = true
		return &a2a.UserInputResponseData{Status: "answered"}
	}
	return toolCtx
}

const analyticsDBWithSecret = `{
  "name": "analytics_db",
  "type": "sql",
  "subtype": "postgres",
  "values": {
    "hostname": "db.internal",
    "port": 5432,
    "database": "reporting",
    "username": "reporting",
    "password": "<<__SAM_REQUIRED__>>"
  }
}`

const analyticsDBAwaitingEverything = `{
  "name": "analytics_db",
  "type": "sql",
  "subtype": "postgres",
  "values": {
    "hostname": "<<__SAM_REQUIRED__>>",
    "port": 5432,
    "database": "<<__SAM_REQUIRED__>>",
    "username": "<<__SAM_REQUIRED__>>",
    "password": "<<__SAM_REQUIRED__>>"
  }
}`

const analyticsDBFullyConfigured = `{
  "name": "analytics_db",
  "type": "sql",
  "subtype": "postgres",
  "values": {
    "hostname": "db.internal",
    "port": 5432,
    "database": "reporting",
    "username": "reporting",
    "password": "hunter2"
  }
}`

func TestAskUserQuestionRefusesPlaceholderedSecret(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{
				"question":    "What is the password for the 'reporting' user on the analytics database?",
				"header":      "Password",
				"placeholder": "database password",
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the secret ask was admitted", result.Status)
	}
	for _, want := range []string{"ask_user_question was not used", "password", "analytics_db", samRequiredPlaceholder, "masked"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message = %q, missing %q", result.Message, want)
		}
	}
	if reached {
		t.Error("the form reached the user; the guard must short-circuit before WaitForUserInput")
	}
}

func TestAskUserQuestionRefusesSecretNamedOnlyByPlaceholder(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{
				"question":    "What should I use to authenticate as 'reporting'?",
				"header":      "Auth",
				"placeholder": "the account password",
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionRefusesPrefixedSecretField(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "retail_event_mesh.json", `{
  "name": "retail_event_mesh",
  "type": "event_mesh",
  "values": {"event_mesh_broker_config": {"broker_url": "tcps://broker:55443", "broker_password": "<<__SAM_REQUIRED__>>"}}
}`, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "retail_event_mesh",
		"questions": []any{
			map[string]any{"question": "What password should the event mesh connect with?", "header": "Password", "placeholder": "password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "broker_password") {
		t.Errorf("message = %q, expected it to name broker_password", result.Message)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionRefusesA2AAgentSecret(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "weather_proxy.json", `{
  "name": "WeatherProxy",
  "agentUrl": "https://weather.example.com",
  "agentAuthConfig": {"type": "bearer", "token": "<<__SAM_REQUIRED__>>"}
}`, SAMA2AAgentConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "weather_proxy",
		"questions": []any{
			map[string]any{"question": "What bearer token should I use?", "header": "Token", "placeholder": "bearer token"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the bearer token ask was admitted", result.Status)
	}
	for _, want := range []string{"token", "weather_proxy", samRequiredPlaceholder, "masked"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message = %q, missing %q", result.Message, want)
		}
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionRefusesEveryA2AAuthTypeSecret(t *testing.T) {
	cases := []struct {
		name        string
		authConfig  string
		question    string
		placeholder string
		blamed      string
	}{
		{
			name:        "apikey",
			authConfig:  `{"type": "apikey", "apiKey": "<<__SAM_REQUIRED__>>", "headerName": "X-API-Key"}`,
			question:    "What API key should the proxy send?",
			placeholder: "the API key",
			blamed:      "apiKey",
		},
		{
			name:        "oauth2",
			authConfig:  `{"type": "oauth2", "clientId": "sam", "clientSecret": "<<__SAM_REQUIRED__>>", "tokenEndpoint": "https://weather.example.com/token"}`,
			question:    "What client secret was issued for this app?",
			placeholder: "the client secret",
			blamed:      "clientSecret",
		},
		{
			name:        "basic",
			authConfig:  `{"type": "basic", "username": "sam", "password": "<<__SAM_REQUIRED__>>"}`,
			question:    "What password should I authenticate with?",
			placeholder: "agent password",
			blamed:      "password",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "weather_proxy.json", fmt.Sprintf(`{
  "name": "WeatherProxy",
  "agentUrl": "https://weather.example.com",
  "agentAuthConfig": %s
}`, tc.authConfig), SAMA2AAgentConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "weather_proxy",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Auth", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error — the %s secret ask was admitted", result.Status, tc.name)
			}
			if !strings.Contains(result.Message, tc.blamed) {
				t.Errorf("message = %q, expected it to name %q", result.Message, tc.blamed)
			}
			if reached {
				t.Error("the form reached the user")
			}
		})
	}
}

func TestAskUserQuestionRefusesA2AAgentCardSecret(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "weather_proxy.json", `{
  "name": "WeatherProxy",
  "agentUrl": "https://weather.example.com",
  "agentAuthConfig": {"type": "none"},
  "agentCardAuthConfig": {"type": "bearer", "token": "<<__SAM_REQUIRED__>>"}
}`, SAMA2AAgentConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "weather_proxy",
		"questions": []any{
			map[string]any{"question": "Which token should I use to fetch the agent card?", "header": "Card", "placeholder": "card token"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the card-fetch token ask was admitted", result.Status)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionAdmitsNonSecretA2AAuthFields(t *testing.T) {
	cases := []struct {
		name        string
		authConfig  string
		question    string
		placeholder string
	}{
		{
			name:        "headerName alongside a placeholdered apiKey",
			authConfig:  `{"type": "apikey", "apiKey": "<<__SAM_REQUIRED__>>", "headerName": "<<__SAM_REQUIRED__>>"}`,
			question:    "Which HTTP header should carry the credential?",
			placeholder: "header name",
		},
		{
			name:        "clientId alongside a placeholdered clientSecret",
			authConfig:  `{"type": "oauth2", "clientId": "<<__SAM_REQUIRED__>>", "clientSecret": "<<__SAM_REQUIRED__>>", "tokenEndpoint": "https://weather.example.com/token"}`,
			question:    "Which client id was registered for this app?",
			placeholder: "client id",
		},
		{
			name:        "tokenEndpoint alongside a placeholdered clientSecret",
			authConfig:  `{"type": "oauth2", "clientId": "sam", "clientSecret": "<<__SAM_REQUIRED__>>", "tokenEndpoint": "<<__SAM_REQUIRED__>>"}`,
			question:    "Which token endpoint issues the access tokens?",
			placeholder: "https://example.com/oauth/token",
		},
		{
			name:        "username alongside a placeholdered password",
			authConfig:  `{"type": "basic", "username": "<<__SAM_REQUIRED__>>", "password": "<<__SAM_REQUIRED__>>"}`,
			question:    "Which username should the proxy authenticate as?",
			placeholder: "service account name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "weather_proxy.json", fmt.Sprintf(`{
  "name": "WeatherProxy",
  "agentUrl": "https://weather.example.com",
  "agentAuthConfig": %s
}`, tc.authConfig), SAMA2AAgentConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "weather_proxy",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Config", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("a %s question was refused: %q", tc.name, result.Message)
			}
			if !reached {
				t.Error("the form did not reach the user")
			}
		})
	}
}

func TestSecretFieldSetsExcludeNonSecretA2AAuthFields(t *testing.T) {
	for _, field := range []string{"type", "headerName", "username", "clientId", "tokenEndpoint", "scopes"} {
		if isPlatformSecretField(field) {
			t.Errorf("%q is treated as a platform secret, but the a2a-agent schema declares it a "+
				"plain field of authConfig; a question asking for it would be refused", field)
		}
	}

	declared := a2aAuthConfigPropertyNames(t)
	for _, field := range declared {
		if A2AAgentSecretFieldLabels[field] != nil || field == "type" {
			continue
		}
		if isPlatformSecretField(field) {
			t.Errorf("the a2a-agent schema declares authConfig property %q, which no auth type lists as "+
				"a secret, yet the guard treats it as one", field)
		}
	}
	for field := range A2AAgentSecretFieldLabels {
		if !slices.Contains(declared, field) {
			t.Errorf("A2AAgentSecretFieldLabels lists %q but the a2a-agent schema's authConfig has no such "+
				"property — the guard is matching a name the config artifact never carries", field)
		}
	}
}

func a2aAuthConfigPropertyNames(t *testing.T) []string {
	t.Helper()
	raw, err := schema.SchemaContent("a2a-agent")
	if err != nil {
		t.Fatalf("SchemaContent(a2a-agent): %v", err)
	}
	var doc struct {
		Defs struct {
			AuthConfig struct {
				Properties map[string]any `json:"properties"`
			} `json:"authConfig"`
		} `json:"$defs"`
	}
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parsing a2a-agent schema: %v", unmarshalErr)
	}
	if len(doc.Defs.AuthConfig.Properties) == 0 {
		t.Fatal("a2a-agent schema declares no authConfig properties")
	}
	names := make([]string, 0, len(doc.Defs.AuthConfig.Properties))
	for name := range doc.Defs.AuthConfig.Properties {
		names = append(names, name)
	}
	return names
}

func TestAskUserQuestionFindsConfigThroughManifest(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, `
name: reporting-stack
components:
  - filename: pg_reporting.json
    type: connector
    name: analytics_db
    origin: session
`, SAMBuildManifestMIMEType)
	saveTestArtifact(t.Context(), svc, toolCtx, "pg_reporting.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the database password?", "header": "Password", "placeholder": "password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the manifest names the artifact", result.Status)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionAdmitsNonSecretConfigAlongsideAPlaceholderedSecret(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the hostname of the analytics database?", "header": "Host", "placeholder": "db.internal"},
			map[string]any{"question": "Which port does it listen on?", "header": "Port", "placeholder": "5432"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("the non-secret config form was refused: %q", result.Message)
	}
	if !reached {
		t.Error("the form did not reach the user")
	}
}

func TestAskUserQuestionAdmitsPlaceholderedNonSecretFields(t *testing.T) {
	cases := []struct {
		name        string
		question    string
		placeholder string
	}{
		{"hostname", "What is the hostname of the analytics database?", "db.internal"},
		{"hostname named by placeholder", "Where does it live?", "database hostname"},
		{"username", "Which username should connect to the analytics database?", "reporting"},
		{"database", "Which database should I connect to?", "database name"},
		{"port", "Which port does it listen on?", "5432"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBAwaitingEverything, SAMConnectorConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "analytics_db",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Config", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("a %s question was refused: %q", tc.name, result.Message)
			}
			if !reached {
				t.Error("the form did not reach the user")
			}
		})
	}
}

func TestAskUserQuestionStillRefusesTheSecretInAnAllPlaceholderedConfig(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBAwaitingEverything, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the hostname?", "header": "Host", "placeholder": "db.internal"},
			map[string]any{"question": "And the password for that user?", "header": "Password", "placeholder": "database password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the secret ask rode in on a non-secret one", result.Status)
	}
	blamed := fmt.Sprintf("ask_user_question was not used: %q on component %q", "password", "analytics_db")
	if !strings.HasPrefix(result.Message, blamed) {
		t.Errorf("message = %q, expected it to blame password rather than a non-secret field", result.Message)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionAdmitsPasswordQuestionWhenNothingIsMarked(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBFullyConfigured, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the database password?", "header": "Password", "placeholder": "database password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("refused with no marking present: %q", result.Message)
	}
	if !reached {
		t.Error("the form did not reach the user")
	}
}

func TestAskUserQuestionFailsOpenWithoutReadableEvidence(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, svc *mockArtifactService, toolCtx *tool.Context)
	}{
		{
			name:  "no config artifact saved yet",
			setup: func(*testing.T, *mockArtifactService, *tool.Context) {},
		},
		{
			name: "config artifact is not parseable",
			setup: func(t *testing.T, svc *mockArtifactService, toolCtx *tool.Context) {
				saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json",
					"\x00\x01 not json, not yaml: [unclosed", SAMConnectorConfigMIMEType)
			},
		},
		{
			name: "the store cannot be read",
			setup: func(t *testing.T, svc *mockArtifactService, toolCtx *tool.Context) {
				saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)
				svc.failLoads = map[string]bool{"analytics_db.json": true, manifestFilename: true}
			},
		},
		{
			name: "no artifact service is configured",
			setup: func(t *testing.T, _ *mockArtifactService, toolCtx *tool.Context) {
				toolCtx.ArtifactService = nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			tc.setup(t, svc, toolCtx)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "analytics_db",
				"questions": []any{
					map[string]any{"question": "What is the database password?", "header": "Password", "placeholder": "database password"},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("refused without readable evidence: %q", result.Message)
			}
			if !reached {
				t.Error("the form did not reach the user")
			}
		})
	}
}

func TestAskUserQuestionSecretGuardReadsTextOnlySiblingsOfAChoiceQuestion(t *testing.T) {
	choiceQuestion := map[string]any{
		"question": "How should the password reach the database?",
		"header":   "Password",
		"options": []any{
			map[string]any{"label": "Secrets manager", "description": "Read it from the platform's secret store."},
			map[string]any{"label": "Connectors page", "description": "Type it into the masked field after the build."},
		},
	}

	cases := []struct {
		name      string
		questions []any
		refused   bool
	}{
		{
			name:      "the choice question alone is judged by its options",
			questions: []any{choiceQuestion},
		},
		{
			name: "a plain configuration ask beside the choice question",
			questions: []any{
				choiceQuestion,
				map[string]any{"question": "What is the hostname of the analytics database?", "header": "Host", "placeholder": "db.internal"},
			},
		},
		{
			name: "the choice question padding a secret ask",
			questions: []any{
				choiceQuestion,
				map[string]any{"question": "What is the database password?", "header": "Secret", "placeholder": "database password"},
			},
			refused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "analytics_db",
				"questions":      tc.questions,
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if !tc.refused {
				if result.Status == tool.StatusError {
					t.Fatalf("refused: %q", result.Message)
				}
				if !reached {
					t.Error("the form did not reach the user")
				}
				return
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error — the secret ask rode in on a choice question", result.Status)
			}
			for _, want := range []string{"password", "analytics_db", samRequiredPlaceholder, "masked"} {
				if !strings.Contains(result.Message, want) {
					t.Errorf("message = %q, missing %q", result.Message, want)
				}
			}
			if reached {
				t.Error("the form reached the user; the guard must short-circuit before WaitForUserInput")
			}
		})
	}
}

func TestAskUserQuestionSecretGuardEnumeratesTheManifestWithoutAComponentName(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, `
name: reporting-stack
components:
  - filename: pg_reporting.json
    type: connector
    name: analytics_db
    origin: session
`, SAMBuildManifestMIMEType)
	saveTestArtifact(t.Context(), svc, toolCtx, "pg_reporting.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"questions": []any{
			map[string]any{"question": "What is the database password?", "header": "Password", "placeholder": "database password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — omitting component_name skipped the guard", result.Status)
	}
	blamed := fmt.Sprintf("ask_user_question was not used: %q on component %q", "password", "analytics_db")
	if !strings.HasPrefix(result.Message, blamed) {
		t.Errorf("message = %q, expected it to name the matched component rather than the empty argument", result.Message)
	}
	if !strings.Contains(result.Message, "masked") {
		t.Errorf("message = %q, missing %q", result.Message, "masked")
	}
	if reached {
		t.Error("the form reached the user")
	}
}

func TestAskUserQuestionSecretGuardIgnoresAnotherComponentsPlaceholderedSecret(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, `
name: reporting-stack
components:
  - filename: analytics_db.json
    type: connector
    name: analytics_db
    origin: session
  - filename: staging_db.json
    type: connector
    name: staging_db
    origin: session
`, SAMBuildManifestMIMEType)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBFullyConfigured, SAMConnectorConfigMIMEType)
	saveTestArtifact(t.Context(), svc, toolCtx, "staging_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the password for the reporting user?", "header": "Password", "placeholder": "database password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("a form for a fully configured component was refused on another component's "+
			"outstanding secret: %q", result.Message)
	}
	if !reached {
		t.Error("the form did not reach the user")
	}
}

func TestAskUserQuestionSecretGuardFailsOpenWithNeitherComponentNameNorManifest(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"questions": []any{
			map[string]any{"question": "What is the database password?", "header": "Password", "placeholder": "database password"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("refused with no component named and no manifest to enumerate: %q", result.Message)
	}
	if !reached {
		t.Error("the form did not reach the user")
	}
}

func TestAskUserQuestionFieldMatchIsWholeWords(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		mimeType    string
		question    string
		placeholder string
	}{
		{
			name: "a tokenizer question against a placeholdered token",
			config: `{
  "name": "WeatherProxy",
  "agentUrl": "https://weather.example.com",
  "agentAuthConfig": {"type": "bearer", "token": "<<__SAM_REQUIRED__>>"}
}`,
			mimeType:    SAMA2AAgentConfigMIMEType,
			question:    "Which tokenizer should the proxy use?",
			placeholder: "cl100k_base",
		},
		{
			name:        "a stored-passwords question against a placeholdered password",
			config:      analyticsDBWithSecret,
			mimeType:    SAMConnectorConfigMIMEType,
			question:    "Which passwords are already stored in your vault?",
			placeholder: "list them by name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "component.json", tc.config, tc.mimeType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "component",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Config", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("%s was refused on a substring match: %q", tc.name, result.Message)
			}
			if !reached {
				t.Error("the form did not reach the user")
			}
		})
	}
}

func TestAskUserQuestionRefusesSecretNamedOnlyByQuestionText(t *testing.T) {
	svc := newMockArtifactService()
	reached := false
	toolCtx := askCtxWithArtifacts(svc, &reached)
	saveTestArtifact(t.Context(), svc, toolCtx, "analytics_db.json", analyticsDBWithSecret, SAMConnectorConfigMIMEType)

	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "analytics_db",
		"questions": []any{
			map[string]any{"question": "What is the password for the reporting user?", "header": "Auth", "placeholder": "type it here"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — the question text names the secret", result.Status)
	}
	if reached {
		t.Error("the form reached the user")
	}
}

const openAPIConnectorAwaitingKey = `{
  "name": "billing_api",
  "type": "api",
  "subtype": "openapi",
  "values": {
    "specification_url": "https://billing.example.com/openapi.json",
    "base_url": "https://billing.example.com",
    "auth_type": "apikey",
    "auth_apikey_location": "header",
    "auth_apikey_name": "X-API-Key",
    "auth_apikey_value": "<<__SAM_REQUIRED__>>"
  }
}`

const smtpConnectorAwaitingLoginPassword = `{
  "name": "billing_mailer",
  "type": "email",
  "subtype": "smtp",
  "values": {
    "smtp_host": "smtp.example.com",
    "smtp_port": 587,
    "smtp_auth_type": "login",
    "smtp_username_login": "billing@example.com",
    "smtp_password_login": "<<__SAM_REQUIRED__>>"
  }
}`

const bedrockKnowledgeBaseAwaitingCredentials = `{
  "name": "policy_kb",
  "type": "knowledge_base",
  "subtype": "bedrock",
  "values": {
    "kb_id": "<<__SAM_REQUIRED__>>",
    "region": "us-east-1",
    "auth_type": "access_key",
    "aws_account_id": "<<__SAM_REQUIRED__>>",
    "aws_access_key_id": "<<__SAM_REQUIRED__>>",
    "aws_secret_access_key": "<<__SAM_REQUIRED__>>"
  }
}`

func TestAskUserQuestionRefusesSecretsTheIdentifierTailMissed(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		question    string
		placeholder string
		blamed      string
	}{
		{
			name:        "the API key of an OpenAPI connector",
			config:      openAPIConnectorAwaitingKey,
			question:    "What is the API key for this service?",
			placeholder: "sk-live-...",
			blamed:      "auth_apikey_value",
		},
		{
			name:        "the SMTP password of a login-auth mailer",
			config:      smtpConnectorAwaitingLoginPassword,
			question:    "What SMTP password should I use?",
			placeholder: "the app password",
			blamed:      "smtp_password_login",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "component.json", tc.config, SAMConnectorConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "component",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Auth", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error — %s was admitted into an unmasked box", result.Status, tc.name)
			}
			if !strings.Contains(result.Message, tc.blamed) {
				t.Errorf("message = %q, expected it to name %q", result.Message, tc.blamed)
			}
			if reached {
				t.Error("the form reached the user")
			}
		})
	}
}

func TestAskUserQuestionAdmitsPlainQuestionsTheIdentifierTailRefused(t *testing.T) {
	cases := []struct {
		name        string
		config      string
		question    string
		placeholder string
	}{
		{
			name:        "knowledge base id beside a placeholdered aws_access_key_id",
			config:      bedrockKnowledgeBaseAwaitingCredentials,
			question:    "Which knowledge base ID should the agent query?",
			placeholder: "ABC123XYZ",
		},
		{
			name:        "aws account id beside a placeholdered aws_access_key_id",
			config:      bedrockKnowledgeBaseAwaitingCredentials,
			question:    "What is your AWS account ID?",
			placeholder: "123456789012",
		},
		{
			name:        "a timeout value beside a placeholdered auth_apikey_value",
			config:      openAPIConnectorAwaitingKey,
			question:    "What value should the request timeout be set to?",
			placeholder: "30s",
		},
		{
			name:        "a mailer login beside a placeholdered smtp_password_login",
			config:      smtpConnectorAwaitingLoginPassword,
			question:    "Which login should the mailer authenticate as?",
			placeholder: "billing@example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "component.json", tc.config, SAMConnectorConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "component",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Config", "placeholder": tc.placeholder},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("%s was refused: %q", tc.name, result.Message)
			}
			if !reached {
				t.Error("the form did not reach the user")
			}
		})
	}
}

const gmailMailerAwaitingRefreshToken = `{
  "name": "billing_mailer",
  "type": "email",
  "subtype": "smtp",
  "values": {
    "smtp_host": "smtp.gmail.com",
    "smtp_port": 587,
    "smtp_auth_type": "oauth_google",
    "oauth_google_client_id": "918273645.apps.googleusercontent.com",
    "oauth_google_client_secret": "GOCSPX-supplied-earlier",
    "oauth_google_refresh_token": "<<__SAM_REQUIRED__>>",
    "oauth_google_email": "billing@example.com"
  }
}`

const bedrockKnowledgeBaseAwaitingSecretKeyOnly = `{
  "name": "policy_kb",
  "type": "knowledge_base",
  "subtype": "bedrock",
  "values": {
    "kb_id": "ABC123XYZ",
    "region": "us-east-1",
    "auth_type": "access_key",
    "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE",
    "aws_secret_access_key": "<<__SAM_REQUIRED__>>"
  }
}`

func TestAskUserQuestionRefusesSecretAskedForWithoutItsLeadingQualifier(t *testing.T) {
	cases := []struct {
		name     string
		config   string
		question string
		blamed   string
	}{
		{
			name:     "the Google refresh token of an OAuth mailer",
			config:   gmailMailerAwaitingRefreshToken,
			question: "What refresh token did the Google consent flow return?",
			blamed:   "oauth_google_refresh_token",
		},
		{
			name:     "the AWS secret access key of a Bedrock knowledge base",
			config:   bedrockKnowledgeBaseAwaitingSecretKeyOnly,
			question: "What is the secret access key for that IAM user?",
			blamed:   "aws_secret_access_key",
		},
		{
			name:     "the AWS access key ID of a Bedrock knowledge base",
			config:   bedrockKnowledgeBaseAwaitingCredentials,
			question: "What access key ID should the agent use?",
			blamed:   "aws_access_key_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			reached := false
			toolCtx := askCtxWithArtifacts(svc, &reached)
			saveTestArtifact(t.Context(), svc, toolCtx, "component.json", tc.config, SAMConnectorConfigMIMEType)

			ask := &AskUserQuestionTool{}
			result, err := ask.Execute(t.Context(), map[string]any{
				"component_name": "component",
				"questions": []any{
					map[string]any{"question": tc.question, "header": "Auth", "placeholder": "type it here"},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error — %s was admitted into an unmasked box", result.Status, tc.name)
			}
			if !strings.Contains(result.Message, tc.blamed) {
				t.Errorf("message = %q, expected it to name %q", result.Message, tc.blamed)
			}
			if reached {
				t.Error("the form reached the user")
			}
		})
	}
}

func TestLabelPhrasesShortenFromBothEnds(t *testing.T) {
	cases := []struct {
		label string
		want  []string
	}{
		{"Password", []string{"password"}},
		{"Bot Token", []string{"bot token"}},
		{"Google Refresh Token", []string{"google refresh token", "refresh token"}},
		{"AWS Secret Access Key", []string{"aws secret access key", "secret access key", "access key"}},
		{"AWS Access Key ID", []string{"aws access key id", "aws access key", "access key"}},
		{"API Key Value", []string{"api key value", "api key"}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := labelPhrases(tc.label)
			if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(tc.want))) {
				t.Errorf("labelPhrases(%q) = %q, want %q", tc.label, got, tc.want)
			}
			for _, phrase := range got {
				if !strings.Contains(phrase, " ") && phrase != normaliseWords(tc.label) {
					t.Errorf("labelPhrases(%q) yields the one-word phrase %q; only the whole label may be one word",
						tc.label, phrase)
				}
			}
		})
	}
}

func TestPlaceholderedSecretFieldsAreMarkedByTheWholeValue(t *testing.T) {
	config := []byte(`{
  "name": "billing_api",
  "values": {
    "password": "<<__SAM_REQUIRED__>>",
    "api_key": "sk-live-<<__SAM_REQUIRED__>>-suffix",
    "auth_http_bearer_token": "Bearer <<__SAM_REQUIRED__>>"
  }
}`)

	got := placeholderedSecretFieldNames(config)
	if !slices.Equal(got, []string{"password"}) {
		t.Errorf("placeholderedSecretFieldNames = %q, want only [password] — a secret whose value merely "+
			"embeds the marking is a value the platform would deploy as typed, not an unfilled field", got)
	}
}

func TestSecretFieldPhrasesNeverShortenToOneWord(t *testing.T) {
	for name, labels := range secretFieldLabelsLower {
		allowed := []string{normaliseWords(name)}
		for _, label := range labels {
			allowed = append(allowed, normaliseWords(label))
		}
		for _, phrase := range fieldPhrases(name) {
			if strings.Contains(phrase, " ") || slices.Contains(allowed, phrase) {
				continue
			}
			t.Errorf("secret field %q yields the one-word phrase %q, which neither its name nor any of "+
				"its labels %q is — every question using that word would be refused", name, phrase, labels)
		}
	}
}
