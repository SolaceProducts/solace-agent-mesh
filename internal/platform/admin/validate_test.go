// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateRequest_Valid(t *testing.T) {
	t.Parallel()
	req := CreateAgentRequest{
		Name:        "test-agent",
		Description: "A valid description that is long enough",
	}
	if err := validateRequest(&req); err != nil {
		t.Errorf("expected no error for valid request, got %v", err)
	}
}

func TestValidateRequest_Required(t *testing.T) {
	t.Parallel()
	req := CreateAgentRequest{}
	err := validateRequest(&req)
	if err == nil {
		t.Fatal("expected error for empty required fields")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "name is required") {
		t.Errorf("expected 'name is required' in message, got %q", ve.Message)
	}
	if !strings.Contains(ve.Message, "description is required") {
		t.Errorf("expected 'description is required' in message, got %q", ve.Message)
	}
}

func TestFormatFieldError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     any
		wantSub string
	}{
		{
			name:    "string min violation",
			req:     &CreateAgentRequest{Name: "ab", Description: "A valid description that is long enough"},
			wantSub: "name must be at least 3 characters",
		},
		{
			name:    "string max violation",
			req:     &CreateGatewayRequest{Name: "ok-name", Description: "A valid description that is long enough", Type: strings.Repeat("x", 51)},
			wantSub: "type must be at most 50 characters",
		},
		{
			name: "slice max violation",
			req: &CreateAgentRequest{
				Name:        "test-agent",
				Description: "A valid description that is long enough",
				Connectors:  []string{"a", "b", "c", "d", "e", "f"},
			},
			wantSub: "connectors must have at most 5 items",
		},
		{
			name: "oneof violation",
			req: &CreateAgentRequest{
				Name:        "test-agent",
				Description: "A valid description that is long enough",
				Type:        "invalid_type",
			},
			wantSub: "type must be one of: standard",
		},
		{
			name: "int min violation",
			req: &CreateRemoteAgentRequest{
				Name:                  "test-remote",
				Description:           "A valid description that is long enough",
				AgentURL:              "http://example.com",
				AgentCardLocation:     "well_known",
				RequestTimeoutSeconds: 5,
			},
			wantSub: "requestTimeoutSeconds must be at least 10",
		},
		{
			name: "int max violation",
			req: &CreateRemoteAgentRequest{
				Name:                  "test-remote",
				Description:           "A valid description that is long enough",
				AgentURL:              "http://example.com",
				AgentCardLocation:     "well_known",
				RequestTimeoutSeconds: 700,
			},
			wantSub: "requestTimeoutSeconds must be at most 600",
		},
		{
			name:    "name rejects control char",
			req:     &CreateAgentRequest{Name: "Agent\x01Name", Description: "A valid description that is long enough"},
			wantSub: "name contains unsafe characters",
		},
		{
			name: "name allows newline",
			req:  &CreateAgentRequest{Name: "Agent\nName", Description: "A valid description that is long enough"},
		},
		{
			name:    "safetext rejects control char",
			req:     &CreateAgentRequest{Name: "test-agent", Description: "A description with \x01 control char"},
			wantSub: "description contains unsafe characters",
		},
		{
			name: "safetext allows newline",
			req:  &CreateAgentRequest{Name: "test-agent", Description: "A description\nwith a newline inside"},
		},
		{
			name: "safetext allows tab",
			req:  &CreateAgentRequest{Name: "test-agent", Description: "A description\twith a tab character"},
		},
		{
			name: "name allows utf8",
			req:  &CreateAgentRequest{Name: "Ag\u00ebnt N\u00e0me", Description: "A valid description that is long enough"},
		},
		{
			name: "name allows punctuation",
			req:  &CreateAgentRequest{Name: "agent-name_v2.0!", Description: "A valid description that is long enough"},
		},
		{
			name:    "name rejects all-whitespace",
			req:     &CreateAgentRequest{Name: "     ", Description: "A valid description that is long enough"},
			wantSub: "name contains unsafe characters",
		},
		{
			name:    "name rejects single-tab",
			req:     &CreateAgentRequest{Name: "\t\t\t", Description: "A valid description that is long enough"},
			wantSub: "name contains unsafe characters",
		},
		{
			name:    "safetext rejects all-whitespace description",
			req:     &CreateAgentRequest{Name: "test-agent", Description: "          "},
			wantSub: "description contains unsafe characters",
		},
		{
			name: "name still allows internal spaces",
			req:  &CreateAgentRequest{Name: "Web Search", Description: "A valid description that is long enough"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequest(tt.req)
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if !strings.Contains(ve.Message, tt.wantSub) {
				t.Errorf("expected message to contain %q, got %q", tt.wantSub, ve.Message)
			}
		})
	}
}

func gatewayReqWithSlug(slug string) *CreateGatewayRequest {
	return &CreateGatewayRequest{
		Name:        "valid-name",
		Description: "A valid description that is long enough",
		Type:        "httpsse",
		Slug:        slug,
	}
}

func TestGatewaySlugValidator(t *testing.T) {
	t.Parallel()
	const slugRuleSub = "slug must be 3-63 lowercase characters"
	const slugReservedSub = "not be a reserved word"

	tests := []struct {
		name    string
		slug    string
		wantSub string
	}{
		{name: "empty slug ok", slug: "", wantSub: ""},

		{name: "too short", slug: "ab", wantSub: "slug must be at least 3 characters"},
		{name: "too long", slug: strings.Repeat("a", 64), wantSub: "slug must be at most 63 characters"},
		{name: "max-length boundary 63 chars", slug: strings.Repeat("a", 63), wantSub: ""},
		{name: "min-length boundary 3 chars all letters", slug: "abc", wantSub: ""},

		{name: "uppercase letters", slug: "MyMcp", wantSub: slugRuleSub},
		{name: "starts with digit", slug: "1abc", wantSub: slugRuleSub},
		{name: "starts with dash", slug: "-abc", wantSub: slugRuleSub},
		{name: "ends with dash", slug: "abc-", wantSub: slugRuleSub},
		{name: "contains underscore", slug: "my_slug", wantSub: slugRuleSub},
		{name: "contains space", slug: "my slug", wantSub: slugRuleSub},
		{name: "contains unicode", slug: "café-gw", wantSub: slugRuleSub},

		{name: "reserved oauth", slug: "oauth", wantSub: slugReservedSub},
		{name: "reserved api", slug: "api", wantSub: slugReservedSub},
		{name: "reserved well-known", slug: "well-known", wantSub: slugReservedSub},
		{name: "reserved gw rejected by min=3", slug: "gw", wantSub: "slug must be at least 3 characters"},
		{name: "reserved health", slug: "health", wantSub: slugReservedSub},
		{name: "reserved evals (eval reply gateway id)", slug: "evals", wantSub: slugReservedSub},

		{name: "mixed-case OAUTH rejected by regex", slug: "OAUTH", wantSub: slugRuleSub},
		{name: "mixed-case Api rejected by regex", slug: "Api", wantSub: slugRuleSub},

		{name: "oauth-server passes", slug: "oauth-server", wantSub: ""},
		{name: "api-key passes", slug: "api-key", wantSub: ""},
		{name: "well-known-issuer passes", slug: "well-known-issuer", wantSub: ""},
		{name: "gw-prod passes", slug: "gw-prod", wantSub: ""},
		{name: "healthcheck passes", slug: "healthcheck", wantSub: ""},

		{name: "my-mcp", slug: "my-mcp", wantSub: ""},
		{name: "my-mcp-gateway", slug: "my-mcp-gateway", wantSub: ""},
		{name: "a1b 3-char alnum ends digit", slug: "a1b", wantSub: ""},
		{name: "my-mcp-2 ends digit", slug: "my-mcp-2", wantSub: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequest(gatewayReqWithSlug(tt.slug))
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("expected no error for slug %q, got %v", tt.slug, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected validation error for slug %q", tt.slug)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if !strings.Contains(ve.Message, tt.wantSub) {
				t.Errorf("expected message to contain %q, got %q", tt.wantSub, ve.Message)
			}
		})
	}
}

func TestValidateRequest_MaxSkills(t *testing.T) {
	t.Parallel()
	skills := make([]SkillRequest, 21)
	for i := range skills {
		skills[i] = SkillRequest{Name: fmt.Sprintf("skill-%d", i)}
	}
	req := &CreateAgentRequest{
		Name:        "test-agent",
		Description: "A valid description that is long enough",
		Skills:      skills,
	}
	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected validation error for 21 skills")
	}
	ve := err.(*ValidationError)
	if !strings.Contains(ve.Message, "skills must have at most 20 items") {
		t.Errorf("expected skills limit message, got %q", ve.Message)
	}
}

func TestValidateRequest_MultipleErrors(t *testing.T) {
	t.Parallel()
	req := &CreateAgentRequest{
		Name:        "ab",
		Description: "short",
	}
	err := validateRequest(req)
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve := err.(*ValidationError)
	parts := strings.Split(ve.Message, "; ")
	if len(parts) < 2 {
		t.Errorf("expected multiple errors separated by ';', got %q", ve.Message)
	}
}

func TestCheckInstructionLength_AcceptsTerseAndLongInstructions(t *testing.T) {
	const minChars, maxChars = 1, 32_000

	terse := "You are a helpful SQL assistant."
	if msg := checkInstructionLength(terse, minChars, maxChars); msg != "" {
		t.Errorf("a %d-character instruction was rejected: %s", len(terse), msg)
	}

	long := strings.Repeat("a", 20_000)
	if msg := checkInstructionLength(long, minChars, maxChars); msg != "" {
		t.Errorf("a 20000-character instruction was rejected: %s", msg)
	}

	if msg := checkInstructionLength("", minChars, maxChars); msg != "" {
		t.Errorf("empty instruction should defer to the required-field check, got: %s", msg)
	}
}

func TestCheckInstructionLength_EnforcesConfiguredBounds(t *testing.T) {
	if msg := checkInstructionLength(strings.Repeat("a", 101), 1, 100); msg == "" {
		t.Error("over-limit instruction was accepted")
	} else if !strings.Contains(msg, "100") {
		t.Errorf("message must name the bound so a caller knows what to trim to, got: %s", msg)
	}
	if msg := checkInstructionLength(strings.Repeat("a", 100), 1, 100); msg != "" {
		t.Errorf("at-limit instruction rejected (bound must be inclusive): %s", msg)
	}
	if msg := checkInstructionLength("short", 50, 100); msg == "" {
		t.Error("under-minimum instruction was accepted")
	}
}

func TestCheckInstructionLength_CountsCharactersNotBytesOrUTF16(t *testing.T) {
	cjk := strings.Repeat("目", 100)
	if msg := checkInstructionLength(cjk, 1, 100); msg != "" {
		t.Errorf("CJK instruction within the character bound was rejected: %s", msg)
	}

	emoji := strings.Repeat("\U0001F600", 60)
	if msg := checkInstructionLength(emoji, 100, 32_000); msg == "" {
		t.Error("60 emoji must fall under a 100-character minimum; UTF-16 counting would wrongly pass it")
	}
}

func TestValidateAgentRequest_BatchesWithFieldErrors(t *testing.T) {
	req := &CreateAgentRequest{
		Name:         "ab",
		Description:  "also far too short",
		SystemPrompt: strings.Repeat("a", 50),
	}
	err := validateAgentRequest(req, req.SystemPrompt, 1, 10)
	if err == nil {
		t.Fatal("expected a validation error")
	}
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(verr.Details["systemPrompt"]) == 0 {
		t.Errorf("systemPrompt error missing from details: %v", verr.Details)
	}
	if len(verr.Details) < 2 {
		t.Errorf("expected the name error reported alongside systemPrompt, got %v", verr.Details)
	}
}

func TestValidateAgentRequest_AcceptsValidRequest(t *testing.T) {
	req := &CreateAgentRequest{
		Name:         "Valid Agent",
		Description:  "A description that is comfortably long enough to pass.",
		SystemPrompt: "You are a helpful assistant.",
	}
	if err := validateAgentRequest(req, req.SystemPrompt, 1, 32_000); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestCheckInstructionLength_BoundsAreInclusive(t *testing.T) {
	if msg := checkInstructionLength(strings.Repeat("a", 10), 10, 20); msg != "" {
		t.Errorf("exactly the minimum was rejected: %s", msg)
	}
	if msg := checkInstructionLength(strings.Repeat("a", 20), 10, 20); msg != "" {
		t.Errorf("exactly the maximum was rejected: %s", msg)
	}
	if msg := checkInstructionLength(strings.Repeat("a", 9), 10, 20); msg == "" {
		t.Error("one under the minimum was accepted")
	}
}

func TestValidateAgentRequest_MergesIntoMessageAndDetails(t *testing.T) {
	req := &CreateAgentRequest{
		Name:         "ab",
		Description:  "also far too short",
		SystemPrompt: strings.Repeat("a", 50),
	}
	err := validateAgentRequest(req, req.SystemPrompt, 1, 10)
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(verr.Message, "systemPrompt") {
		t.Errorf("Message must carry the instruction error, got: %s", verr.Message)
	}
	if !strings.Contains(verr.Message, ";") {
		t.Errorf("Message must keep the pre-existing field error alongside it, got: %s", verr.Message)
	}
}
