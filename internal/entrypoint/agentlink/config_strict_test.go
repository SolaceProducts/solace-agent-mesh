// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"strings"
	"testing"
)

func configWith(agent map[string]any) map[string]any {
	base := map[string]any{"name": agentName, "token_sha256": digest(token)}
	for k, v := range agent {
		base[k] = v
	}
	return map[string]any{"agents": []any{base}}
}

func wakeWith(keys map[string]any) map[string]any {
	wake := map[string]any{"url": "https://runtime.example.com"}
	for k, v := range keys {
		wake[k] = v
	}
	return map[string]any{"wake": wake}
}

func TestConfigRefusesAKeyOfTheWrongTypeOrAnUnknownKeyNamingTheAgentAndTheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		agent map[string]any
		key   string
	}{
		"enabled off":                {map[string]any{"enabled": "off"}, "enabled"},
		"enabled zero":               {map[string]any{"enabled": 0}, "enabled"},
		"enabled quoted false":       {map[string]any{"enabled": "false"}, "enabled"},
		"enabled empty":              {map[string]any{"enabled": nil}, "enabled"},
		"peers scalar":               {map[string]any{"peers": "policy-desk"}, "peers"},
		"peer not a string":          {map[string]any{"peers": []any{7}}, "peer"},
		"wake scalar":                {map[string]any{"wake": true}, "wake"},
		"share profile quoted":       {map[string]any{"share_user_profile": "yes"}, "share_user_profile"},
		"owner number":               {map[string]any{"owner": 5}, "owner"},
		"team list":                  {map[string]any{"team": []any{"a"}}, "team"},
		"misspelled enabled":         {map[string]any{"enabld": false}, "enabld"},
		"unknown key":                {map[string]any{"owners": "x"}, "owners"},
		"unknown wake key":           {wakeWith(map[string]any{"timeout_second": 5}), "timeout_second"},
		"wake url number":            {wakeWith(map[string]any{"url": 5}), "url"},
		"wake scope list":            {wakeWith(map[string]any{"scope": []any{"x"}}), "scope"},
		"wake client id number":      {wakeWith(map[string]any{"client_id": 5}), "client_id"},
		"wake headers scalar":        {wakeWith(map[string]any{"headers": "x"}), "headers"},
		"wake timeout text":          {wakeWith(map[string]any{"timeout_seconds": "ten"}), "timeout_seconds"},
		"wake token url number":      {wakeWith(map[string]any{"token_url": 5}), "token_url"},
		"wake client secret boolean": {wakeWith(map[string]any{"client_secret": true}), "client_secret"},
	} {
		_, err := ParseConfig(configWith(tc.agent))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), agentName) || !strings.Contains(err.Error(), tc.key) {
			t.Errorf("%s: the refusal names neither the agent nor the key %q: %v", name, tc.key, err)
		}
	}
}

func TestConfigReportsEveryProblemOfAnAgentTogether(t *testing.T) {
	_, err := ParseConfig(configWith(map[string]any{"enabled": "off", "peers": "policy-desk", "enabld": true}))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, key := range []string{"enabled:", "peers:", "enabld"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("%q is not reported: %v", key, err)
		}
	}
}

func TestConfigKeepsReadingWhatItAlwaysRead(t *testing.T) {
	cfg, err := ParseConfig(configWith(map[string]any{
		"enabled": false, "owner": " ops ", "team": "claims", "share_user_profile": true,
		"peers": []any{peerName},
		"wake": map[string]any{"url": "https://runtime.example.com", "token_url": "https://idp.example.com", "client_id": "id",
			"client_secret": "secret", "scope": "s", "headers": map[string]any{"X-Key": "v"}, "timeout_seconds": 30},
	}))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Agents[agentName]
	if a.Enabled || a.Owner != "ops" || a.Team != "claims" || !a.ShareUserProfile || len(a.Peers) != 1 || a.Wake == nil || a.Wake.Headers["X-Key"] != "v" {
		t.Fatalf("%+v %+v", a, a.Wake)
	}
}

func TestConfigRefusesTwoAgentsThatShareATokenDigestNamingBoth(t *testing.T) {
	for name, second := range map[string]string{"same case": digest(token), "other case": strings.ToUpper(digest(token))} {
		_, err := ParseConfig(map[string]any{"agents": []any{
			map[string]any{"name": agentName, "token_sha256": digest(token)},
			map[string]any{"name": "other-agent", "token_sha256": second},
		}})
		if err == nil || !strings.Contains(err.Error(), agentName) || !strings.Contains(err.Error(), "other-agent") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ParseConfig(map[string]any{"agents": []any{
		map[string]any{"name": agentName, "token_sha256": digest(token)},
		map[string]any{"name": "other-agent", "token_sha256": digest(peerToken)},
	}}); err != nil {
		t.Fatalf("two agents with their own tokens: %v", err)
	}
}

func TestConfigReservesTheNamesThePlatformMintsForItsOwnComponents(t *testing.T) {
	const id = "0190f3c2-7a52-7c11-9d3e-5b6f1a2c3d4e"
	underscored := strings.ReplaceAll(id, "-", "_")
	for _, name := range []string{"agent_" + id, "agent_" + underscored, "workflow_" + id, "workflow_" + underscored} {
		_, err := ParseConfig(configWith(map[string]any{"name": name}))
		if err == nil || !strings.Contains(err.Error(), "reserved for agents and workflows the platform deploys") {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"claims-assistant", "agent_helper", "agent_not-a-uuid", "workflow_" + id + "_x", "my_agent_" + id} {
		if _, err := ParseConfig(configWith(map[string]any{"name": name})); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestConfigRefusesANameTooLongForItsCardToBeListed(t *testing.T) {
	longest := strings.Repeat("a", 117)
	if _, err := ParseConfig(configWith(map[string]any{"name": longest})); err != nil {
		t.Fatalf("a name of 117 characters: %v", err)
	}
	_, err := ParseConfig(configWith(map[string]any{"name": longest + "a"}))
	if err == nil || !strings.Contains(err.Error(), "117") {
		t.Fatalf("a name of 118 characters: %v", err)
	}
}

func TestConfigReportsABadTokenDigestWithTheAgentsUnknownKeys(t *testing.T) {
	_, err := ParseConfig(configWith(map[string]any{"token_sha256": "not-a-digest", "enabld": true}))
	if err == nil || !strings.Contains(err.Error(), "token_sha256") || !strings.Contains(err.Error(), `"enabld"`) {
		t.Fatalf("got %v, want the digest and the unknown key both named", err)
	}
}
