// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func TestDeployerRegistry_LookupReturnsNilForUnknown(t *testing.T) {
	t.Parallel()
	r := NewDeployerRegistry()
	if r.Lookup("nope") != nil {
		t.Errorf("expected nil for unknown type")
	}
}

func TestDeployerRegistry_LookupOrError(t *testing.T) {
	t.Parallel()
	r := NewDeployerRegistry()
	_, err := r.LookupOrError(AgentTypeStandard)
	if err == nil {
		t.Fatal("expected error for unregistered type")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected ValidationError, got %T", err)
	}
}

func TestDeployerRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	r := NewDeployerRegistry()
	std := NewStandardAgentDeployer(nil, nil, nil, nil, nil)
	r.Register(AgentTypeStandard, std)

	got := r.Lookup(AgentTypeStandard)
	if got != std {
		t.Errorf("lookup returned wrong deployer")
	}
}

func TestStandardAgentDeployer_KindIsAgent(t *testing.T) {
	t.Parallel()
	if got := (&StandardAgentDeployer{}).Kind(); got != "agent" {
		t.Errorf("Kind() = %q, want %q", got, "agent")
	}
}

func TestStandardAgentDeployer_InstanceName(t *testing.T) {
	t.Parallel()
	d := &StandardAgentDeployer{}
	a := &store.Agent{ID: "abc", Name: "My Agent"}
	got := d.InstanceName(a)
	want := AgentInstanceName(a)
	if got != want {
		t.Errorf("InstanceName = %q, want %q", got, want)
	}
}

func TestStandardAgentDeployer_Validate_RejectsMissingSystemPrompt(t *testing.T) {
	t.Parallel()
	d := &StandardAgentDeployer{}
	a := &store.Agent{ID: "abc", Name: "x", Type: AgentTypeStandard}
	err := d.Validate(t.Context(), DeployInput{Agent: a, Action: DeployActionDeploy})
	if err == nil {
		t.Fatal("expected validation error for missing system prompt")
	}
}

func TestStandardAgentDeployer_Validate_AllowsUndeployWithoutPrompt(t *testing.T) {
	t.Parallel()
	d := &StandardAgentDeployer{}
	a := &store.Agent{ID: "abc", Name: "x", Type: AgentTypeStandard}
	if err := d.Validate(t.Context(), DeployInput{Agent: a, Action: DeployActionUndeploy}); err != nil {
		t.Errorf("undeploy must not require system prompt, got: %v", err)
	}
}

func TestStandardAgentDeployer_ReplayFromSnapshot_EmptyReturnsNoReplay(t *testing.T) {
	t.Parallel()
	d := &StandardAgentDeployer{}
	_, err := d.ReplayFromSnapshot("")
	if !errors.Is(err, ErrNoReplay) {
		t.Errorf("empty snapshot should return ErrNoReplay, got %v", err)
	}
}

func TestStandardAgentDeployer_BuildPayloads_HappyPath(t *testing.T) {
	t.Parallel()
	yamlSvc := NewYAMLService("test-ns", nil)
	d := NewStandardAgentDeployer(yamlSvc, nil, nil, nil, nil)

	prompt := "You are a friendly test agent that responds to questions."
	desc := "test agent for deployer unit test"
	agent := &store.Agent{
		ID:           "11111111-1111-1111-1111-111111111111",
		Name:         "Build Probe",
		Description:  &desc,
		Type:         AgentTypeStandard,
		SystemPrompt: &prompt,
	}
	input := DeployInput{
		Agent:       agent,
		Action:      DeployActionDeploy,
		Toolsets:    []string{},
		InputModes:  []string{"text"},
		OutputModes: []string{"text"},
	}

	result, err := d.BuildPayloads(t.Context(), input)
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	if len(result.Payloads) == 0 {
		t.Fatal("expected at least one ControlPayload")
	}

	wantName := AgentInstanceName(agent)
	var found bool
	for _, p := range result.Payloads {
		if p["name"] == wantName && p["kind"] == "agent" {
			found = true
			if _, ok := p["app_config"].(map[string]any); !ok {
				t.Errorf("agent payload app_config is not map[string]any: %T", p["app_config"])
			}
		}
	}
	if !found {
		t.Errorf("no payload with name=%q kind=agent in %d payloads", wantName, len(result.Payloads))
	}

	if result.SnapshotYAML == "" {
		t.Error("expected non-empty SnapshotYAML")
	}
	if result.ConfigHash == "" {
		t.Error("expected non-empty ConfigHash")
	}
	snap, ok := result.DataSnapshot.(DeployedAgentInfo)
	if !ok {
		t.Fatalf("DataSnapshot not DeployedAgentInfo: %T", result.DataSnapshot)
	}
	if snap.Name != agent.Name {
		t.Errorf("snapshot.Name = %q, want %q", snap.Name, agent.Name)
	}
	if snap.SystemPrompt != prompt {
		t.Errorf("snapshot.SystemPrompt = %q, want %q", snap.SystemPrompt, prompt)
	}
}

func TestStandardAgentDeployer_ReplayFromSnapshot_RoundTrips(t *testing.T) {
	t.Parallel()
	yamlSvc := NewYAMLService("test-ns", nil)
	d := NewStandardAgentDeployer(yamlSvc, nil, nil, nil, nil)

	prompt := "You are a snapshot-replay test agent for the standard deployer."
	desc := "snapshot replay probe"
	agent := &store.Agent{
		ID:           "22222222-2222-2222-2222-222222222222",
		Name:         "Snapshot Probe",
		Description:  &desc,
		Type:         AgentTypeStandard,
		SystemPrompt: &prompt,
	}
	first, err := d.BuildPayloads(t.Context(), DeployInput{Agent: agent, Action: DeployActionDeploy, InputModes: []string{"text"}, OutputModes: []string{"text"}})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	replayed, err := d.ReplayFromSnapshot(first.SnapshotYAML)
	if err != nil {
		t.Fatalf("ReplayFromSnapshot: %v", err)
	}
	if len(replayed) != len(first.Payloads) {
		t.Errorf("replay payload count = %d, want %d", len(replayed), len(first.Payloads))
	}
}

func TestA2AProxyDeployer_KindIsProxy(t *testing.T) {
	t.Parallel()
	if got := (&A2AProxyDeployer{}).Kind(); got != "proxy" {
		t.Errorf("Kind() = %q, want %q (AWE registration key)", got, "proxy")
	}
}

func TestA2AProxyDeployer_ReplayFromSnapshot_AlwaysNoReplay(t *testing.T) {
	t.Parallel()
	d := &A2AProxyDeployer{}
	_, err := d.ReplayFromSnapshot(`{"id":"abc","name":"foo"}`)
	if !errors.Is(err, ErrNoReplay) {
		t.Errorf("non-empty A2A snapshot should still return ErrNoReplay, got %v", err)
	}
}

func TestA2AProxyDeployer_BuildPayloads_AppConfigShape(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	ctx := t.Context()

	authType := "none"
	authConfig := `{}`
	a2a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "Web Search",
		AgentCardLocation:     "none",
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		Skills:                strPtr(`[{"name":"search"}]`),
		RequestTimeoutSeconds: 90,
		CreatedBy:             "u",
		UpdatedBy:             "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}
	result, err := d.BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	if len(result.Payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(result.Payloads))
	}
	p := result.Payloads[0]
	if p["kind"] != "proxy" {
		t.Errorf("kind = %v, want proxy", p["kind"])
	}
	if p["name"] != A2AAgentInstanceName(a2a) {
		t.Errorf("name = %v, want %s", p["name"], A2AAgentInstanceName(a2a))
	}
	appCfg, ok := p["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("app_config is not map[string]any: %T", p["app_config"])
	}
	if appCfg["url"] != a2a.AgentURL {
		t.Errorf("url = %v, want %s", appCfg["url"], a2a.AgentURL)
	}
	if appCfg["namespace"] != "test-ns" {
		t.Errorf("namespace = %v, want test-ns", appCfg["namespace"])
	}
	if appCfg["display_name"] != a2a.Name {
		t.Errorf("display_name = %v, want %s", appCfg["display_name"], a2a.Name)
	}
	if _, present := appCfg["discovery_interval_seconds"]; present {
		t.Errorf("discovery_interval_seconds must be unset for card=none (got %v)", appCfg["discovery_interval_seconds"])
	}
	cardData, ok := appCfg["agent_card_data"].(map[string]any)
	if !ok {
		t.Fatalf("agent_card_data must be a map for card=none, got %T", appCfg["agent_card_data"])
	}
	if cardData["name"] != a2a.Name {
		t.Errorf("agent_card_data.name = %v, want %s", cardData["name"], a2a.Name)
	}
	skills, ok := cardData["skills"].([]any)
	if !ok || len(skills) != 1 {
		t.Fatalf("agent_card_data.skills = %v, want one inline skill", cardData["skills"])
	}
	if skill0, isMap := skills[0].(map[string]any); !isMap || skill0["name"] != "search" {
		t.Errorf("agent_card_data.skills[0] = %v, want the stored 'search' skill", skills[0])
	}
	if appCfg["request_timeout_seconds"] != 90 {
		t.Errorf("request_timeout_seconds = %v, want 90", appCfg["request_timeout_seconds"])
	}
	if result.ConfigHash == "" {
		t.Error("ConfigHash must be populated")
	}
}

func TestA2AProxyDeployer_BuildPayloads_WellKnownCard(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	ctx := t.Context()

	authType := "bearer"
	authConfig := `{"type":"bearer","token":"real-token"}`
	a2a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "WellKnownAgent",
		AgentCardLocation:     "well_known",
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		Skills:                strPtr(`[{"name":"search"}]`),
		RequestTimeoutSeconds: 90,
		CreatedBy:             "u",
		UpdatedBy:             "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}
	result, err := d.BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	appCfg := result.Payloads[0]["app_config"].(map[string]any)

	if _, ok := appCfg["agent_card_data"]; ok {
		t.Errorf("agent_card_data must be unset for well_known (got %v)", appCfg["agent_card_data"])
	}
	if _, ok := appCfg["agent_card_path"]; ok {
		t.Errorf("agent_card_path must be unset for well_known (proxy default applies), got %v", appCfg["agent_card_path"])
	}
	if appCfg["use_auth_for_agent_card"] != true {
		t.Errorf("use_auth_for_agent_card = %v, want true for authenticated well_known card", appCfg["use_auth_for_agent_card"])
	}
	auth, ok := appCfg["authentication"].(map[string]any)
	if !ok {
		t.Fatalf("authentication must be set for bearer auth, got %T", appCfg["authentication"])
	}
	if auth["type"] != "static_bearer" {
		t.Errorf("authentication.type = %v, want static_bearer (normalized)", auth["type"])
	}
	if auth["token"] != "real-token" {
		t.Errorf("authentication.token = %v, want real-token", auth["token"])
	}
}

func TestA2AProxyDeployer_BuildPayloads_CustomCard(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	ctx := t.Context()

	cardURL := "https://example.com/custom-card.json"
	a2a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "CustomCardAgent",
		AgentCardLocation:     "custom",
		AgentCardURL:          &cardURL,
		AgentAuthType:         strPtr("none"),
		AgentAuthConfig:       strPtr("{}"),
		Skills:                strPtr(`[{"name":"search"}]`),
		RequestTimeoutSeconds: 90,
		CreatedBy:             "u",
		UpdatedBy:             "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}
	result, err := d.BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	appCfg := result.Payloads[0]["app_config"].(map[string]any)

	if appCfg["agent_card_path"] != cardURL {
		t.Errorf("agent_card_path = %v, want %s", appCfg["agent_card_path"], cardURL)
	}
	if _, ok := appCfg["agent_card_data"]; ok {
		t.Errorf("agent_card_data must be unset for custom card (got %v)", appCfg["agent_card_data"])
	}
	if _, ok := appCfg["discovery_interval_seconds"]; ok {
		t.Errorf("discovery_interval_seconds must be unset for custom card (got %v)", appCfg["discovery_interval_seconds"])
	}
}

func TestA2AProxyDeployer_BuildPayloads_CardNoneParsesToStaticCard(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	ctx := t.Context()

	a2a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "InlineSkillAgent",
		Description:           strPtr("Proxies an external agent with no card."),
		AgentCardLocation:     "none",
		AgentAuthType:         strPtr("none"),
		AgentAuthConfig:       strPtr("{}"),
		Skills:                strPtr(`[{"name":"Multiply","description":"Multiplies numbers."}]`),
		InputModes:            strPtr(`["text"]`),
		OutputModes:           strPtr(`["text"]`),
		RequestTimeoutSeconds: 90,
		CreatedBy:             "u",
		UpdatedBy:             "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}
	result, err := d.BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	appCfg := result.Payloads[0]["app_config"].(map[string]any)
	pc, err := proxy.ParseProxyConfig(result.Payloads[0]["name"].(string), config.FromMap(appCfg), nil)
	if err != nil {
		t.Fatalf("ParseProxyConfig rejected the card=none payload: %v", err)
	}
	if pc.AgentCardData == nil {
		t.Fatal("ProxyConfig.AgentCardData is nil — proxy would fall back to HTTP-fetching a nonexistent card")
	}
	if pc.DiscoveryIntervalSeconds <= 0 {
		t.Errorf("DiscoveryIntervalSeconds = %d, want > 0 so the static card republishes past the TTL", pc.DiscoveryIntervalSeconds)
	}

	if pc.AgentCardData["description"] != "Proxies an external agent with no card." {
		t.Errorf("agent_card_data.description = %v, want the stored description", pc.AgentCardData["description"])
	}
	skills, ok := pc.AgentCardData["skills"].([]any)
	if !ok || len(skills) != 1 {
		t.Fatalf("agent_card_data.skills = %v, want one inline skill", pc.AgentCardData["skills"])
	}
	if s0, ok := skills[0].(map[string]any); !ok || s0["name"] != "Multiply" || s0["description"] != "Multiplies numbers." {
		t.Errorf("agent_card_data.skills[0] = %v, want the stored Multiply skill", skills[0])
	}
	if modes, ok := pc.AgentCardData["defaultInputModes"].([]any); !ok || len(modes) != 1 || modes[0] != "text" {
		t.Errorf("agent_card_data.defaultInputModes = %v, want [text]", pc.AgentCardData["defaultInputModes"])
	}
	if modes, ok := pc.AgentCardData["defaultOutputModes"].([]any); !ok || len(modes) != 1 || modes[0] != "text" {
		t.Errorf("agent_card_data.defaultOutputModes = %v, want [text]", pc.AgentCardData["defaultOutputModes"])
	}
}

func TestA2AProxyDeployer_BuildPayloads_SnapshotHasNoSecret(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	ctx := t.Context()

	authType := "oauth2"
	authConfig := `{"type":"oauth2","client_id":"client123","client_secret":"SUPER-SECRET"}`
	a2a := &store.A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "AdditionAgent",
		AgentCardLocation:     "well_known",
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		Skills:                strPtr(`[{"name":"add_numbers"}]`),
		RequestTimeoutSeconds: 90,
		CreatedBy:             "u",
		UpdatedBy:             "u",
	}
	if err := a2aRepo.Create(ctx, a2a); err != nil {
		t.Fatalf("seed a2a row: %v", err)
	}

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: a2a.ID, Name: a2a.Name, Type: AgentTypeA2AProxy}
	result, err := d.BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	dataBlob, err := json.Marshal(result.DataSnapshot)
	if err != nil {
		t.Fatalf("marshal DataSnapshot: %v", err)
	}
	for name, blob := range map[string]string{"SnapshotYAML": result.SnapshotYAML, "DataSnapshot": string(dataBlob)} {
		if strings.Contains(blob, "SUPER-SECRET") {
			t.Errorf("%s leaked secret: %s", name, blob)
		}
		if strings.Contains(blob, "client_secret") || strings.Contains(blob, "AuthConfig") {
			t.Errorf("%s carries an auth-config object: %s", name, blob)
		}
		if !strings.Contains(blob, "oauth2") {
			t.Errorf("%s dropped the non-secret auth type: %s", name, blob)
		}
	}
}

func TestA2AProxyDeployer_Validate_RejectsMissingA2ARow(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)

	d := NewA2AProxyDeployer(a2aRepo, "test-ns")
	agent := &store.Agent{ID: "ffffffff-ffff-ffff-ffff-ffffffffffff", Name: "ghost", Type: AgentTypeA2AProxy}
	err := d.Validate(t.Context(), DeployInput{Agent: agent, Action: DeployActionDeploy})
	if err == nil {
		t.Fatal("expected validation error when a2a_agents row is missing")
	}
}
