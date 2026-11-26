// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const linkDeliveryNamespace = "link-ns"

type deprovisionRecorder struct {
	broker.Broker

	mu             sync.Mutex
	queues         []string
	publishErr     error
	deprovisionErr error
}

func (b *deprovisionRecorder) DeprovisionQueue(name string) error {
	b.mu.Lock()
	b.queues = append(b.queues, name)
	failure := b.deprovisionErr
	b.mu.Unlock()
	if failure != nil {
		return failure
	}
	return b.Broker.DeprovisionQueue(name)
}

func (b *deprovisionRecorder) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	failure := b.publishErr
	b.mu.Unlock()
	if failure != nil {
		return failure
	}
	return b.Broker.Publish(ctx, topic, msg)
}

func (b *deprovisionRecorder) failPublishWith(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publishErr = err
}

func (b *deprovisionRecorder) failDeprovisionWith(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deprovisionErr = err
}

func (b *deprovisionRecorder) deprovisioned() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.queues)
}

type echoGWE struct {
	arrived chan struct{}
	settled chan struct{}

	mu       sync.Mutex
	failure  string
	attempt  string
	nudges   [][]byte
	hold     <-chan struct{}
	answered int
}

func (g *echoGWE) noteAnswered() {
	g.mu.Lock()
	g.answered++
	g.mu.Unlock()
	select {
	case g.settled <- struct{}{}:
	default:
	}
}

func (g *echoGWE) answeredAtLeast(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		g.mu.Lock()
		got := g.answered
		g.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-g.settled:
		case <-timeout:
			t.Fatalf("the Executor's status reached the Platform for %d entrypoint changes, want %d", got, n)
		}
	}
}

func (g *echoGWE) holdNextReply(until <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hold = until
}

func (g *echoGWE) takeHold() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	until := g.hold
	g.hold = nil
	return until
}

func (g *echoGWE) receivedAtLeast(t *testing.T, n int) [][]byte {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		if got := g.received(); len(got) >= n {
			return got
		}
		select {
		case <-g.arrived:
		case <-timeout:
			t.Fatalf("the Executor received %d entrypoint changes, want %d", len(g.received()), n)
		}
	}
}

func (g *echoGWE) failWith(failure string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failure = failure
}

func (g *echoGWE) received() [][]byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.nudges)
}

func (g *echoGWE) applied(payload []byte) control.GatewayStatus {
	var apply control.EntrypointApply
	_ = json.Unmarshal(payload, &apply)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nudges = append(g.nudges, payload)
	if apply.Attempt != "" {
		g.attempt = apply.Attempt
	}
	select {
	case g.arrived <- struct{}{}:
	default:
	}
	return control.GatewayStatus{ProcessID: "gwe-1", Entrypoints: []control.EntrypointStatus{
		{Name: apply.Name, DeploymentID: apply.DeploymentID, Revision: apply.Revision, Attempt: g.attempt, Error: g.failure},
	}}
}

func runEchoGWE(t *testing.T, brk broker.Broker, tracker *gweStatusTracker) *echoGWE {
	t.Helper()
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the Executor's subscription: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.EntrypointApplyTopic(linkDeliveryNamespace)); err != nil {
		t.Fatalf("subscribe the Executor: %v", err)
	}
	gwe := &echoGWE{arrived: make(chan struct{}, 64), settled: make(chan struct{}, 64)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			msg, err := brk.Receive(ctx, queue)
			if ctx.Err() != nil {
				return
			}
			if err != nil || msg == nil {
				continue
			}
			msg.Ack()
			status := gwe.applied(msg.Payload)
			if until := gwe.takeHold(); until != nil {
				go func() {
					select {
					case <-until:
						tracker.observe(status)
						gwe.noteAnswered()
					case <-ctx.Done():
					}
				}()
				continue
			}
			tracker.observe(status)
			gwe.noteAnswered()
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = brk.DeleteQueue(queue.Name())
	})
	return gwe
}

type linkDeliveryFixture struct {
	*linkAgentFixture
	brk         *deprovisionRecorder
	gwe         *echoGWE
	yamlSvc     *YAMLService
	gatewaySvc  *GatewayService
	gwDeploySvc *GatewayDeploymentService
	deploySvc   *DeploymentService
	logger      *slog.Logger
}

func newLinkDeliveryFixture(t *testing.T) *linkDeliveryFixture {
	t.Helper()
	base := newLinkAgentFixture(t)
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect the dev broker: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	brk := &deprovisionRecorder{Broker: dev}

	settled := time.Unix(1_767_322_800, 0)
	tracker := newGWEStatusTracker(brk, linkDeliveryNamespace)
	tracker.listening = settled
	tracker.now = func() time.Time { return settled.Add(gweStatusSettle) }

	logger := base.logger
	flags := features.NewRegistry()
	flags.Register(features.Definition{Key: features.KeyAgentLinkEntrypoint, Name: "Agent Link Entrypoint", ReleasePhase: features.PhaseEarlyAccess, Default: true})
	yamlSvc := NewYAMLService(linkDeliveryNamespace, logger)
	controlClient := control.NewClient(brk, linkDeliveryNamespace)
	gateways := store.NewGatewayRepo(base.db)
	gwDeployments := store.NewGatewayDeploymentRepo(base.db)
	gwDeploySvc := NewGatewayDeploymentService(gwDeployments, gateways, yamlSvc, controlClient, logger)
	gwDeploySvc.gwes = tracker

	agents := store.NewAgentRepo(base.db)
	connectors := store.NewConnectorRepo(base.db)
	registry := NewDefaultDeployerRegistry(yamlSvc, connectors, nil, nil, nil, linkDeliveryNamespace, logger)
	registry.Register(AgentTypeLink, NewLinkAgentDeployer(store.NewLinkAgentRepo(base.db), yamlSvc, logger))
	deploySvc := NewDeploymentService(store.NewDeploymentRepo(base.db), agents, connectors, nil, yamlSvc, controlClient, registry, logger)
	deploySvc.links = &linkAgentRollout{db: base.db, yamlSvc: yamlSvc, gwes: tracker, logger: logger}

	return &linkDeliveryFixture{
		linkAgentFixture: base,
		brk:              brk,
		gwe:              runEchoGWE(t, brk, tracker),
		yamlSvc:          yamlSvc,
		gatewaySvc:       NewGatewayService(gateways, gwDeployments, nil, controlClient, flags, logger),
		gwDeploySvc:      gwDeploySvc,
		deploySvc:        deploySvc,
		logger:           logger,
	}
}

func (f *linkDeliveryFixture) rollEntrypoint(t *testing.T, entrypointID, action string) string {
	t.Helper()
	resp, err := f.gwDeploySvc.Deploy(t.Context(), CreateGatewayDeploymentRequest{GatewayID: entrypointID, Action: action}, "alice@example.com")
	if err != nil {
		t.Fatalf("%s entrypoint %s: %v", action, entrypointID, describeValidationError(err))
	}
	return resp.ID
}

func (f *linkDeliveryFixture) rollAgent(t *testing.T, agentID, action string) string {
	t.Helper()
	resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agentID, Action: action}, "alice@example.com")
	if err != nil {
		t.Fatalf("%s link agent %s: %v", action, agentID, describeValidationError(err))
	}
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("%s link agent %s recorded %s: %s", action, agentID, resp.Status, resp.ErrorMessage)
	}
	return resp.ID
}

func (f *linkDeliveryFixture) desired(t *testing.T, entrypointID string) *control.EntrypointApply {
	t.Helper()
	apply, err := DeployedEntrypoint(t.Context(), f.db, f.yamlSvc, f.logger, entrypointID)
	if err != nil {
		t.Fatalf("DeployedEntrypoint(%s) = %v", entrypointID, err)
	}
	return apply
}

func revisionOf(pairs ...string) string {
	if len(pairs) == 0 {
		return ""
	}
	sorted := slices.Sorted(slices.Values(pairs))
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])[:32]
}

func mergedAgentNames(t *testing.T, apply *control.EntrypointApply) []string {
	t.Helper()
	if apply == nil {
		t.Fatal("the entrypoint has no desired state")
	}
	parsed, err := agentlink.ParseConfig(entrypoint.AdapterConfig(apply.AppConfig))
	if err != nil {
		t.Fatalf("agentlink.ParseConfig(merged adapter_config) = %v", err)
	}
	names := make([]string, 0, len(parsed.Agents))
	for name := range parsed.Agents {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestLinkAgentsRevision_IsAStableHashOfItsSortedAgentAndDeploymentPairs(t *testing.T) {
	alice := deployedLinkAgent{agentID: "a1", deploymentID: "d1"}
	bob := deployedLinkAgent{agentID: "b2", deploymentID: "d2"}
	tests := []struct {
		name     string
		deployed []deployedLinkAgent
		want     string
	}{
		{name: "no link agent", deployed: nil, want: ""},
		{name: "two link agents", deployed: []deployedLinkAgent{alice, bob}, want: "3343b43162053603451d18263a9ffad6"},
		{name: "the same two listed the other way round", deployed: []deployedLinkAgent{bob, alice}, want: "3343b43162053603451d18263a9ffad6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linkAgentsRevision(tt.deployed); got != tt.want {
				t.Errorf("linkAgentsRevision(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestGWEStatusTracker_JudgesAnAttemptOnEveryLiveGWE(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tr := newGWEStatusTracker(nil, "ns")
	tr.now = func() time.Time { return now }
	tr.listening = now.Add(-gweStatusSettle)
	report := func(process, deploymentID, revision, attempt, failure string) {
		tr.observe(control.GatewayStatus{ProcessID: process, Entrypoints: []control.EntrypointStatus{{Name: "ep", DeploymentID: deploymentID, Revision: revision, Attempt: attempt, Error: failure}}})
	}
	check := func(step string, wantDone bool, wantFailure string) {
		t.Helper()
		if done, failure := tr.attemptOutcome("ep", "r2", "a2"); done != wantDone || failure != wantFailure {
			t.Fatalf("%s: attemptOutcome = %v, %q, want %v, %q", step, done, failure, wantDone, wantFailure)
		}
	}

	check("no GWE has reported", false, "")
	report("A", "d1", "r1", "a1", "")
	check("the only GWE still runs the previous revision", false, "")
	report("A", "d1", "r2", "a2", "")
	check("the only GWE runs it", true, "")
	report("B", "d1", "r1", "a1", "an older failure")
	check("a second GWE failed on the previous revision", false, "")
	report("B", "d1", "r2", "a1", "an earlier attempt's failure")
	check("the second GWE's failure belongs to an earlier attempt at the same revision", false, "")
	report("B", "d1", "r2", "a2", "agent bob refused")
	check("the second GWE could not apply it", false, "agent bob refused")
	report("B", "d2", "r2", "a2", "")
	check("both GWEs run it, one under a newer entrypoint deployment", true, "")
	report("B", "d2", "r2", "", "")
	check("a GWE that applied the revision by its own poll counts by its revision", true, "")
	report("B", "d2", "r2", "", "a failure the GWE hit by its own poll")
	check("a failure the GWE hit by its own poll waits for this attempt's own result", false, "")
	report("B", "d2", "r2", "a2", "")
	check("the GWE then applies it under this attempt", true, "")
	report("B", "d2", "r1", "", "")
	check("a GWE back on the previous revision without an attempt has not applied this one", false, "")
}

func TestDeployedEntrypoint_MergesTheLinkAgentsDeployedOnIt(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	entrypointDeployment := f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	f.rollEntrypoint(t, linkFixtureOtherEntrypointID, DeployActionDeploy)
	claims := claimsDesk()
	claims.Owner, claims.Peers, claims.Wake = "alice@example.com", []string{"policy-desk"}, wakeWithOAuth()
	deployed, deployedToken := f.create(t, claims)
	disabled, disabledToken := f.create(t, CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID, Enabled: new(false)})
	neverDeployed, _ := f.create(t, CreateLinkAgentRequest{Name: "Draft Desk", EntrypointID: linkFixtureEntrypointID})
	elsewhere, _ := f.create(t, CreateLinkAgentRequest{Name: "Policy Desk", EntrypointID: linkFixtureOtherEntrypointID})
	firstDeployment := f.rollAgent(t, deployed.ID, DeployActionDeploy)
	secondDeployment := f.rollAgent(t, disabled.ID, DeployActionDeploy)
	f.rollAgent(t, elsewhere.ID, DeployActionDeploy)

	apply := f.desired(t, linkFixtureEntrypointID)

	if apply.Name != linkFixtureEntrypointID || apply.DeploymentID != entrypointDeployment || apply.Undeploy {
		t.Errorf("desired entrypoint = name %q deployment %q undeploy %t, want the entrypoint's own deployment", apply.Name, apply.DeploymentID, apply.Undeploy)
	}
	if want := revisionOf(deployed.ID+"="+firstDeployment, disabled.ID+"="+secondDeployment); apply.Revision != want {
		t.Errorf("desired entrypoint revision = %q, want %q for its two deployed link agents", apply.Revision, want)
	}
	parsed, err := agentlink.ParseConfig(entrypoint.AdapterConfig(apply.AppConfig))
	if err != nil {
		t.Fatalf("agentlink.ParseConfig(merged adapter_config) = %v", err)
	}
	deployedDigest, _ := hex.DecodeString(digestOf(deployedToken))
	disabledDigest, _ := hex.DecodeString(digestOf(disabledToken))
	wantAgents := map[string]*agentlink.AgentConfig{
		meshNameOf(deployed.ID): {
			Name: meshNameOf(deployed.ID), TokenSHA256: deployedDigest, Enabled: true, DisplayName: "Claims Desk",
			Owner: "alice@example.com", Peers: []string{"policy-desk"},
			Wake: &agentlink.WakeConfig{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, Headers: map[string]string{}, Timeout: 15 * time.Minute, OnStart: true},
		},
		meshNameOf(disabled.ID): {
			Name: meshNameOf(disabled.ID), TokenSHA256: disabledDigest, Enabled: false, DisplayName: "Billing Desk",
		},
	}
	if diff := cmp.Diff(wantAgents, parsed.Agents); diff != "" {
		t.Errorf("link agents the adapter parses from the merged config mismatch (-want +got):\n%s", diff)
	}
	if !parsed.PlatformManaged {
		t.Error("merged adapter_config is not platform_managed")
	}
	if slices.Contains(mergedAgentNames(t, apply), meshNameOf(neverDeployed.ID)) {
		t.Error("a link agent that was never deployed is in the merged config")
	}

	all, err := DeployedEntrypoints(t.Context(), f.db, f.yamlSvc, f.logger)
	if err != nil {
		t.Fatalf("DeployedEntrypoints() = %v", err)
	}
	listed := map[string][]string{}
	revisions := map[string]string{}
	for i := range all {
		listed[all[i].Name] = mergedAgentNames(t, &all[i])
		revisions[all[i].Name] = all[i].Revision
	}
	wantListed := map[string][]string{
		linkFixtureEntrypointID:      mergedAgentNames(t, apply),
		linkFixtureOtherEntrypointID: {meshNameOf(elsewhere.ID)},
	}
	if diff := cmp.Diff(wantListed, listed); diff != "" {
		t.Errorf("DeployedEntrypoints() link agents by entrypoint mismatch (-want +got):\n%s", diff)
	}
	if revisions[linkFixtureEntrypointID] != apply.Revision {
		t.Errorf("DeployedEntrypoints() revision = %q, want the %q DeployedEntrypoint gives", revisions[linkFixtureEntrypointID], apply.Revision)
	}
}

func TestDeployedEntrypoint_RevisionFollowsEachLinkAgentDeployment(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	alice, _ := f.create(t, CreateLinkAgentRequest{Name: "Alice Agent", EntrypointID: linkFixtureEntrypointID})
	bob, _ := f.create(t, CreateLinkAgentRequest{Name: "Bob Agent", EntrypointID: linkFixtureEntrypointID})
	check := func(step string, wantNames []string, wantRevision string) {
		t.Helper()
		apply := f.desired(t, linkFixtureEntrypointID)
		if diff := cmp.Diff(wantNames, mergedAgentNames(t, apply)); diff != "" {
			t.Errorf("%s: merged link agents mismatch (-want +got):\n%s", step, diff)
		}
		if apply.Revision != wantRevision {
			t.Errorf("%s: revision = %q, want %q", step, apply.Revision, wantRevision)
		}
	}

	check("no link agent deployed", []string{}, "")

	aliceFirst := f.rollAgent(t, alice.ID, DeployActionDeploy)
	check("alice deployed", []string{meshNameOf(alice.ID)}, revisionOf(alice.ID+"="+aliceFirst))
	check("read again with nothing changed", []string{meshNameOf(alice.ID)}, revisionOf(alice.ID+"="+aliceFirst))

	bobFirst := f.rollAgent(t, bob.ID, DeployActionDeploy)
	both := slices.Sorted(slices.Values([]string{meshNameOf(alice.ID), meshNameOf(bob.ID)}))
	check("bob deployed too", both, revisionOf(alice.ID+"="+aliceFirst, bob.ID+"="+bobFirst))

	aliceSecond := f.rollAgent(t, alice.ID, DeployActionUpdate)
	check("alice deployed again", both, revisionOf(alice.ID+"="+aliceSecond, bob.ID+"="+bobFirst))

	f.rollAgent(t, alice.ID, DeployActionUndeploy)
	check("alice undeployed", []string{meshNameOf(bob.ID)}, revisionOf(bob.ID+"="+bobFirst))

	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionUpdate)
	check("the entrypoint deployed again", []string{meshNameOf(bob.ID)}, revisionOf(bob.ID+"="+bobFirst))
}

func TestDeployedEntrypoint_CountsLinkAgentDeploymentsInFlight(t *testing.T) {
	tests := []struct {
		name             string
		recorded         []string
		inFlight         string
		wantMerged       bool
		wantFromInFlight bool
	}{
		{name: "a deploy in flight on an agent never deployed", recorded: nil, inFlight: DeployActionDeploy, wantMerged: true, wantFromInFlight: true},
		{name: "an update in flight on a deployed agent", recorded: []string{DeployActionDeploy}, inFlight: DeployActionUpdate, wantMerged: true, wantFromInFlight: true},
		{name: "an undeploy in flight on a deployed agent", recorded: []string{DeployActionDeploy}, inFlight: DeployActionUndeploy, wantMerged: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, _ := f.create(t, claimsDesk())
			for _, action := range tt.recorded {
				f.rollAgent(t, agent.ID, action)
			}
			pending := store.AgentDeployment{ID: "0190f5ec-0000-7000-8000-0000000d0f01", AgentID: agent.ID, Action: tt.inFlight,
				AgentConfigSnapshot: new(claimsDeskSnapshot(agent.ID, "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"))}
			body, _ := json.Marshal(agentIntent{Deployment: pending})
			if err := store.NewDeploymentIntentRepo(f.db).Put(t.Context(), &store.DeploymentIntent{ID: pending.ID, Kind: store.IntentKindAgent, ObjectID: agent.ID, Payload: string(body)}); err != nil {
				t.Fatalf("record the deployment in flight: %v", err)
			}

			apply := f.desired(t, linkFixtureEntrypointID)

			merged := slices.Contains(mergedAgentNames(t, apply), meshNameOf(agent.ID))
			if merged != tt.wantMerged {
				t.Errorf("link agent merged with a %s in flight = %t, want %t", tt.inFlight, merged, tt.wantMerged)
			}
			fromInFlight := apply.Revision == revisionOf(agent.ID+"="+pending.ID)
			if fromInFlight != tt.wantFromInFlight {
				t.Errorf("revision %q carries the deployment in flight = %t, want %t", apply.Revision, fromInFlight, tt.wantFromInFlight)
			}
		})
	}
}

func TestDeploymentService_LinkAgentDeployPublishesTheEntrypointsEffectiveConfigAndWaitsForItsRevision(t *testing.T) {
	t.Setenv("FRONTEND_USE_AUTHORIZATION", "")
	f := newLinkDeliveryFixture(t)
	entrypointDeployment := f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	req := claimsDesk()
	req.Wake = wakeWithOAuth()
	agent, token := f.create(t, req)
	before := len(f.gwe.received())

	deploymentID := f.rollAgent(t, agent.ID, DeployActionDeploy)

	nudges := f.gwe.received()[before:]
	if len(nudges) != 1 {
		t.Fatalf("deploy published %d entrypoint changes, want 1", len(nudges))
	}
	wantPublished := `{"name":"` + linkFixtureEntrypointID + `","deployment_id":"` + entrypointDeployment +
		`","app_config":{"gateway_adapter":{"adapter_config":{"agents":[{"display_name":"Claims Desk","enabled":true,"name":"` + meshNameOf(agent.ID) +
		`","share_user_profile":false,"token_sha256":"` + digestOf(token) + `","wake":{"client_id":"claims-client","client_secret":"` + linkFixtureWakeSecret +
		`","on_start":true,"token_url":"` + linkFixtureTokenURL + `","url":"https://runtime.example.com/invocations"}}],"platform_managed":true},"type":"agent_link"},` +
		`"gateway_id":"partner-link","gateway_uuid":"` + linkFixtureEntrypointID + `","namespace":"link-ns","scheduler_service":{"enabled":false}},` +
		`"revision":"` + revisionOf(agent.ID+"="+deploymentID) + `"}`
	var sent control.EntrypointApply
	if err := json.Unmarshal(nudges[0], &sent); err != nil || !idgen.IsCanonical(sent.Attempt) {
		t.Fatalf("the published entrypoint change carries attempt %q (%v), want a fresh id", sent.Attempt, err)
	}
	sent.Attempt = ""
	published, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("encode the published entrypoint change: %v", err)
	}
	if string(published) != wantPublished {
		t.Error("the published entrypoint change is not the entrypoint's full config with the deployed link agent merged in")
	}
	whatTheReaderReturns, err := json.Marshal(f.desired(t, linkFixtureEntrypointID))
	if err != nil || string(published) != string(whatTheReaderReturns) {
		t.Errorf("the published entrypoint change differs from what DeployedEntrypoint returns for it (%v)", err)
	}
	if strings.Contains(string(nudges[0]), token) {
		t.Error("the published entrypoint change carries the token itself")
	}
	read, err := f.svc.GetByID(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	if read.DeploymentStatus != "deployed" || read.SyncStatus == nil || *read.SyncStatus != "in_sync" || read.RuntimeStatus.Status() != "disconnected" {
		t.Errorf("deployed link agent with no agent connected = deployment %q sync %v runtime %q, want deployed, in_sync, disconnected",
			read.DeploymentStatus, read.SyncStatus, read.RuntimeStatus.Status())
	}
	wantDeployed := &DeployedLinkAgentInfo{
		Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID, TokenPrefix: token[5:13], Enabled: true, Peers: []string{},
		Wake: &DeployedLinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL, ClientID: "claims-client",
			HeaderNames: []string{}, TimeoutSeconds: 900, OnStart: true},
		DeployedAt: read.Deployed.DeployedAt, DeployedBy: "alice@example.com",
	}
	if diff := cmp.Diff(wantDeployed, read.Deployed); diff != "" {
		t.Errorf("deployed snapshot mismatch (-want +got):\n%s", diff)
	}
	row, err := store.NewAgentDeploymentRepo(f.db).GetByID(t.Context(), deploymentID)
	if err != nil || row == nil || row.AgentDataSnapshot == nil || row.AgentConfigSnapshot == nil {
		t.Fatalf("deployment row = %v, %v, want it with both snapshots", row, err)
	}
	for _, secret := range []string{token, digestOf(token), linkFixtureWakeSecret} {
		if strings.Contains(*row.AgentDataSnapshot, secret) {
			t.Error("the deployment's data snapshot, the one APIs show, carries a token, its digest or the wake client secret")
		}
	}
	wantConfig := `{"entrypointId":"` + linkFixtureEntrypointID + `","agent":{"display_name":"Claims Desk","enabled":true,"name":"` + meshNameOf(agent.ID) +
		`","share_user_profile":false,"token_sha256":"` + digestOf(token) + `","wake":{"client_id":"claims-client","client_secret":"` + linkFixtureWakeSecret +
		`","on_start":true,"token_url":"` + linkFixtureTokenURL + `","url":"https://runtime.example.com/invocations"}}}`
	if *row.AgentConfigSnapshot != wantConfig {
		t.Error("the deployment's config snapshot is not the complete runtime config of the link agent")
	}
}

func claimsDeskSnapshot(agentID, tokenDigest string) string {
	return `{"entrypointId":"` + linkFixtureEntrypointID + `","agent":{"display_name":"Claims Desk","enabled":true,"name":"` + meshNameOf(agentID) +
		`","share_user_profile":false,"token_sha256":"` + tokenDigest + `"}}`
}

func TestDeploymentService_LinkAgentDeployOutcomes(t *testing.T) {
	const entrypointNotDeployedRefusal = `the link agent's entrypoint "Partner link" is not deployed; deploy the entrypoint first`
	tests := []struct {
		name              string
		entrypointActions []string
		gweFailure        string
		wantRefusal       string
		wantStatus        string
		wantFailureReason string
		wantError         string
		wantNudges        int
	}{
		{
			name:              "every Executor applies it",
			entrypointActions: []string{DeployActionDeploy},
			wantStatus:        "success",
			wantNudges:        1,
		},
		{
			name:              "an Executor could not apply it",
			entrypointActions: []string{DeployActionDeploy},
			gweFailure:        "agent claims refused",
			wantStatus:        "failed",
			wantFailureReason: "runtime_error",
			wantError:         "entrypoint failed to start: agent claims refused",
			wantNudges:        2,
		},
		{
			name:              "the entrypoint was never deployed",
			entrypointActions: nil,
			wantRefusal:       entrypointNotDeployedRefusal,
		},
		{
			name:              "the entrypoint was undeployed",
			entrypointActions: []string{DeployActionDeploy, DeployActionUndeploy},
			wantRefusal:       entrypointNotDeployedRefusal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			for _, action := range tt.entrypointActions {
				f.rollEntrypoint(t, linkFixtureEntrypointID, action)
			}
			agent, _ := f.create(t, claimsDesk())
			f.gwe.failWith(tt.gweFailure)
			before := len(f.gwe.received())

			resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionDeploy}, "alice@example.com")

			var refusal string
			if verr, ok := errors.AsType[*ValidationError](err); ok {
				refusal = verr.Message
			} else if err != nil {
				t.Fatalf("deploy = %T %v, want a *ValidationError or nil", err, err)
			}
			if refusal != tt.wantRefusal {
				t.Errorf("deploy refusal = %q, want %q", refusal, tt.wantRefusal)
			}
			var status, failureReason, errorMessage string
			if resp != nil {
				status, failureReason, errorMessage = resp.Status, resp.FailureReason, resp.ErrorMessage
			}
			if status != tt.wantStatus || failureReason != tt.wantFailureReason || errorMessage != tt.wantError {
				t.Errorf("recorded deployment = status %q reason %q error %q, want status %q reason %q error %q",
					status, failureReason, errorMessage, tt.wantStatus, tt.wantFailureReason, tt.wantError)
			}
			if got := len(f.gwe.receivedAtLeast(t, before+tt.wantNudges)) - before; got != tt.wantNudges {
				t.Errorf("deploy published %d entrypoint changes, want %d", got, tt.wantNudges)
			}
		})
	}
}

func TestDeploymentService_LinkAgentUndeployRemovesItsQueuesOnceExecutorsConfirm(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	if got := f.brk.deprovisioned(); len(got) != 0 {
		t.Fatalf("queues removed by a deploy = %v, want none", got)
	}

	if err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com"); err != nil {
		t.Fatalf("Undeploy() = %v", err)
	}

	wantQueues := []string{
		"link-ns/q/a2a/" + meshNameOf(agent.ID),
		"link-ns/q/agentlink/wake/" + meshNameOf(agent.ID),
	}
	if diff := cmp.Diff(wantQueues, f.brk.deprovisioned()); diff != "" {
		t.Errorf("queues removed by the undeploy mismatch (-want +got):\n%s", diff)
	}
	if got := mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID)); len(got) != 0 {
		t.Errorf("link agents merged after the undeploy = %v, want none", got)
	}
	read, err := f.svc.GetByID(t.Context(), agent.ID)
	if err != nil || read.DeploymentStatus != "not_deployed" || read.RuntimeStatus.Status() != "stopped" {
		t.Errorf("undeployed link agent = %+v, %v, want not_deployed and stopped", read, err)
	}
}

func TestDeploymentService_AFailedLinkAgentUndeployKeepsItsQueues(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.gwe.failWith("agent claims would not stop")

	err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com")

	if err == nil {
		t.Error("Undeploy() = nil with an Executor that could not apply it, want an error")
	}
	if got := f.brk.deprovisioned(); len(got) != 0 {
		t.Errorf("queues removed by an undeploy no Executor confirmed = %v, want none", got)
	}
}

func TestLinkAgentService_DeleteRemovesTheQueuesAFailedUndeployKept(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.gwe.failWith("agent claims would not stop")
	if err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com"); err == nil {
		t.Fatal("Undeploy() = nil with an Executor that could not apply it, want an error")
	}
	if got := f.brk.deprovisioned(); len(got) != 0 {
		t.Fatalf("queues removed by an undeploy no Executor confirmed = %v, want none", got)
	}
	f.gwe.failWith("")

	if err := f.svc.DeleteWithUndeploy(t.Context(), agent.ID, "alice@example.com", f.deploySvc); err != nil {
		t.Fatalf("DeleteWithUndeploy() = %v", err)
	}

	wantQueues := []string{
		"link-ns/q/a2a/" + meshNameOf(agent.ID),
		"link-ns/q/agentlink/wake/" + meshNameOf(agent.ID),
	}
	if diff := cmp.Diff(wantQueues, f.brk.deprovisioned()); diff != "" {
		t.Errorf("queues removed once the undeploy is retried mismatch (-want +got):\n%s", diff)
	}
}

func TestDeploymentService_ALinkAgentUndeployRetriedAfterAFailureIsJudgedByItsOwnAttempt(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.gwe.failWith("agent claims would not stop")
	firstTry := len(f.gwe.received())
	if err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com"); err == nil {
		t.Fatal("Undeploy() = nil with an Executor that could not apply it, want an error")
	}
	f.gwe.answeredAtLeast(t, firstTry+2)
	f.gwe.failWith("")
	before := len(f.gwe.received())
	retryReplied := make(chan struct{})
	f.gwe.holdNextReply(retryReplied)
	var executorAnswered atomic.Bool
	type outcome struct {
		err           error
		afterTheReply bool
	}
	retried := make(chan outcome, 1)

	go func() {
		err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com")
		retried <- outcome{err: err, afterTheReply: executorAnswered.Load()}
	}()
	f.gwe.receivedAtLeast(t, before+1)
	executorAnswered.Store(true)
	close(retryReplied)

	select {
	case got := <-retried:
		if !got.afterTheReply {
			t.Errorf("Undeploy() returned %v before any Executor answered the retry; the failure of the previous attempt was taken for the retry's", got.err)
		}
		if got.err != nil {
			t.Errorf("Undeploy() retried after a failure = %v, want success once the Executor applies it", got.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the retried undeploy did not finish")
	}
	if got := f.brk.deprovisioned(); len(got) != 2 {
		t.Errorf("queues removed by the retried undeploy = %v, want the agent's two", got)
	}
}

func TestDeploymentService_LinkAgentRolloutsOnOneEntrypointRunOneAtATime(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	leader := newDeployLeader(nil, store.NewDeploymentIntentRepo(f.db), f.logger)
	f.deploySvc.leader = leader
	lockEntrypoint := leader.lockEntrypoint
	askedToSerialize := make(chan struct{}, 2)
	f.deploySvc.links.serialize = func(ctx context.Context, entrypointID string) (func(), error) {
		askedToSerialize <- struct{}{}
		return lockEntrypoint(ctx, entrypointID)
	}
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	claims, _ := f.create(t, claimsDesk())
	billing, _ := f.create(t, CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID})
	before := len(f.gwe.received())
	firstPublishAcknowledged := make(chan struct{})
	f.gwe.holdNextReply(firstPublishAcknowledged)
	rollouts := make(chan error, 2)
	deploy := func(agentID string) {
		resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}, "alice@example.com")
		if err == nil && resp.Status != DeployStatusSuccess {
			err = errors.New("deployment recorded " + resp.Status + ": " + resp.ErrorMessage)
		}
		rollouts <- err
	}

	go deploy(claims.ID)
	f.gwe.receivedAtLeast(t, before+1)
	go deploy(billing.ID)
	for range 2 {
		select {
		case <-askedToSerialize:
		case <-time.After(10 * time.Second):
			t.Fatal("a link agent rollout did not reach its entrypoint's lock")
		}
	}

	if got := len(f.gwe.received()) - before; got != 1 {
		t.Errorf("entrypoint changes published while the first rollout awaits its Executor = %d, want 1: the second rollout must wait", got)
	}
	close(firstPublishAcknowledged)
	for range 2 {
		select {
		case err := <-rollouts:
			if err != nil {
				t.Errorf("a link agent rollout = %v, want success", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a link agent rollout did not finish")
		}
	}

	published := f.gwe.received()[before:]
	if len(published) != 2 {
		t.Fatalf("entrypoint changes published by two rollouts = %d, want 2", len(published))
	}
	var last control.EntrypointApply
	if err := json.Unmarshal(published[1], &last); err != nil {
		t.Fatalf("decode the last published entrypoint change: %v", err)
	}
	wantNames := []string{meshNameOf(claims.ID), meshNameOf(billing.ID)}
	slices.Sort(wantNames)
	if diff := cmp.Diff(wantNames, mergedAgentNames(t, &last)); diff != "" {
		t.Errorf("link agents in the last published entrypoint change mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantNames, mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID))); diff != "" {
		t.Errorf("link agents in the delivered entrypoint mismatch (-want +got):\n%s", diff)
	}
}

func TestLinkAgentService_DeleteWithUndeployKeepsALinkAgentItCannotUndeploy(t *testing.T) {
	tests := []struct {
		name        string
		deployed    bool
		noDeploySvc bool
		gweFailure  string
		wantRefused bool
		wantStored  int
	}{
		{name: "a deployed agent an Executor will not stop is kept", deployed: true, gweFailure: "agent claims would not stop", wantRefused: true, wantStored: 1},
		{name: "a deployed agent with no deployment service is kept", deployed: true, noDeploySvc: true, wantRefused: true, wantStored: 1},
		{name: "an agent never deployed is deleted with no deployment service", noDeploySvc: true, wantStored: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, _ := f.create(t, claimsDesk())
			if tt.deployed {
				f.rollAgent(t, agent.ID, DeployActionDeploy)
			}
			f.gwe.failWith(tt.gweFailure)
			deploySvc := f.deploySvc
			if tt.noDeploySvc {
				deploySvc = nil
			}

			err := f.svc.DeleteWithUndeploy(t.Context(), agent.ID, "alice@example.com", deploySvc)

			if _, ok := errors.AsType[*ExternalServiceError](err); ok != tt.wantRefused {
				t.Errorf("DeleteWithUndeploy() = %T %v, want an *ExternalServiceError: %t", err, err, tt.wantRefused)
			}
			if err != nil && !tt.wantRefused {
				t.Errorf("DeleteWithUndeploy() = %v, want nil", err)
			}
			if linkAgents, mirrors := countLinkAgents(t, f.db); linkAgents != tt.wantStored || mirrors != tt.wantStored {
				t.Errorf("after DeleteWithUndeploy: %d link agents and %d mirrors stored, want %d of each", linkAgents, mirrors, tt.wantStored)
			}
			if got := f.brk.deprovisioned(); tt.wantRefused && len(got) != 0 {
				t.Errorf("queues removed by a refused delete = %v, want none", got)
			}
		})
	}
}

func TestDeploymentService_ALinkAgentCannotBeRolledBack(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	first := f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.rollAgent(t, agent.ID, DeployActionUpdate)

	_, err := f.deploySvc.Rollback(t.Context(), CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionUpdate}, first, "alice@example.com")

	verr, ok := errors.AsType[*ValidationError](err)
	if !ok || verr.Message != "a link agent can't be rolled back" || verr.Field != "deploymentId" {
		t.Errorf("Rollback() = %v, want a refusal on deploymentId saying a link agent can't be rolled back", err)
	}
}

func TestLinkAgentService_DeleteWithUndeployUndeploysThenDeletes(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)

	if err := f.svc.DeleteWithUndeploy(t.Context(), agent.ID, "alice@example.com", f.deploySvc); err != nil {
		t.Fatalf("DeleteWithUndeploy() = %v", err)
	}

	if linkAgents, mirrors := countLinkAgents(t, f.db); linkAgents != 0 || mirrors != 0 {
		t.Errorf("after DeleteWithUndeploy: %d link agents and %d mirrors stored, want none", linkAgents, mirrors)
	}
	if got := f.brk.deprovisioned(); len(got) != 2 {
		t.Errorf("queues removed by the undeploy before the delete = %v, want the agent's two queues", got)
	}
}

func TestEntrypoint_UndeployAndDeleteAreRefusedWhileLinkAgentsUseIt(t *testing.T) {
	const (
		deployedRefusal = "this entrypoint while link agents are deployed on it: Billing Desk, Claims Desk; undeploy them first"
		assignedRefusal = "cannot delete this entrypoint while link agents connect through it: Billing Desk, Claims Desk; delete them or move them to another entrypoint first"
	)
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	claims, _ := f.create(t, claimsDesk())
	billing, _ := f.create(t, CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID})
	f.rollAgent(t, claims.ID, DeployActionDeploy)
	f.rollAgent(t, billing.ID, DeployActionDeploy)
	refusalOf := func(err error) string {
		t.Helper()
		verr, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("error = %T %v, want a *ValidationError", err, err)
		}
		return verr.Message
	}

	_, undeployErr := f.gwDeploySvc.Deploy(t.Context(), CreateGatewayDeploymentRequest{GatewayID: linkFixtureEntrypointID, Action: DeployActionUndeploy}, "alice@example.com")
	deleteErr := f.gatewaySvc.DeleteWithUndeploy(t.Context(), linkFixtureEntrypointID, "alice@example.com", f.gwDeploySvc)

	if got := refusalOf(undeployErr); got != "cannot undeploy "+deployedRefusal {
		t.Errorf("entrypoint undeploy refusal = %q, want %q", got, "cannot undeploy "+deployedRefusal)
	}
	if got := refusalOf(deleteErr); got != "cannot delete "+deployedRefusal {
		t.Errorf("entrypoint delete refusal = %q, want %q", got, "cannot delete "+deployedRefusal)
	}
	if got := mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID)); len(got) != 2 {
		t.Errorf("link agents merged after the refused undeploy = %v, want both still deployed", got)
	}

	f.rollAgent(t, claims.ID, DeployActionUndeploy)
	f.rollAgent(t, billing.ID, DeployActionUndeploy)
	plainDeleteErr := f.gatewaySvc.Delete(t.Context(), linkFixtureEntrypointID)
	if got := refusalOf(plainDeleteErr); got != assignedRefusal {
		t.Errorf("entrypoint delete refusal with undeployed link agents = %q, want %q", got, assignedRefusal)
	}

	for _, id := range []string{claims.ID, billing.ID} {
		if err := f.svc.Delete(t.Context(), id); err != nil {
			t.Fatalf("delete link agent %s: %v", id, err)
		}
	}
	if err := f.gatewaySvc.DeleteWithUndeploy(t.Context(), linkFixtureEntrypointID, "alice@example.com", f.gwDeploySvc); err != nil {
		t.Errorf("entrypoint delete with no link agent left = %v, want it deleted", describeValidationError(err))
	}
}

func TestEntrypointRedeploy_PublishesItsLinkAgentsAgain(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	agentDeployment := f.rollAgent(t, agent.ID, DeployActionDeploy)
	before := len(f.gwe.received())

	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionUpdate)

	published := f.gwe.received()[before:]
	if len(published) != 1 {
		t.Fatalf("entrypoint redeploy published %d changes, want 1", len(published))
	}
	var apply control.EntrypointApply
	if err := json.Unmarshal(published[0], &apply); err != nil {
		t.Fatalf("decode the published change: %v", err)
	}
	if diff := cmp.Diff([]string{meshNameOf(agent.ID)}, mergedAgentNames(t, &apply)); diff != "" {
		t.Errorf("link agents in the published redeploy mismatch (-want +got):\n%s", diff)
	}
	if want := revisionOf(agent.ID + "=" + agentDeployment); apply.Revision != want {
		t.Errorf("published redeploy revision = %q, want %q", apply.Revision, want)
	}
}

func TestReconciler_LeavesLinkAgentsOutOfTheAWEAnswer(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	deploymentID := f.rollAgent(t, agent.ID, DeployActionDeploy)
	if row, err := store.NewAgentDeploymentRepo(f.db).GetByID(t.Context(), deploymentID); err != nil || row == nil || row.Status != DeployStatusSuccess {
		t.Fatalf("link agent deployment row = %+v, %v, want success", row, err)
	}
	reconciler := NewReconciler(store.NewAgentRepo(f.db), store.NewAgentDeploymentRepo(f.db), nil, nil, nil, nil, nil, nil, nil, nil, "ns", f.logger)
	reconciler.SetDeployerRegistry(f.deploySvc.deployers)

	listed, err := reconciler.desiredAWEAgents(t.Context())

	if err != nil {
		t.Fatalf("desiredAWEAgents: %v", err)
	}
	for _, a := range listed {
		if a.DeploymentID == deploymentID {
			t.Errorf("the AWE answer lists the link agent %s, want it left to its entrypoint", a.Name)
		}
	}
}

func TestFinishInFlight_RecordsALinkAgentDeployItsLeaderDidNot(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	intents := store.NewDeploymentIntentRepo(f.db)
	left := store.AgentDeployment{ID: idgen.New(), AgentID: agent.ID, Action: DeployActionDeploy, CreatedBy: "alice@example.com",
		AgentConfigSnapshot: new(claimsDeskSnapshot(agent.ID, f.stored(t, agent.ID).TokenSHA256))}
	body, _ := json.Marshal(agentIntent{Deployment: left})
	if err := intents.Put(t.Context(), &store.DeploymentIntent{ID: left.ID, Kind: store.IntentKindAgent, ObjectID: agent.ID, Payload: string(body)}); err != nil {
		t.Fatalf("record the deployment in flight: %v", err)
	}
	before := len(f.gwe.received())

	leader := newDeployLeader(store.NewDeployRequestRepo(f.db), intents, f.logger)
	finishInFlight(t.Context(), leader, f.deploySvc, nil, nil)

	recorded, err := store.NewAgentDeploymentRepo(f.db).GetByID(t.Context(), left.ID)
	if err != nil || recorded == nil || recorded.Status != DeployStatusSuccess {
		t.Fatalf("the link agent deploy in flight was not recorded as a success: %+v, %v", recorded, err)
	}
	if remaining, _ := intents.List(t.Context(), store.IntentKindAgent); len(remaining) != 0 {
		t.Errorf("%d agent deployments still in flight, want none", len(remaining))
	}
	nudges := f.gwe.received()[before:]
	if len(nudges) != 1 || !strings.Contains(string(nudges[0]), `"revision":"`+revisionOf(agent.ID+"="+left.ID)+`"`) {
		t.Errorf("the new leader published %d entrypoint changes, want one carrying the left deployment's revision", len(nudges))
	}
}

func deliveredAgent(t *testing.T, apply *control.EntrypointApply, meshName string) *agentlink.AgentConfig {
	t.Helper()
	if apply == nil {
		t.Fatal("the entrypoint has no desired state")
	}
	parsed, err := agentlink.ParseConfig(entrypoint.AdapterConfig(apply.AppConfig))
	if err != nil {
		t.Fatalf("agentlink.ParseConfig(merged adapter_config) = %v", err)
	}
	return parsed.Agents[meshName]
}

func TestDeployedEntrypoint_DeliversWhatWasDeployedNotTheCurrentRow(t *testing.T) {
	const rotatedDigest = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	req := claimsDesk()
	req.Peers, req.Wake = []string{"policy-desk"}, wakeWithOAuth()
	agent, token := f.create(t, req)
	firstDeployment := f.rollAgent(t, agent.ID, DeployActionDeploy)
	deployedDigest, _ := hex.DecodeString(digestOf(token))
	wantDeployed := &agentlink.AgentConfig{
		Name: meshNameOf(agent.ID), TokenSHA256: deployedDigest, Enabled: true, DisplayName: "Claims Desk", Peers: []string{"policy-desk"},
		Wake: &agentlink.WakeConfig{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
			ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, Headers: map[string]string{}, Timeout: 15 * time.Minute, OnStart: true},
	}
	check := func(step string, want *agentlink.AgentConfig, wantRevision string) {
		t.Helper()
		apply := f.desired(t, linkFixtureEntrypointID)
		if diff := cmp.Diff(want, deliveredAgent(t, apply, meshNameOf(agent.ID))); diff != "" {
			t.Errorf("%s: DeployedEntrypoint() delivers another config (-want +got):\n%s", step, diff)
		}
		if apply.Revision != wantRevision {
			t.Errorf("%s: DeployedEntrypoint() revision = %q, want %q", step, apply.Revision, wantRevision)
		}
		all, err := DeployedEntrypoints(t.Context(), f.db, f.yamlSvc, f.logger)
		if err != nil || len(all) != 1 {
			t.Fatalf("%s: DeployedEntrypoints() = %d entrypoints, %v, want 1", step, len(all), err)
		}
		if diff := cmp.Diff(want, deliveredAgent(t, &all[0], meshNameOf(agent.ID))); diff != "" {
			t.Errorf("%s: DeployedEntrypoints() delivers another config (-want +got):\n%s", step, diff)
		}
		if all[0].Revision != wantRevision {
			t.Errorf("%s: DeployedEntrypoints() revision = %q, want %q", step, all[0].Revision, wantRevision)
		}
	}
	check("as deployed", wantDeployed, revisionOf(agent.ID+"="+firstDeployment))

	edited := UpdateLinkAgentRequest(claimsDesk())
	edited.Name, edited.Enabled, edited.Peers = "Claims Desk Two", new(false), []string{"billing-desk"}
	edited.Wake = wakeWithOAuth()
	edited.Wake.ClientSecret = "second-secret"
	if _, err := f.svc.Replace(t.Context(), agent.ID, edited, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	if err := store.NewLinkAgentRepo(f.db).SetToken(t.Context(), agent.ID, rotatedDigest, "Zz9yX-wv", "bob@example.com"); err != nil {
		t.Fatalf("SetToken() = %v", err)
	}
	check("after its row was edited and its token digest replaced", wantDeployed, revisionOf(agent.ID+"="+firstDeployment))
	if read, err := f.svc.GetByID(t.Context(), agent.ID); err != nil || read.SyncStatus == nil || *read.SyncStatus != "out_of_sync" {
		t.Errorf("sync status of the edited, deployed link agent = %v, %v, want out_of_sync", read.SyncStatus, err)
	}

	secondDeployment := f.rollAgent(t, agent.ID, DeployActionUpdate)
	newDigest, _ := hex.DecodeString(rotatedDigest)
	wantRedeployed := &agentlink.AgentConfig{
		Name: meshNameOf(agent.ID), TokenSHA256: newDigest, Enabled: false, DisplayName: "Claims Desk Two", Peers: []string{"billing-desk"},
		Wake: &agentlink.WakeConfig{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
			ClientID: "claims-client", ClientSecret: "second-secret", Headers: map[string]string{}, Timeout: 15 * time.Minute, OnStart: true},
	}
	check("after it was deployed again", wantRedeployed, revisionOf(agent.ID+"="+secondDeployment))
}

func TestDeploymentService_AFailedLinkAgentUpdateLeavesTheDeployedConfigDelivered(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	entrypointDeployment := f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, token := f.create(t, claimsDesk())
	firstDeployment := f.rollAgent(t, agent.ID, DeployActionDeploy)
	if _, err := f.svc.Replace(t.Context(), agent.ID, UpdateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID, Peers: []string{"billing-desk"}}, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	f.gwe.failWith("agent claims refused")
	before := len(f.gwe.received())

	resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionUpdate}, "alice@example.com")

	if err != nil || resp.Status != "failed" || resp.FailureReason != "runtime_error" {
		t.Fatalf("update with an Executor that refuses it = %+v, %v, want a recorded runtime_error failure", resp, err)
	}
	apply := f.desired(t, linkFixtureEntrypointID)
	deployedDigest, _ := hex.DecodeString(digestOf(token))
	want := &agentlink.AgentConfig{Name: meshNameOf(agent.ID), TokenSHA256: deployedDigest, Enabled: true, DisplayName: "Claims Desk"}
	if diff := cmp.Diff(want, deliveredAgent(t, apply, meshNameOf(agent.ID))); diff != "" {
		t.Errorf("config delivered after the failed update mismatch (-want +got):\n%s", diff)
	}
	if want := revisionOf(agent.ID + "=" + firstDeployment); apply.Revision != want {
		t.Errorf("revision after the failed update = %q, want the first deployment's %q", apply.Revision, want)
	}
	published := f.gwe.receivedAtLeast(t, before+2)[before:]
	whatTheReaderReturns, err := json.Marshal(apply)
	if err != nil || len(published) != 2 || string(published[1]) != string(whatTheReaderReturns) {
		t.Errorf("after the failed update the entrypoint was told %d changes, want 2 ending with what DeployedEntrypoint returns (%v)", len(published), err)
	}
	var settled control.EntrypointApply
	if err := json.Unmarshal(published[len(published)-1], &settled); err != nil {
		t.Fatalf("decode the last published change: %v", err)
	}
	if got := deliveredAgent(t, &settled, meshNameOf(agent.ID)); got == nil || len(got.Peers) != 0 || settled.DeploymentID != entrypointDeployment {
		t.Errorf("the change published after the failed update does not carry the first deployment's config under the entrypoint's deployment")
	}
}

func TestLinkAgentService_RotateTokenDeploysADeliveredAgentAgain(t *testing.T) {
	tests := []struct {
		name              string
		deployments       []string
		gweFailure        string
		wantNewRows       int
		wantStatus        string
		wantAction        string
		wantFailureReason string
		wantDelivered     string
		wantSync          string
		wantDeployStatus  string
	}{
		{
			name:        "a deployed agent is deployed again and only the new token works",
			deployments: []string{DeployActionDeploy},
			wantNewRows: 1, wantStatus: "success", wantAction: "update",
			wantDelivered: "the new token", wantSync: "in_sync", wantDeployStatus: "deployed",
		},
		{
			name:        "an agent never deployed only has its token replaced",
			deployments: nil,
			wantNewRows: 0, wantDelivered: "no agent", wantDeployStatus: "not_deployed",
		},
		{
			name:        "an undeployed agent only has its token replaced",
			deployments: []string{DeployActionDeploy, DeployActionUndeploy},
			wantNewRows: 0, wantDelivered: "no agent", wantDeployStatus: "not_deployed",
		},
		{
			name:        "a rollout an Executor refuses still returns the token and leaves the previous one working",
			deployments: []string{DeployActionDeploy},
			gweFailure:  "agent claims refused",
			wantNewRows: 1, wantStatus: "failed", wantAction: "update", wantFailureReason: "runtime_error",
			wantDelivered: "the previous token", wantSync: "out_of_sync", wantDeployStatus: "deploy_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, previousToken := f.create(t, claimsDesk())
			for _, action := range tt.deployments {
				f.rollAgent(t, agent.ID, action)
			}
			f.gwe.failWith(tt.gweFailure)
			deployments := store.NewAgentDeploymentRepo(f.db)
			_, rowsBefore, err := deployments.ListPaginated(t.Context(), 1, 100, agent.ID)
			if err != nil {
				t.Fatalf("count deployments: %v", err)
			}

			rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)

			if err != nil {
				t.Fatalf("RotateToken() = %v", err)
			}
			if rotation.Token == previousToken || !strings.HasPrefix(rotation.Token, "samk_") {
				t.Errorf("RotateToken() did not return a new samk_ token")
			}
			if got := f.stored(t, agent.ID).TokenSHA256; got != digestOf(rotation.Token) {
				t.Errorf("stored digest after RotateToken is not the SHA-256 of the token it returned")
			}
			_, rowsAfter, err := deployments.ListPaginated(t.Context(), 1, 100, agent.ID)
			if err != nil {
				t.Fatalf("count deployments: %v", err)
			}
			if got := rowsAfter - rowsBefore; got != tt.wantNewRows {
				t.Errorf("RotateToken() recorded %d deployments, want %d", got, tt.wantNewRows)
			}
			var status, action, failureReason string
			if d := rotation.Deployment; d != nil {
				status, action, failureReason = d.Status, d.Action, d.FailureReason
			}
			if status != tt.wantStatus || action != tt.wantAction || failureReason != tt.wantFailureReason || rotation.DeploymentRefusal != "" {
				t.Errorf("RotateToken() deployment = status %q action %q reason %q refusal %q, want status %q action %q reason %q and no refusal",
					status, action, failureReason, rotation.DeploymentRefusal, tt.wantStatus, tt.wantAction, tt.wantFailureReason)
			}
			delivered := "no agent"
			if got := deliveredAgent(t, f.desired(t, linkFixtureEntrypointID), meshNameOf(agent.ID)); got != nil {
				switch hex.EncodeToString(got.TokenSHA256) {
				case digestOf(rotation.Token):
					delivered = "the new token"
				case digestOf(previousToken):
					delivered = "the previous token"
				default:
					delivered = "another token"
				}
			}
			if delivered != tt.wantDelivered {
				t.Errorf("after RotateToken the entrypoint is delivered %s, want %s", delivered, tt.wantDelivered)
			}
			var sync string
			if rotation.Agent.SyncStatus != nil {
				sync = *rotation.Agent.SyncStatus
			}
			if sync != tt.wantSync || rotation.Agent.DeploymentStatus != tt.wantDeployStatus {
				t.Errorf("RotateToken() agent = sync %q deployment %q, want sync %q deployment %q", sync, rotation.Agent.DeploymentStatus, tt.wantSync, tt.wantDeployStatus)
			}
		})
	}
}

func TestLinkAgentService_RotateTokenSaysWhyADeliveredAgentWasNotDeployedAgain(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, previousToken := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)

	rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", nil)

	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	const wantRefusal = "the new token is stored and no deployment service is available; send an update deployment to make it take effect"
	if rotation.Deployment != nil || rotation.DeploymentRefusal != wantRefusal {
		t.Errorf("RotateToken() with no deployment service = deployment %v refusal %q, want no deployment and %q", rotation.Deployment, rotation.DeploymentRefusal, wantRefusal)
	}
	if rotation.Token == previousToken || f.stored(t, agent.ID).TokenSHA256 != digestOf(rotation.Token) {
		t.Errorf("RotateToken() with no deployment service did not store and return a new token")
	}
	got := deliveredAgent(t, f.desired(t, linkFixtureEntrypointID), meshNameOf(agent.ID))
	if got == nil || hex.EncodeToString(got.TokenSHA256) != digestOf(previousToken) {
		t.Errorf("after a rotation that was not deployed, the entrypoint is not delivered the previous token")
	}
}

func TestLinkAgentService_RefusesADigestADeployedAgentStillServes(t *testing.T) {
	const replacedDigest = "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	claims, token := f.create(t, claimsDesk())
	f.rollAgent(t, claims.ID, DeployActionDeploy)
	served := digestOf(token)
	replaced := UpdateLinkAgentRequest(claimsDesk())
	replaced.TokenSHA256 = replacedDigest
	if _, err := f.svc.Replace(t.Context(), claims.ID, replaced, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	billing := CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID, TokenSHA256: served}

	_, _, err := f.svc.Create(t.Context(), billing, "alice@example.com")

	var refused *ValidationError
	if !errors.As(err, &refused) || len(refused.Details[fieldLinkAgentTokenSHA256]) == 0 {
		t.Fatalf("Create() with the digest the deployed Claims Desk still serves = %v, want a tokenSha256 refusal", describeValidationError(err))
	}

	policy, _ := f.create(t, CreateLinkAgentRequest{Name: "Policy Desk", EntrypointID: linkFixtureEntrypointID})
	toServed := UpdateLinkAgentRequest{Name: "Policy Desk", EntrypointID: linkFixtureEntrypointID, TokenSHA256: served}
	if _, err := f.svc.Replace(t.Context(), policy.ID, toServed, "bob@example.com", linkAgentEditorScopes); !errors.As(err, &refused) || len(refused.Details[fieldLinkAgentTokenSHA256]) == 0 {
		t.Fatalf("Replace() of another agent to the digest Claims Desk still serves = %v, want a tokenSha256 refusal", describeValidationError(err))
	}
	back := UpdateLinkAgentRequest(claimsDesk())
	back.TokenSHA256 = served
	if _, err := f.svc.Replace(t.Context(), claims.ID, back, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() of Claims Desk back to the digest it still serves = %v, want it accepted", describeValidationError(err))
	}
	if _, err := f.svc.Replace(t.Context(), claims.ID, replaced, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}

	f.rollAgent(t, claims.ID, DeployActionUndeploy)
	if _, _, err := f.svc.Create(t.Context(), billing, "alice@example.com"); err != nil {
		t.Fatalf("Create() with the digest of an undeployed agent = %v, want it accepted", describeValidationError(err))
	}
}

func TestLinkAgentService_RefusesADigestADeployInFlightCarries(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	claims, _ := f.create(t, claimsDesk())
	f.putInFlightAgentDeployment(t, claims.ID, DeployActionDeploy)
	billing := CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID, TokenSHA256: digestOf("the-old-token")}

	_, _, err := f.svc.Create(t.Context(), billing, "alice@example.com")

	var refused *ValidationError
	if !errors.As(err, &refused) || len(refused.Details[fieldLinkAgentTokenSHA256]) == 0 {
		t.Fatalf("Create() with the digest a deploy of Claims Desk in flight carries = %v, want a tokenSha256 refusal", describeValidationError(err))
	}
}

func TestLinkAgentService_AnUndeployInFlightFreesTheDigestItsRecordedDeployServed(t *testing.T) {
	const replacedDigest = "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	claims, token := f.create(t, claimsDesk())
	f.rollAgent(t, claims.ID, DeployActionDeploy)
	replaced := UpdateLinkAgentRequest(claimsDesk())
	replaced.TokenSHA256 = replacedDigest
	if _, err := f.svc.Replace(t.Context(), claims.ID, replaced, "bob@example.com", linkAgentEditorScopes); err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	f.putInFlightAgentDeployment(t, claims.ID, DeployActionUndeploy)
	billing := CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID, TokenSHA256: digestOf(token)}

	_, _, err := f.svc.Create(t.Context(), billing, "alice@example.com")

	if err != nil {
		t.Fatalf("Create() with the digest of an agent whose undeploy is in flight = %v, want it accepted: the undeploy supersedes the recorded deploy", describeValidationError(err))
	}
}
