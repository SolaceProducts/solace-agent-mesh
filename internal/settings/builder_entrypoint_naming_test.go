// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"regexp"
	"strings"
	"testing"
)

var exampleNameWithGateway = regexp.MustCompile("(?i)(?:^|[\\s{,#(\\[`-])[\"'`]?(name|filename|display_name|gateway_id|agent_name|title|component_name|app_name)[\"'`]?[ \\t]*:[ \\t]*[\"'`]?([^#,}\\n\"'`]*gateway[^#,}\\n\"'`]*)")

var frozenGatewayTokens = map[string]bool{
	"sam-gateway-schema":          true,
	"vnd.sam-gateway-config+yaml": true,
}

var valueToken = regexp.MustCompile(`[\w.+-]+`)

func namesGatewayOutsideFrozenTokens(value string) bool {
	for _, token := range valueToken.FindAllString(strings.ToLower(value), -1) {
		if !frozenGatewayTokens[token] && strings.Contains(token, "gateway") {
			return true
		}
	}
	return false
}

var envReference = regexp.MustCompile(`^\$\{[A-Z_]+`)

func gatewayNamedExamples(body string) []string {
	var offenders []string
	for _, m := range exampleNameWithGateway.FindAllStringSubmatch(body, -1) {
		value := strings.TrimSpace(m[2])
		if envReference.MatchString(value) || !namesGatewayOutsideFrozenTokens(value) {
			continue
		}
		offenders = append(offenders, m[1]+": "+value)
	}
	return offenders
}

func TestBuilderCorpus_ExampleNamesSayEntrypointNotGateway(t *testing.T) {
	for name, body := range builderCorpusFiles(t) {
		for _, offender := range gatewayNamedExamples(body) {
			t.Errorf("%s has an example name containing \"gateway\" (%s).\n"+
				"The Builder copies example names, so they must say \"entrypoint\" (for example order_events_entrypoint). "+
				"If this is a frozen identifier, add it to frozenGatewayTokens.",
				name, offender)
		}
	}
}

func TestGatewayNamedExamples_FlagsOnlyUserVisibleNames(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"app name", "apps:\n  - name: order_gateway\n", 1},
		{"manifest filename", "- filename: my_em_gateway.yaml\n", 1},
		{"quoted gateway id", "gateway_id: \"prod-gateway\"\n", 1},
		{"hyphenated name in capitals", "  name: Alert-GATEWAY\n", 1},
		{"spaced display name", "display_name: \"Order Gateway\"\n", 1},
		{"unquoted spaced name", "name: Order Gateway\n", 1},
		{"JSON name", "{\"name\": \"order_gateway\"}", 1},
		{"flow-style mapping", "- {name: sensor_gateway, type: gateway}", 1},
		{"row inside a comment block", "#     - name: alert_gateway\n", 1},
		{"camelCase segment", "title: OrderGateway\n", 1},
		{"backticked key", "`name`: x_gateway", 1},
		{"bracket lead", "[name: x_gateway]", 1},
		{"parenthesis lead", "(name: x_gateway)", 1},
		{"frozen name inside a longer token", "name: my-sam-gateway-schema-copy\n", 1},
		{"empty name followed by a type", "name:\n  type: gateway\n", 0},
		{"skill name with a suffix", "name: sam-gateway-schema (load it)\n", 0},
		{"MIME type filename", "filename: application/vnd.sam-gateway-config+yaml\n", 0},
		{"environment placeholder", "gateway_id: \"${GATEWAY_ID}\"\n", 0},
		{"entrypoint style name", "  - name: order_events_entrypoint\n", 0},
		{"component type is not a name", "type: gateway\ngateway_type: event_mesh\n", 0},
		{"frozen skill name", "name: sam-gateway-schema\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(gatewayNamedExamples(tt.body)); got != tt.want {
				t.Errorf("got %d offenders in %q, want %d", got, tt.body, tt.want)
			}
		})
	}
}

func TestBuilderPrompt_KeepsTheEntrypointNamingRule(t *testing.T) {
	prompt := strings.Join(strings.Fields(string(builderAgentYAML)), " ")
	for _, want := range []string{
		`say "entrypoint", never "gateway"`,
		"`order_events_entrypoint`",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("builder_agent.yaml lost the entrypoint naming rule: missing %q", want)
		}
	}
}
