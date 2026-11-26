// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
)

var reUnionMember = regexp.MustCompile(`"([^"]+)"`)

func extractStringLiterals(t *testing.T, path, marker, terminator string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block := string(data)
	i := strings.Index(block, marker)
	if i < 0 {
		t.Fatalf("%q not found in %s", marker, path)
	}
	block = block[i+len(marker):]
	if end := strings.Index(block, terminator); end >= 0 {
		block = block[:end]
	}
	matches := reUnionMember.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		t.Fatalf("no members parsed after %q in %s", marker, path)
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

func frontendUnion(t *testing.T, typeName string) []string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "webui", "src", "common", "product-analytics", "types.ts")
	return extractStringLiterals(t, path, "export type "+typeName+" =", ";")
}

func frontendFormFields(t *testing.T) []string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "webui", "src", "common", "product-analytics", "formFields.ts")
	return extractStringLiterals(t, path, "export const PRODUCT_ANALYTICS_FORM_FIELDS = [", "] as const")
}

func frontendEventNames(t *testing.T) []string {
	t.Helper()
	return frontendUnion(t, "ProductAnalyticsEventName")
}

func TestFormVocabulariesMatchFrontendTypes(t *testing.T) {
	cases := []struct {
		typeName string
		goSet    []string
	}{
		{"ProductAnalyticsFormName", FormNames},
		{"ProductAnalyticsFormField", FormFields},
		{"ProductAnalyticsFormErrorType", FormErrorTypes},
	}
	for _, c := range cases {
		t.Run(c.typeName, func(t *testing.T) {
			var fe []string
			if c.typeName == "ProductAnalyticsFormField" {
				fe = frontendFormFields(t)
			} else {
				fe = frontendUnion(t, c.typeName)
			}
			slices.Sort(fe)
			goSorted := slices.Clone(c.goSet)
			slices.Sort(goSorted)
			if !slices.Equal(fe, goSorted) {
				t.Errorf("frontend %s = %v, Go set = %v; keep them in sync", c.typeName, fe, goSorted)
			}
		})
	}
}

func TestSuggestionLabelsMatchFrontendTypes(t *testing.T) {
	fe := frontendUnion(t, "ProductAnalyticsSuggestionLabel")
	slices.Sort(fe)
	goSorted := slices.Clone(SuggestionLabels)
	slices.Sort(goSorted)
	if !slices.Equal(fe, goSorted) {
		t.Errorf("frontend ProductAnalyticsSuggestionLabel = %v, Go SuggestionLabels = %v; keep them in sync", fe, goSorted)
	}
}

func TestActivationVocabulariesMatchFrontendTypes(t *testing.T) {
	for typeName, goSet := range map[string][]string{
		"ProductAnalyticsSetupPath":     SetupPaths,
		"ProductAnalyticsSignInFailure": SignInFailures,
	} {
		fe := frontendUnion(t, typeName)
		slices.Sort(fe)
		goSorted := slices.Clone(goSet)
		slices.Sort(goSorted)
		if !slices.Equal(fe, goSorted) {
			t.Errorf("frontend %s = %v, Go set = %v; keep them in sync", typeName, fe, goSorted)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test working dir")
		}
		dir = parent
	}
}

func TestCatalogMatchesFrontendTypes(t *testing.T) {
	fe := frontendEventNames(t)
	feSet := make(map[string]bool, len(fe))
	for _, name := range fe {
		feSet[name] = true
		if _, ok := webUICatalog[name]; !ok {
			t.Errorf("frontend event %q missing from the Go web UI catalog, add it to catalog.go", name)
		}
	}
	for name := range webUICatalog {
		if !feSet[name] {
			t.Errorf("Go web UI catalog event %q is not in the frontend types.ts, remove it or add it on the FE", name)
		}
	}
	if len(fe) != len(webUICatalog) {
		t.Errorf("web UI catalog size = %d, frontend defines %d, keep them in sync", len(webUICatalog), len(fe))
	}
}

func TestCLICatalogIsDisjointFromFrontend(t *testing.T) {
	for _, name := range frontendEventNames(t) {
		if _, ok := cliCatalog[name]; ok {
			t.Errorf("event %q is CLI-only but the frontend declares it; move it to webUICatalog or drop it from types.ts", name)
		}
	}
	for name := range cliCatalog {
		if !IsCatalogEvent(name) {
			t.Errorf("CLI event %q did not survive the catalog merge", name)
		}
	}
}

func TestServerCatalogIsDisjointFromFrontend(t *testing.T) {
	for _, name := range frontendEventNames(t) {
		if _, ok := serverCatalog[name]; ok {
			t.Errorf("event %q is server-only but the frontend declares it; move it to webUICatalog or drop it from types.ts", name)
		}
	}
	for name := range serverCatalog {
		if !IsCatalogEvent(name) {
			t.Errorf("server event %q did not survive the catalog merge", name)
		}
	}
}

func TestCatalogAllowlistPinned(t *testing.T) {
	expected := map[string]map[string]string{
		"app:launched": {},
		"model:configured": {
			"provider":      "enum",
			"model":         "enum",
			"modelType":     "enum",
			"submittedFrom": "enum",
			"modelKind":     "enum",
		},
		"model:used":                       {"provider": "enum", "model": "enum", "modelType": "enum"},
		"agent:creation_started":           {},
		"agent:created":                    {"agentType": "enum", "toolsetCount": "count", "submittedFrom": "enum"},
		"agent:deleted":                    {"agentType": "enum"},
		"agent:deployed":                   {},
		"workflow:created":                 {},
		"workflow:deployed":                {},
		"chat:first_response":              {"traceId": "uuid"},
		"builder:opened":                   {},
		"builder:completed":                {},
		"connector:created":                {"category": "enum"},
		"connector:deleted":                {},
		"gateway:created":                  {"gatewayType": "enum"},
		"gateway:deleted":                  {},
		"toolset:created":                  {},
		"tool:used":                        {"category": "enum", "connectorCategory": "enum", "builtinCategory": "enum", "runOrigin": "enum"},
		"agent:peer_delegated":             {"runOrigin": "enum"},
		"task:submit_rejected":             {"rejectReason": "enum", "submittedFrom": "enum", "entrypointType": "enum"},
		"form:validation_error":            {"form": "enum", "field": "enum", "errorType": "enum"},
		"error:displayed":                  {"errorCategory": "enum", "errorSource": "enum", "page": "routePattern", "traceId": "uuid"},
		"page_view":                        {},
		"chat:suggestion_used":             {"suggestionLabel": "enum"},
		"activation:sign_in_clicked":       {},
		"activation:sign_in_failed":        {"signInFailure": "enum"},
		"activation:signed_in":             {},
		"activation:terms_shown":           {"alreadyAgreed": "bool"},
		"activation:terms_error":           {},
		"activation:replace_confirmed":     {},
		"activation:setup_chosen":          {"setupPath": "enum"},
		"activation:accepted":              {"setupPath": "enum"},
		"activation:accept_failed":         {"setupPath": "enum"},
		"free_model:credits_low":           {},
		"free_model:credits_out":           {},
		"free_model:sign_in_again_clicked": {},
		"cli:command_run":                  {"command": "text(64)", "outcome": "enum"},
		"agent:run_completed": {
			"outcome": "enum", "agentType": "enum", "errorCategory": "enum", "provider": "enum",
			"submittedFrom": "enum",
			"model":         "enum",
			"toolModels":    "enumSet(16)",
			"modelType":     "enum",
		},
		"entrypoint:used": {"entrypointType": "enum", "outcome": "enum"},
		"eval:created":    {"targetType": "enum"},
		"eval:run": {
			"outcome": "enum", "errorCategory": "enum", "targetType": "enum",
			"evaluatorTypes": "enumSet(8)", "trialsPerExample": "enum", "workflowTrialFailureKinds": "enumSet(8)",
			"decisionEvaluatorCount": "count",
			"decisionCasesDecided":   "count", "decisionCasesEscalated": "count", "decisionCasesErrored": "count",
			"decisionEscalatedLowConfidence": "count", "decisionEscalatedNoConfidence": "count",
			"decisionEscalatedProviderError": "count", "decisionEscalatedModelLookupFailed": "count",
		},
		"evaluator:created": {
			"evaluatorType": "enum", "questionType": "enum", "escalation": "enum", "submittedFrom": "enum",
		},
		"consent:changed": {"consentPrevious": "enum", "consentNew": "enum"},
		"install:inventory": {
			"agentCount": "count", "workflowCount": "count", "connectorCount": "count",
			"entrypointCount": "count", "toolsetCount": "count", "configuredModelCount": "count",
			"agentsWithCustomToolsets": "count", "workflowsWithCustomToolsets": "count",
			"connectorCategories": "enumSet(32)", "entrypointTypes": "enumSet(32)",
			"deployedAgentCount": "count", "deployedWorkflowCount": "count",
			"deployedAgentsWithCustomToolsets": "count", "deployedWorkflowsWithCustomToolsets": "count",
			"builtinAgentCount": "count", "deployedBuiltinAgentCount": "count",
		},
	}
	if len(expected) != len(catalog) {
		t.Fatalf("pinned %d events, catalog has %d, update the pin intentionally", len(expected), len(catalog))
	}
	for name, fields := range expected {
		extra, ok := catalog[name]
		if !ok {
			t.Errorf("event %q missing from catalog", name)
			continue
		}
		for field, shape := range fields {
			spec, has := extra[field]
			if !has {
				t.Errorf("%s: pinned field %q missing from allow-list", name, field)
				continue
			}
			if spec.shape != shape {
				t.Errorf("%s.%s: shape = %q, pinned %q; widening a field is a deliberate edit", name, field, spec.shape, shape)
			}
		}
		for field := range extra {
			if _, want := fields[field]; !want {
				t.Errorf("%s: unexpected field %q in allow-list, update the pin intentionally", name, field)
			}
		}
	}
}

func TestUniversalFieldShapesPinned(t *testing.T) {
	cases := []struct {
		surface string
		want    map[string]string
	}{
		{SurfaceUI, map[string]string{"path": "routePattern"}},
		{SurfaceCLI, map[string]string{}},
		{SurfaceServer, map[string]string{"traceId": "uuid"}},
	}
	for _, c := range cases {
		_, universal := catalogFor(c.surface)
		for field, shape := range c.want {
			spec, ok := universal[field]
			if !ok {
				t.Errorf("%s: pinned universal field %q is missing", c.surface, field)
				continue
			}
			if spec.shape != shape {
				t.Errorf("%s.%s: shape = %q, pinned %q", c.surface, field, spec.shape, shape)
			}
		}
		for field := range universal {
			if _, want := c.want[field]; !want {
				t.Errorf("%s: unexpected universal field %q", c.surface, field)
			}
		}
	}
}

func TestEveryCatalogShapeIsNamed(t *testing.T) {
	for _, surface := range surfaces {
		events, universal := catalogFor(surface)
		for name, fields := range events {
			for field, spec := range fields {
				if spec.shape == "" {
					t.Errorf("%s.%s: shape is unnamed, so the catalog pin cannot hold it", name, field)
				}
			}
		}
		for field, spec := range universal {
			if spec.shape == "" {
				t.Errorf("%s universal field %s: shape is unnamed", surface, field)
			}
		}
	}
}

func TestCatalogEnumValuesPinned(t *testing.T) {
	cases := []struct {
		surface      string
		event, field string
		valid        []string
		invalid      string
	}{
		{SurfaceServer, "model:configured", "modelType", []string{"hosted", "local", "free_model"}, "cloud"},
		{SurfaceServer, "model:configured", "modelKind", []string{"chat", "decision"}, "embedding"},
		{SurfaceServer, "evaluator:created", "evaluatorType", []string{"llm_judge", "decision"}, "acme-refund-policy-judge"},
		{SurfaceServer, "evaluator:created", "questionType", []string{"boolean", "choice", "score"}, "Did the agent quote the Acme refund policy?"},
		{SurfaceServer, "evaluator:created", "escalation", []string{"none", "on_error", "on_low_confidence"}, "escalate to acme-legal-judge"},
		{SurfaceServer, "evaluator:created", "submittedFrom", []string{"ui", "cli", "api"}, "acme-provisioner"},
		{SurfaceServer, "eval:run", "trialsPerExample", []string{"single", "multiple"}, "3"},
		{SurfaceServer, "gateway:created", "gatewayType", []string{"rest", "webhook", "slack", "teams", "event_mesh", "email", "whatsapp", "google_chat", "mcp"}, "sms"},
		{SurfaceServer, "tool:used", "category", ToolCategories, "external"},
		{SurfaceUI, "error:displayed", "errorCategory", []string{"auth_failure", "rate_limit", "timeout", "context_overflow", "content_filtered", "unknown"}, "meltdown"},
		{SurfaceUI, "error:displayed", "errorSource", ErrorSources, "403 Forbidden: agent:acme-payroll:invoke required"},
		{SurfaceCLI, "cli:command_run", "outcome", []string{"success", "error"}, "cancelled"},
		{SurfaceServer, "agent:run_completed", "outcome", []string{"success", "error", "cancelled", "timeout"}, "abandoned"},
		{SurfaceServer, "agent:run_completed", "agentType", []string{"builtin", "user_created", "workflow"}, "internal"},
		{SurfaceServer, "agent:run_completed", "submittedFrom", []string{"ui", "cli", "api"}, "browser"},
		{SurfaceServer, "agent:run_completed", "errorCategory", []string{"auth_failure", "rate_limit", "timeout", "context_overflow", "content_filtered", "unknown"}, "meltdown"},
		{SurfaceServer, "agent:run_completed", "provider", []string{"openai", "anthropic", "bedrock", "vertex", "azure", "ollama", "other"}, "acme-payroll-llm.internal"},
		{SurfaceServer, "model:configured", "provider", []string{"openai", "anthropic", "bedrock", "vertex", "azure", "ollama", "other"}, "acme-internal-vllm-cluster"},
		{SurfaceServer, "model:configured", "model", modelfamily.Families, "acme-corp-ft-2024-support-bot"},
		{SurfaceServer, "connector:created", "category", ConnectorCategories, "acme_customer_ledger"},
		{SurfaceServer, "tool:used", "connectorCategory", ConnectorCategories, "acme_customer_ledger"},
		{SurfaceServer, "tool:used", "runOrigin", RunOrigins, "webhook"},
		{SurfaceServer, "agent:peer_delegated", "runOrigin", RunOrigins, "webhook"},
		{SurfaceUI, "form:validation_error", "form", FormNames, "CUSTOMER=Acme Financial Holdings; DEAL=Project Falcon"},
		{SurfaceUI, "form:validation_error", "field", FormFields, "/home/jane/secrets/prod.env"},
		{SurfaceUI, "form:validation_error", "errorType", FormErrorTypes, "sk-ant-api03-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"},
		{SurfaceUI, "chat:suggestion_used", "suggestionLabel", SuggestionLabels, "help me plan a birthday party"},
		{SurfaceUI, "activation:setup_chosen", "setupPath", SetupPaths, "ada@example.com"},
		{SurfaceUI, "activation:sign_in_failed", "signInFailure", SignInFailures, "Solace ID did not complete the sign-in for ada@example.com"},
	}
	for _, c := range cases {
		for _, v := range c.valid {
			out, _ := scrub(c.surface, RawEvent{Name: c.event, Properties: map[string]any{c.field: v}})
			if out[c.field] != v {
				t.Errorf("%s.%s: value %q should be accepted", c.event, c.field, v)
			}
		}
		out, _ := scrub(c.surface, RawEvent{Name: c.event, Properties: map[string]any{c.field: c.invalid}})
		if _, ok := out[c.field]; ok {
			t.Errorf("%s.%s: value %q should be rejected", c.event, c.field, c.invalid)
		}
	}
}

func TestScrubDropsUnknownEvent(t *testing.T) {
	if _, ok := scrub(SurfaceUI, RawEvent{Name: "totally:made_up"}); ok {
		t.Fatal("unknown event should be dropped")
	}
}

func TestScrubStripsDisallowedField(t *testing.T) {
	out, ok := scrub(SurfaceServer, RawEvent{
		Name:       "model:configured",
		Properties: map[string]any{"provider": "openai", "apiKey": "sk-secret"},
	})
	if !ok {
		t.Fatal("known event should be kept")
	}
	if _, leaked := out["apiKey"]; leaked {
		t.Error("apiKey is not in the allow-list and must be stripped")
	}
	if out["provider"] != "openai" {
		t.Errorf("provider = %v, want openai", out["provider"])
	}
}

func TestScrubRejectsBadShape(t *testing.T) {
	out, _ := scrub(SurfaceUI, RawEvent{
		Name:       "model:configured",
		Properties: map[string]any{"modelType": "bogus"},
	})
	if _, ok := out["modelType"]; ok {
		t.Error("invalid enum value must be dropped")
	}
	out, _ = scrub(SurfaceCLI, RawEvent{
		Name:       EventCLICommandRun,
		Properties: map[string]any{PropCommand: strings.Repeat("x", 200)},
	})
	if _, ok := out[PropCommand]; ok {
		t.Error("over-length string must be dropped")
	}
}

func TestScrubKeepsTheToolsetCountExact(t *testing.T) {
	for _, c := range []struct {
		sent any
		want int64
	}{
		{float64(0), 0},
		{float64(1), 1},
		{float64(3), 3},
		{7, 7},
		{int64(4096), 4096},
	} {
		out, _ := scrub(SurfaceServer, RawEvent{
			Name:       "agent:created",
			Properties: map[string]any{"toolsetCount": c.sent},
		})
		if out["toolsetCount"] != c.want {
			t.Errorf("toolsetCount %v scrubbed to %#v, want %d", c.sent, out["toolsetCount"], c.want)
		}
	}
	for _, bad := range []any{float64(-1), float64(maxReportableCount + 1)} {
		out, _ := scrub(SurfaceServer, RawEvent{
			Name:       "agent:created",
			Properties: map[string]any{"toolsetCount": bad},
		})
		if got, ok := out["toolsetCount"]; ok {
			t.Errorf("toolsetCount %v was kept as %v, want it dropped", bad, got)
		}
	}
}

func TestScrubStripsClientInterface(t *testing.T) {
	for _, v := range []string{"cli", "api", "carrier-pigeon"} {
		out, _ := scrub(SurfaceUI, RawEvent{Name: "page_view", Properties: map[string]any{"interface": v}})
		if _, ok := out["interface"]; ok {
			t.Errorf("client interface %q must be stripped by scrub (interface is enrich-stamped)", v)
		}
	}
}

func TestTimestampPropertyIsStripped(t *testing.T) {
	for _, surface := range []struct {
		name  string
		event EventName
	}{
		{SurfaceUI, "page_view"},
		{SurfaceCLI, EventCLICommandRun},
	} {
		out, ok := scrub(surface.name, RawEvent{
			Name:       surface.event,
			Properties: map[string]any{"timestamp": "2026-08-06T00:00:00.000Z"},
		})
		if !ok {
			t.Fatalf("%s: known event should be kept", surface.name)
		}
		if got, leaked := out["timestamp"]; leaked {
			t.Errorf("%s: timestamp belongs on the envelope, not in properties, got %v", surface.name, got)
		}
	}
}

func TestTraceIDFieldRejectsNonUUID(t *testing.T) {
	const id = "123e4567-e89b-12d3-a456-426614174000"
	out, _ := scrub(SurfaceUI, RawEvent{Name: EventChatFirstResponse, Properties: map[string]any{"traceId": id}})
	if out["traceId"] != id {
		t.Errorf("valid UUID traceId should survive, got %v", out["traceId"])
	}
	out, _ = scrub(SurfaceUI, RawEvent{Name: EventChatFirstResponse, Properties: map[string]any{"traceId": "john.doe@acme.com not-a-uuid"}})
	if _, ok := out["traceId"]; ok {
		t.Errorf("non-UUID traceId must be dropped, got %v", out["traceId"])
	}
}

func scrubCommand(t *testing.T, value string) string {
	t.Helper()
	out, ok := scrub(SurfaceCLI, RawEvent{
		Name:       EventCLICommandRun,
		Properties: map[string]any{PropCommand: value},
	})
	if !ok {
		t.Fatal("known event should be kept")
	}
	got, _ := out[PropCommand].(string)
	return got
}

func TestTextShapePhoneNumberIsNotRedacted(t *testing.T) {
	got := scrubCommand(t, "call 555-123-4567 or a@b.com")
	if strings.Contains(got, "a@b.com") {
		t.Errorf("email must be redacted from a free-text field, got %q", got)
	}
	if !strings.Contains(got, "555-123-4567") {
		t.Errorf("phone currently passes through by design, got %q", got)
	}
}

func TestTextShapeRedactsHomePath(t *testing.T) {
	for _, value := range []string{
		"open /Users/alice/secrets.env",
		"open /home/bob/.ssh/id_rsa",
		`open C:\Users\carol\secrets.env`,
	} {
		got := scrubCommand(t, value)
		if strings.Contains(got, "alice") || strings.Contains(got, "bob") || strings.Contains(got, "carol") {
			t.Errorf("home path must be redacted from a free-text field, got %q", got)
		}
	}
}

func TestTextShapeRedactsIPv4(t *testing.T) {
	got := scrubCommand(t, "connect to 192.168.1.42 now")
	if strings.Contains(got, "192.168.1.42") {
		t.Errorf("IPv4 address must be redacted from a free-text field, got %q", got)
	}
	if !strings.Contains(got, "connect to") || !strings.Contains(got, "now") {
		t.Errorf("legitimate text must survive redaction, got %q", got)
	}
}

func TestScrubStripsPropertiesOutsideTheAllowList(t *testing.T) {
	out, _ := scrub(SurfaceServer, RawEvent{
		Name:       "connector:created",
		Properties: map[string]any{"category": "slack", "userEmail": "a@b.com"},
	})
	if out["category"] != "slack" {
		t.Errorf("the approved property should be kept, got %v", out["category"])
	}
	if _, ok := out["userEmail"]; ok {
		t.Error("a property the catalog does not name must be stripped by the backend allow-list")
	}
}

func routePatternOf(v any) (string, bool) {
	got, keep := routePatternShape().validate(v)
	if !keep {
		return "", false
	}
	s, _ := got.(string)
	return s, true
}

func TestRoutePatternsAreAcceptedVerbatim(t *testing.T) {
	for _, pattern := range []string{
		"/",
		"/chat",
		"/agent-mode/recent-chats",
		"/agent-mesh/:id",
		"/agent-mesh/workflows/:workflowName",
		"/connectors/:id/tools/event_mesh/:toolName",
		"/evaluations/runGroups/:groupId/examples/:exampleKey",
		"/user-management/claim-mapping",
		"/toolsets/:*",
		"/:*",
		"/:id/:id",
	} {
		got, keep := routePatternOf(pattern)
		if !keep {
			t.Errorf("pattern %q was refused, but the router emits it", pattern)
			continue
		}
		if got != pattern {
			t.Errorf("pattern %q came back as %q, want it emitted exactly as sent", pattern, got)
		}
	}
}

func TestOperatorAuthoredNamesInAPathAreRefused(t *testing.T) {
	for _, path := range []string{
		"/workflows/refund-bot",
		"/agents/hr-policy-bot",
		"/connectors/acme-payroll",
		"/toolsets/mergers",
		"/agents/123e4567-e89b-12d3-a456-426614174000/edit",
		"/orders/123456",
		"/u/a@b.com",
		"/u/john.doe",
		"/files/%2Fhome%2Falice%2Fkey.pem",
		"/reset?token=sk1234",
		"/page#a@b.com",
		"/users/john\x00.smith/settings",
		"/Agents",
		"/agents/",
		"agents",
		"",
	} {
		if got, keep := routePatternOf(path); keep {
			t.Errorf("path %q was kept as %q, want the field dropped", path, got)
		}
	}
}

func TestABulkTextPathIsRefused(t *testing.T) {
	var b strings.Builder
	for range 24 {
		b.WriteString("/")
		b.WriteString(strings.Repeat("a", 20))
	}
	if got, keep := routePatternOf(b.String()); keep {
		t.Errorf("a %d byte segmented payload was kept as %q", b.Len(), got)
	}
}

func TestRoutePatternRejectsANonString(t *testing.T) {
	for _, v := range []any{7, nil, []string{"/chat"}, map[string]any{}} {
		if _, keep := routePatternOf(v); keep {
			t.Errorf("value %#v is not a string and must be dropped", v)
		}
	}
}

func TestTextShapeRedactsEmailButKeepsNames(t *testing.T) {
	got := scrubCommand(t, "agent add for john.doe@acme.com")
	if strings.Contains(got, "@") || strings.Contains(got, "acme.com") {
		t.Errorf("email must be redacted from a free-text field, got %q", got)
	}
	if !strings.Contains(got, "agent add") {
		t.Errorf("legitimate text must survive redaction, got %q", got)
	}
}

func TestTextShapePreservesIdentifierLikeNames(t *testing.T) {
	if got := scrubCommand(t, "auth-config-2-token"); got != "auth-config-2-token" {
		t.Errorf("value should be preserved verbatim, got %q", got)
	}
}

func TestTextShapeStripsControlChars(t *testing.T) {
	if got := scrubCommand(t, "save\x00\n-btn"); got != "save-btn" {
		t.Errorf("control chars must be stripped, got %q", got)
	}
}

func TestTextShapeRedactsEmailSplitByControlChars(t *testing.T) {
	for _, value := range []string{
		"user@exa\x01mple.com",
		"us\x01er@example.com",
		"user@example.co\x01m",
		"user\x00@example.com",
	} {
		got := scrubCommand(t, value)
		if strings.Contains(got, "@") || strings.Contains(got, "example.com") {
			t.Errorf("control-split email survived redaction: input %q gave %q", value, got)
		}
	}
}

func TestEverySurfaceHasACatalog(t *testing.T) {
	for _, surface := range surfaces {
		events, _ := catalogFor(surface)
		if len(events) == 0 {
			t.Errorf("surface %q has no catalog, so it would drop every event", surface)
		}
	}
}

func TestAnUndeclaredSurfaceEmitsNothing(t *testing.T) {
	for _, surface := range []string{"", "api", "UI", "cli "} {
		if events, universal := catalogFor(surface); len(events) != 0 || len(universal) != 0 {
			t.Errorf("surface %q resolved to a catalog of %d events and %d universal fields, want none",
				surface, len(events), len(universal))
		}
		if _, ok := scrub(surface, RawEvent{Name: EventPageView}); ok {
			t.Errorf("surface %q was allowed to emit %q", surface, EventPageView)
		}
	}
}

func TestSurfaceCatalogsAreDisjoint(t *testing.T) {
	seen := make(map[EventName]string, len(catalog))
	for _, surface := range surfaces {
		events, _ := catalogFor(surface)
		for name := range events {
			if other, dup := seen[name]; dup {
				t.Errorf("event %q is claimed by both %q and %q", name, other, surface)
			}
			seen[name] = surface
		}
	}
	if len(seen) != len(catalog) {
		t.Errorf("per-surface catalogs cover %d events, the merged catalog has %d", len(seen), len(catalog))
	}
}

func TestAnOverlongPathIsDropped(t *testing.T) {
	if maxPathLen != 512 {
		t.Errorf("maxPathLen = %d, want 512", maxPathLen)
	}
	if _, ok := routePatternOf("/" + strings.Repeat("a", maxPathLen)); ok {
		t.Errorf("a %d byte path was accepted, but the bound is %d", maxPathLen+1, maxPathLen)
	}
	if _, ok := routePatternOf("/agents/:id/edit"); !ok {
		t.Error("an ordinary route must still be accepted, or the bound proves nothing")
	}
}

var reRouterPath = regexp.MustCompile(`(?m)^\s*path:\s*"([^"]*)"`)

func routerVocabulary(t *testing.T) (words, params []string) {
	t.Helper()
	routerPath := filepath.Join(repoRoot(t), "webui", "src", "app", "router.tsx")
	data, err := os.ReadFile(routerPath)
	if err != nil {
		t.Fatalf("read the web UI route table: %v", err)
	}
	matches := reRouterPath.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no route paths parsed from %s", routerPath)
	}
	for _, m := range matches {
		for _, segment := range strings.Split(m[1], "/") {
			switch {
			case segment == "":
			case segment == "*":
				params = append(params, "*")
			case strings.HasPrefix(segment, ":"):
				params = append(params, strings.TrimPrefix(segment, ":"))
			default:
				words = append(words, segment)
			}
		}
	}
	slices.Sort(words)
	slices.Sort(params)
	return slices.Compact(words), slices.Compact(params)
}

func TestRouteVocabularyMatchesTheAppRouteTable(t *testing.T) {
	words, params := routerVocabulary(t)
	if !slices.Equal(words, routeWords) {
		t.Errorf("route table declares static segments %v, routeWords holds %v", words, routeWords)
	}
	if !slices.Equal(params, routeParams) {
		t.Errorf("route table declares dynamic segments %v, routeParams holds %v", params, routeParams)
	}
}

func TestEveryDeclaredRouteSegmentIsAccepted(t *testing.T) {
	words, params := routerVocabulary(t)
	for _, w := range words {
		if _, keep := routePatternOf("/" + w); !keep {
			t.Errorf("static route segment %q is refused", w)
		}
	}
	for _, p := range params {
		if _, keep := routePatternOf("/:" + p); !keep {
			t.Errorf("dynamic route segment %q is refused", ":"+p)
		}
	}
}

func TestAnImplausiblyDeepRouteIsRefused(t *testing.T) {
	deep := strings.Repeat("/chat", maxRouteSegments)
	if _, keep := routePatternOf(deep); !keep {
		t.Errorf("a %d segment route was refused, but the bound is %d", maxRouteSegments, maxRouteSegments)
	}
	tooDeep := strings.Repeat("/chat", maxRouteSegments+1)
	if got, keep := routePatternOf(tooDeep); keep {
		t.Errorf("a %d segment route was kept as %q, but the bound is %d", maxRouteSegments+1, got, maxRouteSegments)
	}
}

func TestOperatorAuthoredModelTextNeverReachesASink(t *testing.T) {
	cases := []struct {
		name    string
		field   string
		value   string
		want    any
		secrets []string
	}{
		{
			name:    "an inference-profile ARN carries an AWS account id",
			field:   "model",
			value:   "arn:aws:bedrock:us-east-1:123456789012:inference-profile/acme-legal",
			want:    nil,
			secrets: []string{"123456789012", "acme-legal", "us-east-1"},
		},
		{
			name:    "a fine-tune id names the organization and the use case",
			field:   "model",
			value:   "ft:gpt-4o-2024-08-06:acme-corp:legal-triage:AbCd1234",
			want:    nil,
			secrets: []string{"acme-corp", "legal-triage", "AbCd1234"},
		},
		{
			name:    "a self-hosted deployment name identifies the install",
			field:   "model",
			value:   "acme-internal-vllm-cluster",
			want:    nil,
			secrets: []string{"acme"},
		},
		{
			name:    "a self-hosted provider name identifies the install",
			field:   "provider",
			value:   "acme-internal-vllm-cluster",
			want:    nil,
			secrets: []string{"acme"},
		},
		{
			name:    "a short human-meaningful provider name is not spared for being short",
			field:   "provider",
			value:   "acme",
			want:    nil,
			secrets: []string{"acme"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := scrub(SurfaceServer, RawEvent{
				Name:       EventModelConfigured,
				Properties: map[string]any{c.field: c.value},
			})
			if !ok {
				t.Fatal("the event itself is approved and must survive")
			}
			if got := out[c.field]; got != c.want {
				t.Errorf("%s = %#v, want %#v", c.field, got, c.want)
			}
			rendered := fmt.Sprintf("%v", out)
			for _, secret := range c.secrets {
				if strings.Contains(rendered, secret) {
					t.Errorf("%q survived into the scrubbed properties %s", secret, rendered)
				}
			}
		})
	}
}

func TestUnknownConnectorCategoryIsDropped(t *testing.T) {
	cases := []struct{ surface, event, field string }{
		{SurfaceServer, EventConnectorCreated, "category"},
		{SurfaceServer, EventToolUsed, PropConnectorCategory},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			out, ok := scrub(c.surface, RawEvent{
				Name:       c.event,
				Properties: map[string]any{c.field: "acme_customer_ledger"},
			})
			if !ok {
				t.Fatal("the event itself is approved and must survive")
			}
			if got, kept := out[c.field]; kept {
				t.Errorf("%s = %v, want the unlisted category dropped", c.field, got)
			}
			out, _ = scrub(c.surface, RawEvent{
				Name:       c.event,
				Properties: map[string]any{c.field: "knowledge_base"},
			})
			if out[c.field] != "knowledge_base" {
				t.Errorf("%s = %v, want a listed category kept", c.field, out[c.field])
			}
		})
	}
}

func TestModelIdentifiersAreRefusedOnEveryEventThatNamesAFamily(t *testing.T) {
	const identifier = "ft:gpt-4o:acme-corp:legal-triage:AbCd1234"

	for _, event := range []EventName{EventAgentRunCompleted, EventModelConfigured} {
		t.Run(event, func(t *testing.T) {
			out, ok := scrub(SurfaceServer, RawEvent{
				Name:       event,
				Properties: map[string]any{PropModel: identifier},
			})
			if !ok {
				t.Fatal("the event itself is approved and must survive")
			}
			if got, kept := out[PropModel]; kept {
				t.Errorf("%s = %v, want the identifier refused", PropModel, got)
			}

			out, _ = scrub(SurfaceServer, RawEvent{
				Name:       event,
				Properties: map[string]any{PropModel: modelfamily.GPT4o},
			})
			if out[PropModel] != modelfamily.GPT4o {
				t.Errorf("%s = %v, want a classified family kept", PropModel, out[PropModel])
			}
		})
	}
}

func TestCatalogPropertiesMatchFrontendTypes(t *testing.T) {
	fe := frontendEventProperties(t)
	for name, fields := range webUICatalog {
		feProps, declared := fe[name]
		if !declared {
			t.Errorf("event %q has no entry in ProductAnalyticsEventProperties; add one so its fields are pinned", name)
			continue
		}
		goProps := make([]string, 0, len(fields))
		for field := range fields {
			goProps = append(goProps, field)
		}
		slices.Sort(goProps)
		slices.Sort(feProps)
		if !slices.Equal(goProps, feProps) {
			t.Errorf("event %q: Go carries %v, the frontend declares %v", name, goProps, feProps)
		}
	}
	for name := range fe {
		if _, ok := webUICatalog[name]; !ok {
			t.Errorf("ProductAnalyticsEventProperties declares %q, which is not a web UI catalog event", name)
		}
	}
}

func frontendEventProperties(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "webui", "src", "common", "product-analytics", "types.ts"))
	if err != nil {
		t.Fatalf("read frontend types.ts: %v", err)
	}
	block := regexp.MustCompile(`(?s)export type ProductAnalyticsEventProperties = \{(.*?)\n\};`).FindStringSubmatch(string(data))
	if block == nil {
		t.Fatal("ProductAnalyticsEventProperties not found in types.ts")
	}
	out := map[string][]string{}
	for _, line := range regexp.MustCompile(`(?m)^\s*"?([\w:]+)"?:\s*(.+?);\s*$`).FindAllStringSubmatch(block[1], -1) {
		name, value := line[1], line[2]
		if strings.TrimSpace(value) == "never" {
			out[name] = nil
			continue
		}
		out[name] = regexp.MustCompile(`"([^"]+)"`).FindAllString(value, -1)
		for i, quoted := range out[name] {
			out[name][i] = strings.Trim(quoted, `"`)
		}
	}
	return out
}
