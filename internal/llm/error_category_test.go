// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestErrorCategoryFromRealProviderFailures(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want string
	}{
		{
			"openai rejects an incorrect api key",
			&Error{
				Message:    "Incorrect API key provided: sk-***. You can find your API key at https://platform.openai.com/account/api-keys.",
				StatusCode: 401,
				Type:       "invalid_request_error",
				Code:       "invalid_api_key",
				Provider:   "openai",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"anthropic rejects the x-api-key header",
			&Error{
				Message:    "invalid x-api-key",
				StatusCode: 401,
				Type:       "authentication_error",
				Provider:   "anthropic",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"bedrock rejects the signing credentials",
			&Error{
				Message:    "The security token included in the request is invalid.",
				StatusCode: 403,
				Type:       "UnrecognizedClientException",
				Provider:   "bedrock",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"vertex denies the predict permission",
			&Error{
				Message:    "Permission 'aiplatform.endpoints.predict' denied on resource '//aiplatform.googleapis.com/projects/acme/locations/us-east1/publishers/google/models/gemini-2.5-pro'.",
				StatusCode: 403,
				Provider:   "vertex",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"a transport-level credential rejection carries no status",
			&Error{
				Message:  "failed to execute HTTP request to provider API: unauthorized: invalid credentials",
				Provider: "ollama",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"openai reports a tokens-per-minute rate limit",
			&Error{
				Message:    "Rate limit reached for gpt-4o in organization org-abc123 on tokens per min (TPM): Limit 30000, Used 29000, Requested 2000.",
				StatusCode: 429,
				Type:       "requests",
				Code:       "rate_limit_exceeded",
				Provider:   "openai",
			},
			ErrorCategoryRateLimit,
		},
		{
			"anthropic reports a per-minute input token limit",
			&Error{
				Message:    "Number of request tokens has exceeded your per-minute token limit. Please reduce the rate at which you are sending requests.",
				StatusCode: 429,
				Type:       "rate_limit_error",
				Provider:   "anthropic",
			},
			ErrorCategoryRateLimit,
		},
		{
			"bedrock throttles the account",
			&Error{
				Message:    "Too many requests, please wait before trying again.",
				StatusCode: 429,
				Type:       "ThrottlingException",
				Provider:   "bedrock",
			},
			ErrorCategoryRateLimit,
		},
		{
			"a project quota is exhausted without a 429",
			&Error{
				Message:    "Quota exceeded for aiplatform.googleapis.com/online_prediction_requests_per_base_model",
				StatusCode: 500,
				Provider:   "vertex",
			},
			ErrorCategoryRateLimit,
		},
		{
			"an upstream gateway times the request out",
			&Error{
				Message:    "<html><head><title>504 Gateway Time-out</title></head></html>",
				StatusCode: 504,
				Provider:   "bedrock",
			},
			ErrorCategoryTimeout,
		},
		{
			"a provider answers 408",
			&Error{
				Message:    "Request Timeout",
				StatusCode: 408,
				Provider:   "azure",
			},
			ErrorCategoryTimeout,
		},
		{
			"the client's own request deadline fires",
			&Error{
				Message:  "request timed out (default is 30 seconds). You can increase it by setting the default_request_timeout_in_seconds in the network_config",
				Provider: "openai",
			},
			ErrorCategoryTimeout,
		},
		{
			"the call is abandoned on a context deadline",
			&Error{
				Message:  "failed to execute HTTP request to provider API: context deadline exceeded",
				Provider: "anthropic",
			},
			ErrorCategoryTimeout,
		},
		{
			"a socket read times out",
			&Error{
				Message:  "network error occurred while connecting to provider API: dial tcp 10.0.0.4:443: i/o timeout",
				Provider: "vllm",
			},
			ErrorCategoryTimeout,
		},
		{
			"openai reports the conversation past the context window",
			&Error{
				Message:    "This model's maximum context length is 128000 tokens. However, your messages resulted in 131204 tokens. Please reduce the length of the messages.",
				StatusCode: 400,
				Type:       "invalid_request_error",
				Code:       "context_length_exceeded",
				Provider:   "openai",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"anthropic reports the prompt past the context window",
			&Error{
				Message:    "prompt is too long: 213456 tokens > 200000 maximum",
				StatusCode: 400,
				Type:       "invalid_request_error",
				Provider:   "anthropic",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"anthropic reports input plus max_tokens past the context window",
			&Error{
				Message:    "input length and `max_tokens` exceed context limit: 187254 + 20000 > 200000, decrease input length or `max_tokens` and try again",
				StatusCode: 400,
				Type:       "invalid_request_error",
				Provider:   "anthropic",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"bedrock's token throttle relayed as a 400",
			&Error{
				Message:    "Too many tokens, please wait before trying again.",
				StatusCode: 400,
				Provider:   "bedrock",
			},
			ErrorCategoryRateLimit,
		},
		{
			"anthropic's per-minute token limit relayed as a 400",
			&Error{
				Message:    "Number of request tokens has exceeded your per-minute token limit. Please reduce the rate at which you are sending requests.",
				StatusCode: 400,
				Provider:   "anthropic",
			},
			ErrorCategoryRateLimit,
		},
		{
			"a gateway reports too many tokens per minute as a 400",
			&Error{
				Message:    "Too many tokens per minute",
				StatusCode: 400,
				Provider:   "bedrock",
			},
			ErrorCategoryRateLimit,
		},
		{
			"a gateway reports a per-minute token limit as a 400",
			&Error{
				Message:    "token limit per minute exceeded",
				StatusCode: 400,
				Provider:   "openai",
			},
			ErrorCategoryRateLimit,
		},
		{
			"openai's tokens-per-minute rate limit relayed as a 400",
			&Error{
				Message:    "Rate limit reached for gpt-4o in organization org-abc123 on tokens per min (TPM): Limit 30000, Used 29000, Requested 2000. Please try again in 2s.",
				StatusCode: 400,
				Provider:   "openai",
			},
			ErrorCategoryRateLimit,
		},
		{
			"an explicit context_length_exceeded code outranks rate wording",
			&Error{
				Message:    "Rate limit reached: too many tokens per minute",
				StatusCode: 400,
				Code:       "context_length_exceeded",
				Provider:   "openai",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"bedrock reports the input past the context window",
			&Error{
				Message:    "Input is too long for requested model.",
				StatusCode: 400,
				Type:       "ValidationException",
				Provider:   "bedrock",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"cohere reports too many tokens without any rate wording",
			&Error{
				Message:    "too many tokens: total number of tokens in the prompt cannot exceed 4081 - received 5000. Try using a shorter prompt",
				StatusCode: 400,
				Provider:   "cohere",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"a provider reports its input token limit exceeded",
			&Error{
				Message:    "input token limit exceeded",
				StatusCode: 400,
				Provider:   "openai",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"a provider words an overflow with usage-limit phrasing",
			&Error{
				Message:    "prompt is too long: usage limit exceeded for context",
				StatusCode: 400,
				Provider:   "anthropic",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"a provider reports too many input tokens",
			&Error{
				Message:    "Request has too many input tokens",
				StatusCode: 400,
				Provider:   "bedrock",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"google reports the input past the context window",
			&Error{
				Message:    "Your input exceeds the context window of this model. Please adjust your input and try again.",
				StatusCode: 400,
				Provider:   "gemini",
			},
			ErrorCategoryContextOverflow,
		},
		{
			"azure filters the prompt on its content policy",
			&Error{
				Message:    "The response was filtered due to the prompt triggering Azure OpenAI's content management policy. Please modify your prompt and retry.",
				StatusCode: 400,
				Code:       "content_filter",
				Provider:   "azure",
			},
			ErrorCategoryContentFiltered,
		},
		{
			"openai rejects the request on its safety system",
			&Error{
				Message:    "Your request was rejected as a result of our safety system.",
				StatusCode: 400,
				Type:       "invalid_request_error",
				Code:       "content_policy_violation",
				Provider:   "openai",
			},
			ErrorCategoryContentFiltered,
		},
		{
			"a bedrock guardrail blocks the request",
			&Error{
				Message:    "Request was blocked by the configured guardrail.",
				StatusCode: 400,
				Type:       "ValidationException",
				Provider:   "bedrock",
			},
			ErrorCategoryContentFiltered,
		},
		{
			"a provider returns an unexplained server error",
			&Error{
				Message:    "Internal server error",
				StatusCode: 500,
				Provider:   "openai",
			},
			ErrorCategoryUnknown,
		},
		{
			"vertex's service account fails oauth token exchange",
			&Error{
				Message: "litellm.InternalServerError: Vertex_aiException InternalServerError - " +
					"('invalid_grant: Invalid grant: account not found', " +
					"{'error': 'invalid_grant', 'error_description': 'Invalid grant: account not found'})",
				StatusCode: 500,
				Provider:   "vertex",
			},
			ErrorCategoryAuthFailure,
		},
		{
			"the model name is wrong",
			&Error{
				Message:    "The model `gpt-4o-mini-2099` does not exist or you do not have access to it.",
				StatusCode: 404,
				Type:       "invalid_request_error",
				Provider:   "openai",
			},
			ErrorCategoryUnknown,
		},
		{
			"the provider host refuses the connection",
			&Error{
				Message:  "network error occurred while connecting to provider API (DNS lookup, connection refused, etc.): dial tcp 127.0.0.1:11434: connect: connection refused",
				Provider: "ollama",
			},
			ErrorCategoryUnknown,
		},
		{
			"a nil error is not a classification",
			nil,
			ErrorCategoryUnknown,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.err.Category(); got != c.want {
				t.Errorf("Category() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorCategoriesMatchTheCatalog(t *testing.T) {
	mine := []string{
		ErrorCategoryAuthFailure,
		ErrorCategoryRateLimit,
		ErrorCategoryTimeout,
		ErrorCategoryContextOverflow,
		ErrorCategoryContentFiltered,
		ErrorCategoryFreeLLMTermsRequired,
		ErrorCategoryFreeLLMPaused,
		ErrorCategoryAllowanceExhausted,
		ErrorCategoryUnknown,
		taxonomy.ErrorCategoryBudgetExhausted,
	}
	slices.Sort(mine)
	theirs := slices.Clone(taxonomy.ErrorCategories)
	slices.Sort(theirs)
	if !slices.Equal(mine, theirs) {
		t.Fatalf("categories = %v, catalog = %v", mine, theirs)
	}
}

func TestProviderLabelKeepsOnlyProvidersThisClientSupports(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		want     string
	}{
		{"a supported provider is reported by name", "anthropic", "anthropic"},
		{"casing and padding do not make a provider unknown", "  OpenAI ", "openai"},
		{"a self-hosted deployment is not named", "acme-internal-llm-gateway", ProviderOther},
		{"a customer's own bifrost provider is not named", "vllm-prod.acme.corp", ProviderOther},
		{"an unknown provider is reported as other", "some-new-vendor", ProviderOther},
		{"an unreported provider stays unreported", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ProviderLabel(c.provider)
			if got != c.want {
				t.Fatalf("ProviderLabel(%q) = %q, want %q", c.provider, got, c.want)
			}
		})
	}
}

func TestProviderLabelLeaksNothingOfAnUnknownName(t *testing.T) {
	const provider = "acme-payroll-llm.internal.acme.corp"
	got := ProviderLabel(provider)
	if got != ProviderOther {
		t.Fatalf("ProviderLabel(%q) = %q, want %q", provider, got, ProviderOther)
	}
	for _, fragment := range []string{"acme", "payroll", "internal", "corp"} {
		if strings.Contains(got, fragment) {
			t.Errorf("label %q leaks %q from the provider name", got, fragment)
		}
	}
}

func TestProviderLabelsMatchTheCatalog(t *testing.T) {
	mine := ProviderLabels()
	theirs := slices.Clone(taxonomy.ProviderLabels)
	slices.Sort(theirs)
	if !slices.Equal(mine, theirs) {
		t.Fatalf("labels = %v, catalog = %v", mine, theirs)
	}
}

func TestCategoryAndProviderAreAlwaysCatalogValues(t *testing.T) {
	errs := []*Error{
		nil,
		{},
		{Message: "boom", StatusCode: 503, Provider: "acme-internal"},
		{Message: "Rate limit reached", StatusCode: 429, Provider: "OPENAI"},
		{Message: "prompt is too long", StatusCode: 400, Provider: "anthropic"},
		{Message: "content filter triggered", StatusCode: 400, Provider: ""},
	}
	for _, e := range errs {
		if got := e.Category(); !slices.Contains(taxonomy.ErrorCategories, got) {
			t.Errorf("Category() = %q, not a catalog value", got)
		}
		if e == nil {
			continue
		}
		label := ProviderLabel(e.Provider)
		if label == "" {
			continue
		}
		if !slices.Contains(taxonomy.ProviderLabels, label) {
			t.Errorf("ProviderLabel(%q) = %q, not a catalog value", e.Provider, label)
		}
	}
}
