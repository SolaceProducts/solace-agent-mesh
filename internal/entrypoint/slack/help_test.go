// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func TestCmdHelp_HidesRestrictedAndEphemeralAgents(t *testing.T) {
	t.Parallel()

	posts := &postedTexts{}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"email": "u@test.com"}})
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posts.add(r.FormValue("text"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C1", "ts": "1700000000.000010"})
	})
	apiSrv := httptest.NewServer(mux)
	t.Cleanup(apiSrv.Close)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "open-agent"})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "restricted-agent",
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI:    a2a.ExtensionURIRequiredScopes,
			Params: map[string]any{"scopes": []any{"analytics:_:read"}},
		}}},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: "ephemeral-agent",
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI: a2a.EphemeralAgentExtensionURI,
		}}},
	})
	core.AgentRegistry().AddOrUpdate(cardWithDisplayName(
		"agent_019fe224_dd07_764b_ae4c_125a04fb3ab2", "Orchestrator"))

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	api := slackapi.NewSlackAPIAdapter(goslack.New("xoxb-test", goslack.OptionAPIURL(apiSrv.URL+"/")), "xoxb-test", slackapi.WithDisabledRateLimiting())
	a := &SlackGatewayAdapter{
		core:       core,
		cfg:        &SlackConfig{},
		api:        api,
		emailCache: NewEmailCache(api, 0, testLogger()),
		logger:     testLogger(),
		ctx:        ctx,
		cancel:     cancel,
	}

	a.cmdHelp("C1", "", "U1", "T1")

	all := posts.all()
	if len(all) != 1 {
		t.Fatalf("expected exactly one help message, got %d: %v", len(all), all)
	}
	body := all[0]
	if !strings.Contains(body, "open-agent") {
		t.Errorf("help should list the open agent, got: %q", body)
	}
	if strings.Contains(body, "restricted-agent") {
		t.Errorf("help must not list an agent the caller lacks required_scopes for, got: %q", body)
	}
	if strings.Contains(body, "ephemeral-agent") {
		t.Errorf("help must not list ephemeral (__test__) agents, got: %q", body)
	}
	if strings.Contains(body, "agent_019fe224_dd07_764b_ae4c_125a04fb3ab2") {
		t.Errorf("help leaks a platform-deployed agent's broker wire name, got: %q", body)
	}
	if !strings.Contains(body, "Orchestrator") {
		t.Errorf("help should list the platform-deployed agent by display name, got: %q", body)
	}
}
