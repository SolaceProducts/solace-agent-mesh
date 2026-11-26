// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	googleChatTestSubscription  = "projects/test-project/subscriptions/test-sub"
	googleChatTestSender        = "tester@example.com"
	googleChatDMSpace           = "spaces/TESTDM"
	googleChatSharedSpace       = "spaces/TESTSPACE"
	googleChatSpaceTypeDM       = "dm"
	googleChatSpaceTypeSpace    = "space"
	googleChatTaskDiscovery     = 3 * time.Second
	googleChatResolutionUnknown = "unknown"
)

type GoogleChatTestConfig struct {
	DefaultAgent            string                   `yaml:"default_agent"`
	SenderEmail             string                   `yaml:"sender_email"`
	SpaceType               string                   `yaml:"space_type"`
	NoMention               bool                     `yaml:"no_mention"`
	Identities              []GoogleChatTestIdentity `yaml:"identities"`
	NoAuthorizer            bool                     `yaml:"no_authorizer"`
	AuthorizerFailures      int                      `yaml:"authorizer_failures"`
	RunAs                   string                   `yaml:"run_as"`
	AllowedEmailDomains     []string                 `yaml:"allowed_email_domains"`
	ScriptFailures          []string                 `yaml:"script_failures"`
	FeedbackEnabled         bool                     `yaml:"feedback_enabled"`
	ThreadHistoryLimit      int                      `yaml:"thread_history_limit"`
	ArtifactDrive           bool                     `yaml:"artifact_drive"`
	ArtifactDriveAccessDays int                      `yaml:"artifact_drive_access_days"`
	DriveFailures           []string                 `yaml:"drive_failures"`
	DriveMaxArtifactBytes   int                      `yaml:"drive_max_artifact_bytes"`
	Delivery                string                   `yaml:"delivery"`
	WritePacing             bool                     `yaml:"write_pacing"`
	EventWorkers            int                      `yaml:"event_workers"`
}

type GoogleChatTestIdentity struct {
	Email      string   `yaml:"email"`
	Resolution string   `yaml:"resolution"`
	Scopes     []string `yaml:"scopes"`
}

type googleChatTestAuthorizer struct {
	identities   map[string]GoogleChatTestIdentity
	calls        atomic.Int64
	failuresLeft atomic.Int64
}

var errGoogleChatTestIdentityUnavailable = errors.New("test identity service unavailable")

func (a *googleChatTestAuthorizer) AuthorizeSender(_ context.Context, req auth.SenderAuthorizationRequest) (auth.SenderAuthorization, error) {
	a.calls.Add(1)
	if a.failuresLeft.Add(-1) >= 0 {
		return auth.SenderAuthorization{}, errGoogleChatTestIdentityUnavailable
	}
	entry, ok := a.identities[strings.ToLower(req.ClaimValue)]
	if !ok {
		return auth.SenderAuthorization{Resolution: identity.LookupNoMatch}, nil
	}
	resolution := identity.LookupResolution(entry.Resolution)
	switch entry.Resolution {
	case "":
		resolution = identity.LookupMatched
	case googleChatResolutionUnknown:
		resolution = identity.LookupUnknown
	}
	scopes := entry.Scopes
	if len(scopes) == 0 {
		scopes = []string{"*:*:*"}
	}
	return auth.SenderAuthorization{Resolution: resolution, ResolvedUserID: "platform-" + strutil.PIIFingerprint(req.ClaimValue), Scopes: scopes}, nil
}

type GoogleChatTestGateway struct {
	brk       broker.Broker
	namespace string
	gatewayID string
	logger    *slog.Logger
	svc       runtime.Services
	cfg       *GoogleChatTestConfig

	core       *entrypoint.GatewayCore
	mockServer *MockGoogleChatServer
	authorizer *googleChatTestAuthorizer

	dbURL  string
	dbDrop func()
	db     *store.DB

	signer      *googleChatTestSigner
	proxy       *entrypoint.GatewayProxy
	proxyServer *httptest.Server

	responseQueue broker.Queue
	statusQueue   broker.Queue
	requestQueue  broker.Queue
	stopRequests  context.CancelFunc
	requestsWG    sync.WaitGroup
	submittedMu   sync.Mutex
	submitted     []GoogleChatSubmittedTask
	answers       []GoogleChatSubmittedTask
	mu            sync.Mutex
	events        []CollectedEvent
	done          chan struct{}
	deliveryErr   error
	seq           atomic.Int64
}

func NewGoogleChatTestGateway(brk broker.Broker, cfg *GoogleChatTestConfig, namespace, gatewayID string, logger *slog.Logger) *GoogleChatTestGateway {
	if cfg == nil {
		cfg = &GoogleChatTestConfig{}
	}
	return &GoogleChatTestGateway{brk: brk, cfg: cfg, namespace: namespace, gatewayID: gatewayID, logger: logger, done: make(chan struct{})}
}

func (g *GoogleChatTestGateway) SetServices(svc runtime.Services) { g.svc = svc }

func (g *GoogleChatTestGateway) MockServer() *MockGoogleChatServer { return g.mockServer }

func (g *GoogleChatTestGateway) AuthorizerCalls() int64 {
	if g.authorizer == nil {
		return 0
	}
	return g.authorizer.calls.Load()
}

func (g *GoogleChatTestGateway) DBURL() string { return g.dbURL }

func (g *GoogleChatTestGateway) Setup(ctx context.Context) error {
	if err := g.setup(ctx); err != nil {
		g.Cleanup()
		return err
	}
	return nil
}

func (g *GoogleChatTestGateway) setup(ctx context.Context) error {
	if err := g.cfg.validate(); err != nil {
		return err
	}
	g.mockServer = NewMockGoogleChatServer()
	g.mockServer.ScriptFailures(g.cfg.ScriptFailures...)
	if g.cfg.WritePacing {
		g.mockServer.EnforceSpaceWriteRate(GoogleChatSpaceWriteInterval)
	}
	g.mockServer.ScriptDriveFailures(g.cfg.DriveFailures...)

	tmpl, err := getGatewayDBTemplate()
	if err != nil {
		return fmt.Errorf("init gateway db template: %w", err)
	}
	g.dbURL, g.dbDrop, err = tmpl.CloneDB()
	if err != nil {
		return fmt.Errorf("clone gateway db: %w", err)
	}
	if g.db, err = store.Open(g.dbURL, g.logger); err != nil {
		return fmt.Errorf("open gateway db: %w", err)
	}

	identities := g.cfg.Identities
	if len(identities) == 0 {
		identities = []GoogleChatTestIdentity{{Email: g.senderEmail("")}}
	}
	g.authorizer = &googleChatTestAuthorizer{identities: map[string]GoogleChatTestIdentity{}}
	g.authorizer.failuresLeft.Store(int64(g.cfg.AuthorizerFailures))
	for _, id := range identities {
		g.authorizer.identities[strings.ToLower(id.Email)] = id
	}

	if g.responseQueue, err = g.brk.CreateDirectSubscription(); err != nil {
		return fmt.Errorf("create response queue: %w", err)
	}
	if subErr := g.brk.Subscribe(g.responseQueue, a2a.GatewayResponseSubscription(g.namespace, g.gatewayID)); subErr != nil {
		return fmt.Errorf("subscribe response: %w", subErr)
	}
	if g.statusQueue, err = g.brk.CreateDirectSubscription(); err != nil {
		return fmt.Errorf("create status queue: %w", err)
	}
	if subErr := g.brk.Subscribe(g.statusQueue, a2a.GatewayStatusSubscription(g.namespace, g.gatewayID)); subErr != nil {
		return fmt.Errorf("subscribe status: %w", subErr)
	}
	if err := g.watchSubmittedTasks(ctx); err != nil {
		return err
	}
	if g.overHTTPS() {
		if err := g.startHTTPSIngress(); err != nil {
			return err
		}
	}
	return g.startAdapter(ctx)
}

type GoogleChatSubmittedTask struct {
	TaskID    string
	AgentName string
	UserID    string
	Files     []a2a.FileContent
}

func (g *GoogleChatTestGateway) watchSubmittedTasks(ctx context.Context) error {
	q, err := g.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create request watch queue: %w", err)
	}
	g.requestQueue = q
	if err := g.brk.Subscribe(q, a2a.AllAgentRequestSubscription(g.namespace)); err != nil {
		return fmt.Errorf("subscribe agent requests: %w", err)
	}
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	g.stopRequests = cancel
	requestTopicPrefix := a2a.AgentRequestTopic(g.namespace, "")
	g.requestsWG.Go(func() {
		for watchCtx.Err() == nil {
			msg, err := g.brk.Receive(watchCtx, q)
			if err != nil || msg == nil {
				continue
			}
			msg.Ack()
			props := a2a.DecodeUserProperties(msg.UserProperties)
			if props.ClientID != g.gatewayID {
				continue
			}
			var req googleChatAgentRequest
			if json.Unmarshal(msg.Payload, &req) != nil {
				continue
			}
			published := GoogleChatSubmittedTask{
				TaskID:    req.ID,
				AgentName: strings.TrimPrefix(msg.Topic, requestTopicPrefix),
				UserID:    props.UserID,
			}
			for _, p := range req.Params.Message.Parts {
				if p.File != nil {
					published.Files = append(published.Files, *p.File)
				}
			}
			g.submittedMu.Lock()
			switch {
			case req.Method == "message/stream":
				g.submitted = append(g.submitted, published)
			case req.answersAQuestion():
				g.answers = append(g.answers, published)
			}
			g.submittedMu.Unlock()
		}
	})
	return nil
}

type googleChatAgentRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params struct {
		Message struct {
			Parts []googleChatRequestPart `json:"parts"`
		} `json:"message"`
	} `json:"params"`
}

type googleChatRequestPart struct {
	Data struct {
		Type a2a.SignalType `json:"type"`
	} `json:"data"`
	File *a2a.FileContent `json:"file"`
}

func (r googleChatAgentRequest) answersAQuestion() bool {
	return r.Method == "message/send" && slices.ContainsFunc(r.Params.Message.Parts, func(p googleChatRequestPart) bool {
		return p.Data.Type == a2a.SignalUserInputResponse
	})
}

func (g *GoogleChatTestGateway) SubmittedTasks() []GoogleChatSubmittedTask {
	g.submittedMu.Lock()
	defer g.submittedMu.Unlock()
	return append([]GoogleChatSubmittedTask(nil), g.submitted...)
}

func (g *GoogleChatTestGateway) SubmittedAnswers() []GoogleChatSubmittedTask {
	g.submittedMu.Lock()
	defer g.submittedMu.Unlock()
	return append([]GoogleChatSubmittedTask(nil), g.answers...)
}

func (g *GoogleChatTestGateway) AdapterConfig() map[string]any {
	cfg := map[string]any{
		"default_agent_name":    g.cfg.DefaultAgent,
		"api_base_url":          g.mockServer.URL(),
		"pubsub_base_url":       g.mockServer.URL(),
		"test_skip_google_auth": true,
	}
	if !g.cfg.WritePacing {
		cfg["min_edit_interval_ms"] = 0
	}
	switch g.cfg.Delivery {
	case GoogleChatDeliveryHTTPS, GoogleChatDeliveryUnset:
		if g.cfg.Delivery == GoogleChatDeliveryHTTPS {
			cfg["delivery"] = googlechat.DeliveryHTTPS
		}
		cfg["auth_audience_type"] = googlechat.AudienceProjectNumber
		cfg["project_number"] = GoogleChatTestProjectNumber
		cfg["endpoint_url"] = GoogleChatTestEndpointURL
		cfg["signer_certs_url"] = g.mockServer.SignerCertsURL()
	default:
		cfg["delivery"] = googlechat.DeliveryPubSub
		cfg["pubsub_subscription"] = googleChatTestSubscription
	}
	if g.cfg.RunAs != "" {
		cfg["run_as"] = g.cfg.RunAs
	}
	if g.cfg.FeedbackEnabled {
		cfg["feedback_enabled"] = true
	}
	if g.cfg.ThreadHistoryLimit > 0 {
		cfg["thread_history_limit"] = g.cfg.ThreadHistoryLimit
	}
	if g.cfg.ArtifactDrive {
		cfg["artifact_drive_folder_id"] = MockDriveFolderID
		cfg["drive_base_url"] = g.mockServer.DriveURL()
		if g.cfg.ArtifactDriveAccessDays > 0 {
			cfg["artifact_drive_access_days"] = g.cfg.ArtifactDriveAccessDays
		}
		if g.cfg.DriveMaxArtifactBytes > 0 {
			cfg["test_drive_max_artifact_bytes"] = g.cfg.DriveMaxArtifactBytes
		}
		cfg["test_drive_remove_backoff_ms"] = driveRemoveBackoffMillis
	}
	if len(g.cfg.AllowedEmailDomains) > 0 {
		cfg["allowed_email_domains"] = strings.Join(g.cfg.AllowedEmailDomains, ",")
	}
	return cfg
}

func (g *GoogleChatTestGateway) appConfig(adapterConfig map[string]any) map[string]any {
	cfg := map[string]any{
		"namespace":  g.namespace,
		"gateway_id": g.gatewayID,
		"gateway_adapter": map[string]any{
			"type":           googlechat.EntrypointType,
			"adapter_config": adapterConfig,
		},
		"session_service": map[string]any{
			"type":             "sql",
			"database_url":     g.dbURL,
			"default_behavior": "PERSISTENT",
		},
		"task_logging": map[string]any{"enabled": true},
	}
	if g.cfg.EventWorkers > 0 {
		cfg["event_workers"] = g.cfg.EventWorkers
	}
	return cfg
}

func (g *GoogleChatTestGateway) startAdapter(ctx context.Context) error {
	core, err := g.NewInstance(ctx)
	if err != nil {
		return err
	}
	g.core = core
	return nil
}

func (g *GoogleChatTestGateway) NewInstance(ctx context.Context) (*entrypoint.GatewayCore, error) {
	adapterConfig := g.AdapterConfig()
	adapter := googlechat.NewAdapter(adapterConfig)
	appConfig := g.appConfig(adapterConfig)
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(g.svc)
	if !g.cfg.NoAuthorizer {
		core.SetIdentityAuthorizer(g.authorizer)
	}
	if err := g.mount(core, appConfig); err != nil {
		return nil, err
	}
	if err := core.Init(ctx, nil); err != nil {
		_ = core.Stop(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("google chat gateway init: %w", err)
	}
	if err := core.Start(ctx); err != nil {
		_ = core.Stop(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("google chat gateway start: %w", err)
	}
	return core, nil
}

func (g *GoogleChatTestGateway) RestartInstance(ctx context.Context) error {
	if g.core == nil {
		return errors.New("google chat gateway: no replica is running")
	}
	if err := g.core.Stop(ctx); err != nil {
		return fmt.Errorf("stop google chat gateway: %w", err)
	}
	g.core = nil
	core, err := g.NewInstance(ctx)
	if err != nil {
		return err
	}
	g.core = core
	return nil
}

func (g *GoogleChatTestGateway) DB() *store.DB { return g.db }

func (g *GoogleChatTestGateway) ClaimState(ctx context.Context, eventKey string) (string, error) {
	return store.NewGoogleChatProcessedRepo(g.db).State(ctx, g.gatewayID, eventKey)
}

func (g *GoogleChatTestGateway) senderEmail(override string) string {
	return cmp.Or(override, g.cfg.SenderEmail, googleChatTestSender)
}

type GoogleChatInbound struct {
	SenderEmail string
	Text        string
	Shared      bool
	NoMention   bool
	Thread      string
	MessageName string
	Attachments []GoogleChatAttachment
	AddOn       bool
	ThreadReply bool
}

type GoogleChatAttachment struct {
	Name     string
	MIMEType string
	Data     []byte
}

func (g *GoogleChatTestGateway) PublishMessage(in GoogleChatInbound) (messageName, pubsubID string) {
	messageName, body := g.MessageEvent(in)
	return messageName, g.deliver(body)
}

func (g *GoogleChatTestGateway) deliver(body []byte) string {
	if g.overHTTPS() {
		g.postSigned(body)
		return ""
	}
	return g.mockServer.Publish(body)
}

func (g *GoogleChatTestGateway) MessageEvent(in GoogleChatInbound) (messageName string, body []byte) {
	n := g.seq.Add(1)
	space, spaceType, legacyType := googleChatDMSpace, "DIRECT_MESSAGE", "DM"
	if in.Shared {
		space, spaceType, legacyType = googleChatSharedSpace, "SPACE", "ROOM"
	}
	messageName = cmp.Or(in.MessageName, fmt.Sprintf("%s/messages/in-%d-%d", space, time.Now().UnixNano(), n))
	thread := cmp.Or(in.Thread, space+"/threads/main")
	email := g.senderEmail(in.SenderEmail)
	user := map[string]any{"name": "users/" + strutil.PIIFingerprint(email), "displayName": "Test User", "email": email, "type": "HUMAN"}

	text := in.Text
	message := map[string]any{
		"name":   messageName,
		"sender": user,
		"thread": map[string]any{"name": thread},
	}
	if in.Shared && !in.NoMention {
		message["annotations"] = []map[string]any{{
			"type": "USER_MENTION", "startIndex": 0, "length": 4,
			"userMention": map[string]any{"user": map[string]any{"name": "users/app", "type": "BOT"}, "type": "MENTION"},
		}}
		message["argumentText"] = " " + text
		text = "@SAM " + text
	}
	message["text"] = text
	var attachments []map[string]any
	for i, att := range in.Attachments {
		resource := fmt.Sprintf("media-%d-%d", n, i)
		g.mockServer.RegisterMedia(resource, att.Data)
		attachments = append(attachments, map[string]any{
			"name": fmt.Sprintf("%s/attachments/%d", messageName, i), "contentName": att.Name, "contentType": att.MIMEType,
			"source": "UPLOADED_CONTENT", "attachmentDataRef": map[string]any{"resourceName": resource},
		})
	}
	if len(attachments) > 0 {
		message["attachment"] = attachments
	}
	if in.ThreadReply {
		message["threadReply"] = true
	}
	g.mockServer.recordThreadPost(messageName, thread, user["displayName"].(string), text)

	eventTime := time.Now().UTC().Format(time.RFC3339Nano)
	spaceObj := map[string]any{"name": space, "type": legacyType, "spaceType": spaceType, "singleUserBotDm": !in.Shared}
	event := map[string]any{"type": "MESSAGE", "eventTime": eventTime, "space": spaceObj, "user": user, "message": message}
	if in.AddOn {
		event = map[string]any{
			"commonEventObject": map[string]any{"hostApp": "CHAT"},
			"chat": map[string]any{
				"user": user, "eventTime": eventTime,
				"messagePayload": map[string]any{"space": spaceObj, "message": message},
			},
		}
	}
	body, _ = json.Marshal(event)
	return messageName, body
}

type GoogleChatClick struct {
	Message     string
	Function    string
	Params      map[string]string
	FormInputs  map[string][]string
	SenderEmail string
	Shared      bool
	AddOn       bool
}

func (g *GoogleChatTestGateway) PublishCardClick(click GoogleChatClick) string {
	email := g.senderEmail(click.SenderEmail)
	user := map[string]any{"name": "users/" + strutil.PIIFingerprint(email), "displayName": "Test User", "email": email, "type": "HUMAN"}
	inputs := map[string]any{}
	for name, values := range click.FormInputs {
		inputs[name] = map[string]any{"stringInputs": map[string]any{"value": values}}
	}
	space, spaceObj := googleChatDMSpace, map[string]any{"name": googleChatDMSpace, "type": "DM", "spaceType": "DIRECT_MESSAGE", "singleUserBotDm": true}
	if click.Shared {
		space, spaceObj = googleChatSharedSpace, map[string]any{"name": googleChatSharedSpace, "type": "ROOM", "spaceType": "SPACE", "singleUserBotDm": false}
	}
	message := map[string]any{"name": click.Message, "thread": map[string]any{"name": space + "/threads/main"}}
	common := map[string]any{"invokedFunction": click.Function, "parameters": click.Params, "formInputs": inputs}
	event := map[string]any{"type": "CARD_CLICKED", "space": spaceObj, "user": user, "message": message, "common": common}
	if click.AddOn {
		common["hostApp"] = "CHAT"
		event = map[string]any{
			"commonEventObject": common,
			"chat": map[string]any{
				"user": user, "eventTime": time.Now().UTC().Format(time.RFC3339Nano),
				"buttonClickedPayload": map[string]any{"space": spaceObj, "message": message},
			},
		}
	}
	body, _ := json.Marshal(event)
	return g.deliver(body)
}

func (g *GoogleChatTestGateway) SendRequest(_ context.Context, _ string, input *GatewayInput) (string, error) {
	var text string
	var attachments []GoogleChatAttachment
	for _, p := range input.GetParts() {
		if p.Type == "file" {
			if name, b64 := resolveFilePartFields(p); b64 != "" {
				data, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					return "", fmt.Errorf("decode file part: %w", err)
				}
				attachments = append(attachments, GoogleChatAttachment{Name: name, MIMEType: p.MimeType, Data: data})
			}
			continue
		}
		if t := p.GetText(); t != "" && text == "" {
			text = t
		}
	}
	g.PublishMessage(GoogleChatInbound{
		SenderEmail: input.UserIdentity,
		Text:        text,
		Attachments: attachments,
		Shared:      g.cfg.SpaceType == googleChatSpaceTypeSpace,
		NoMention:   g.cfg.NoMention,
	})
	if err := g.DeliveryErr(); err != nil {
		return "", err
	}
	return g.discoverTaskID(googleChatTaskDiscovery), nil
}

func (g *GoogleChatTestGateway) discoverTaskID(timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, q := range []struct {
		queue broker.Queue
		kind  string
	}{{g.statusQueue, "status"}, {g.responseQueue, "response"}} {
		msg, err := g.brk.Receive(ctx, q.queue)
		if err != nil || msg == nil {
			continue
		}
		taskID := extractTaskIDFromTopic(msg.Topic)
		g.record(CollectedEvent{Type: q.kind, Topic: msg.Topic, TaskID: taskID, Payload: json.RawMessage(msg.Payload), Timestamp: time.Now()})
		msg.Ack()
		if q.kind == "response" {
			g.signalDone()
		}
		return taskID
	}
	return ""
}

func (g *GoogleChatTestGateway) record(evt CollectedEvent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, evt)
}

func (g *GoogleChatTestGateway) ResetForNextRequest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = nil
	g.done = make(chan struct{})
}

func (g *GoogleChatTestGateway) doneCh() chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.done
}

func (g *GoogleChatTestGateway) signalDone() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.done:
	default:
		close(g.done)
	}
}

func (g *GoogleChatTestGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { g.receiveLoop(collectCtx, g.responseQueue, "response", true) })
	wg.Go(func() { g.receiveLoop(collectCtx, g.statusQueue, "status", false) })

	gotResponse := false
	select {
	case <-g.doneCh():
		gotResponse = true
		cancel()
	case <-collectCtx.Done():
	}
	wg.Wait()

	g.mu.Lock()
	events := append([]CollectedEvent(nil), g.events...)
	g.mu.Unlock()
	if gotResponse {
		return events, nil
	}
	return events, collectCtx.Err()
}

func (g *GoogleChatTestGateway) receiveLoop(ctx context.Context, queue broker.Queue, kind string, isFinal bool) {
	for ctx.Err() == nil {
		msg, err := g.brk.Receive(ctx, queue)
		if err != nil || msg == nil {
			continue
		}
		g.record(CollectedEvent{Type: kind, Topic: msg.Topic, TaskID: extractTaskIDFromTopic(msg.Topic), Payload: json.RawMessage(msg.Payload), Timestamp: time.Now()})
		msg.Ack()
		if isFinal {
			g.signalDone()
		}
	}
}

func (g *GoogleChatTestGateway) AwaitFinalResponses(n int, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for seen := 0; seen < n; {
		msg, err := g.brk.Receive(ctx, g.responseQueue)
		if err != nil {
			return false
		}
		if msg == nil {
			continue
		}
		g.record(CollectedEvent{Type: "response", Topic: msg.Topic, TaskID: extractTaskIDFromTopic(msg.Topic), Payload: json.RawMessage(msg.Payload), Timestamp: time.Now()})
		msg.Ack()
		seen++
	}
	return true
}

func (g *GoogleChatTestGateway) SendCancel(ctx context.Context, agentName, taskID string) error {
	if g.core == nil {
		return errors.New("google chat gateway: no replica is running")
	}
	return g.core.CancelTask(ctx, taskID, agentName)
}

func (g *GoogleChatTestGateway) Cleanup() {
	ctx := context.Background()
	if g.core != nil {
		_ = g.core.Stop(ctx)
	}
	if g.stopRequests != nil {
		g.stopRequests()
		g.requestsWG.Wait()
	}
	for _, q := range []broker.Queue{g.responseQueue, g.statusQueue, g.requestQueue} {
		if q != nil {
			_ = g.brk.DeleteQueue(q.Name())
		}
	}
	g.stopHTTPSIngress()
	if g.mockServer != nil {
		g.mockServer.Close()
	}
	if g.db != nil {
		if err := g.db.Close(); err != nil {
			g.logger.Warn("google chat test gateway database close failed", "error", err)
		}
	}
	if g.dbDrop != nil {
		g.dbDrop()
	}
}

var _ Gateway = (*GoogleChatTestGateway)(nil)

const driveRemoveBackoffMillis = 50
