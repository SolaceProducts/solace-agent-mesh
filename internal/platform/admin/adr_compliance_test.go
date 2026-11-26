// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/adrscan"
)

var allowedSnakeCaseJSONTags = map[string]string{
	"model_config_bootstrap.go": "A2A broker bootstrap payload (component_id, component_type, reply_to, model_id) — API naming exception",
	"a2a_agent_service.go":      "OAuth 2.0 access_token (RFC 6749) — API naming exception",
	"mcp_oauth_token.go":        "OAuth 2.0 token response (access_token, token_type) per RFC 6749 — API naming exception",
	"skill_config_service.go":   "A2A SkillInitParams.BundledTools blob (tool_name, config_schema) parsed off the broker wire — API naming exception",
	"authz_lookup_handler.go":   "A2A broker authz lookup payload (user_id, gateway_context) — API naming exception",
	"user_upsert_handler.go":    "A2A broker user-upsert payload (snake_case) — API naming exception",
}

var allowedSnakeCaseQueryParams = map[string]string{}

var allowedRawDecoderFiles = map[string]string{
	"decode.go": "decodeJSON helper definition itself.",
}

func TestADRCompliance(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	adrscan.Run(t, pkgDir, adrscan.Options{
		SnakeCaseJSONTags:    allowedSnakeCaseJSONTags,
		SnakeCaseQueryParams: allowedSnakeCaseQueryParams,
		RawDecoderFiles:      allowedRawDecoderFiles,
	})
}
