// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
)

const (
	redacted       = "<REDACTED>"
	missingID      = "0190a5f0-0000-7000-8000-000000000000"
	slackBotToken  = "xoxb-bot-token-the-test-supplies"
	isoMillisUTC   = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`
	deployFailText = "instance failed to start"
)

type fakeGateway struct {
	failAdds    atomic.Bool
	failRemoves atomic.Bool
	removes     atomic.Int32
}

func (g *fakeGateway) handle(method string, _ []string, _ json.RawMessage) (any, error) {
	if method == "delete" {
		g.removes.Add(1)
	}
	switch {
	case method == "get":
		return []any{}, nil
	case method == "post" && g.failAdds.Load():
		return nil, errors.New(deployFailText)
	case method == "delete" && g.failRemoves.Load():
		return nil, errors.New("instance failed to stop")
	default:
		return map[string]string{"status": "ok"}, nil
	}
}

type entrypointAPI struct {
	*apiClient
	gateway *fakeGateway
}

func newEntrypointAPI(t *testing.T, extraConfig map[string]any) *entrypointAPI {
	t.Helper()
	core, _ := newTestCoreWithConfig(t, extraConfig)
	gw := &fakeGateway{}
	startControlApp(t, core, "gateway", gw.handle)
	gw.runAsGWE(t, core)
	return &entrypointAPI{apiClient: newAPIClient(t, core), gateway: gw}
}

func (g *fakeGateway) runAsGWE(t *testing.T, core *platpkg.PlatformCore) {
	t.Helper()
	brk := core.Services().Broker()
	if limited, ok := brk.(*publishLimitBroker); ok {
		brk = limited.Broker
	}
	namespace := core.Config().Namespace
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(queue, a2a.EntrypointApplyTopic(namespace)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	applied := map[string]control.EntrypointStatus{}
	report := func() {
		mu.Lock()
		status := control.GatewayStatus{ProcessID: "fake-gwe"}
		for _, e := range applied {
			status.Entrypoints = append(status.Entrypoints, e)
		}
		mu.Unlock()
		body, _ := json.Marshal(status)
		_ = brk.Publish(ctx, a2a.GWEStatusTopic(namespace), &broker.Message{Payload: body})
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			msg, err := brk.Receive(ctx, queue)
			if ctx.Err() != nil {
				return
			}
			if err != nil || msg == nil {
				continue
			}
			var d control.EntrypointApply
			if json.Unmarshal(msg.Payload, &d) != nil {
				continue
			}
			status := control.EntrypointStatus{Name: d.Name, DeploymentID: d.DeploymentID, Revision: d.Revision, Attempt: d.Attempt}
			switch {
			case d.Undeploy:
				g.removes.Add(1)
				if g.failRemoves.Load() {
					status.Error = "instance failed to stop"
				}
			case g.failAdds.Load():
				status.Error = deployFailText
			}
			mu.Lock()
			applied[d.Name] = status
			mu.Unlock()
			report()
		}
	})
	wg.Go(func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			report()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		_ = brk.DeleteQueue(queue.Name())
	})
}

func (a *entrypointAPI) createSlack(name string) Entrypoint {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           name,
		"description":    "Slack entrypoint the test creates.",
		"entrypointType": "slack",
		"values":         map[string]any{"bot_token": slackBotToken, "app_token": "xapp-app-token"},
	})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[Entrypoint](a.t, rec)
	expectLocation(a.t, rec, "/entrypoints/"+created.ID)
	return created
}

func (a *entrypointAPI) deploy(entrypointID string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.call(http.MethodPost, "/entrypointDeployments", map[string]any{"entrypointId": entrypointID, "action": "deploy"})
}

func expectLocation(t *testing.T, rec *httptest.ResponseRecorder, path string) {
	t.Helper()
	if got := rec.Header().Get("Location"); got != platpkg.APIV2Prefix+path {
		t.Errorf("Location = %q, want %q", got, platpkg.APIV2Prefix+path)
	}
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var body entplatform.DataResponse[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode {data} from %s: %v", rec.Body.String(), err)
	}
	return body.Data
}

func decodeList[T any](t *testing.T, rec *httptest.ResponseRecorder) entplatform.PaginatedResponse[T] {
	t.Helper()
	var body entplatform.PaginatedResponse[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list from %s: %v", rec.Body.String(), err)
	}
	if body.Data == nil {
		t.Fatalf("list data is null in %s", rec.Body.String())
	}
	return body
}

func expectDetail(t *testing.T, rec *httptest.ResponseRecorder, status int, field string) {
	t.Helper()
	expectStatus(t, rec, status)
	if body := decodeError(t, rec); len(body.ValidationDetails[field]) == 0 {
		t.Errorf("validationDetails has nothing for %q: %s", field, rec.Body.String())
	}
}

func TestEntrypoints_Lifecycle(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("lifecycle-slack")

	if created.Type != "entrypoint" || created.EntrypointType != "slack" {
		t.Errorf("type %q entrypointType %q, want entrypoint and slack", created.Type, created.EntrypointType)
	}
	if created.Values["bot_token"] != redacted {
		t.Errorf("values.bot_token = %v, want it redacted", created.Values["bot_token"])
	}
	if created.RuntimeStatus.Problems == nil {
		t.Error("runtimeStatus.problems is null")
	}

	got := api.call(http.MethodGet, "/entrypoints/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"createdTime", "updatedTime"} {
		if s, _ := raw.Data[key].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", key, raw.Data[key])
		}
	}
	for _, dropped := range []string{"gatewayId", "connectionStatus", "yamlTemplatePath", "backendPort", "dnsId", "deploy"} {
		if _, ok := raw.Data[dropped]; ok {
			t.Errorf("GET returned the dropped field %q", dropped)
		}
	}
	if raw.Data["values"].(map[string]any)["bot_token"] != redacted {
		t.Errorf("GET values.bot_token = %v, want it redacted", raw.Data["values"])
	}

	list := decodeList[Entrypoint](t, api.call(http.MethodGet, "/entrypoints", nil))
	if len(list.Data) != 1 || list.Data[0].ID != created.ID {
		t.Errorf("list = %+v, want the one entrypoint", list.Data)
	}

	patched := api.call(http.MethodPatch, "/entrypoints/"+created.ID, map[string]any{"name": "renamed-slack"})
	expectStatus(t, patched, http.StatusOK)
	if name := decodeData[Entrypoint](t, patched).Name; name != "renamed-slack" {
		t.Errorf("patched name = %q", name)
	}

	expectStatus(t, api.call(http.MethodDelete, "/entrypoints/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil), http.StatusNotFound)
}

func TestEntrypoints_Paging(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	for _, name := range []string{"page-one", "page-two", "page-three"} {
		api.createSlack(name)
	}

	defaults := decodeList[Entrypoint](t, api.call(http.MethodGet, "/entrypoints", nil))
	if p := defaults.Meta.Pagination; p.PageNumber != 1 || p.PageSize != defaultPageSize || p.Count != 3 {
		t.Errorf("default pagination = %+v, want page 1 of size %d with count 3", p, defaultPageSize)
	}

	second := decodeList[Entrypoint](t, api.call(http.MethodGet, "/entrypoints?pageNumber=2&pageSize=2", nil))
	if len(second.Data) != 1 || second.Meta.Pagination.Count != 3 || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 of size 2 = %d items, pagination %+v", len(second.Data), second.Meta.Pagination)
	}

	expectStatus(t, api.call(http.MethodGet, "/entrypoints?pageSize=100", nil), http.StatusOK)
	expectDetail(t, api.call(http.MethodGet, "/entrypoints?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/entrypoints?pageNumber=0", nil), http.StatusUnprocessableEntity, "pageNumber")
	expectDetail(t, api.call(http.MethodGet, "/entrypoints?pageSize=ten", nil), http.StatusBadRequest, "pageSize")

	filtered := decodeList[Entrypoint](t, api.call(http.MethodGet, "/entrypoints?entrypointType=teams", nil))
	if len(filtered.Data) != 0 {
		t.Errorf("entrypointType=teams returned %d slack entrypoints", len(filtered.Data))
	}
}

func TestEntrypoints_Validation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("validation-slack")
	path := "/entrypoints/" + created.ID

	expectDetail(t, api.call(http.MethodPost, "/entrypoints", `{"name":"x","entrypointType":"slack"}`), http.StatusUnprocessableEntity, "description")
	expectDetail(t, api.call(http.MethodPost, "/entrypoints", `{"name":"extra-field","gatewayType":"slack"}`), http.StatusBadRequest, "gatewayType")
	expectDetail(t, api.call(http.MethodPost, "/entrypoints", `{"name":7}`), http.StatusBadRequest, "name")
	expectStatus(t, api.call(http.MethodPost, "/entrypoints", `{"name":`), http.StatusBadRequest)

	expectDetail(t, api.call(http.MethodPatch, path, `{"entrypointType":"teams"}`), http.StatusUnprocessableEntity, "entrypointType")
	expectDetail(t, api.call(http.MethodPatch, path, `{"slug":"new-slug"}`), http.StatusUnprocessableEntity, "slug")
	expectDetail(t, api.call(http.MethodPatch, path, `{"name":null}`), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, path, `{"values":null}`), http.StatusUnprocessableEntity, "values")
	expectDetail(t, api.call(http.MethodPatch, path, `{"deploy":true}`), http.StatusBadRequest, "deploy")

	both := api.call(http.MethodPatch, path, `{"slug":"new-slug","description":""}`)
	expectStatus(t, both, http.StatusUnprocessableEntity)
	if details := decodeError(t, both).ValidationDetails; len(details["slug"]) == 0 || len(details["description"]) == 0 {
		t.Errorf("want every field problem at once, got %v", details)
	}

	expectDetail(t, api.call(http.MethodGet, "/entrypoints/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+missingID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPatch, "/entrypoints/"+missingID, `{"name":"missing-one"}`), http.StatusNotFound)
}

func TestEntrypoints_DuplicateNameOnCreateIs409(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	body := map[string]any{"name": "dup-teams", "description": "Entrypoint created twice.", "entrypointType": "teams",
		"values": map[string]any{"microsoft_app_id": "12345678-1234-1234-1234-123456789abc", "microsoft_app_password": "app-password"}}

	expectCreated[Entrypoint](t, api.call(http.MethodPost, "/entrypoints", body), "entrypoints")
	expectMessage(t, api.call(http.MethodPost, "/entrypoints", body), http.StatusConflict, "Unable to create entrypoint")
}

func TestEntrypoints_PatchNullClearsPublicURL(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("public-url-slack")
	path := "/entrypoints/" + created.ID

	set := api.call(http.MethodPatch, path, `{"publicUrl":"https://hooks.example.com/slack"}`)
	expectStatus(t, set, http.StatusOK)
	if got := decodeData[Entrypoint](t, set).PublicURL; got == nil || *got != "https://hooks.example.com/slack" {
		t.Fatalf("publicUrl after set = %v", got)
	}

	cleared := api.call(http.MethodPatch, path, `{"publicUrl":null}`)
	expectStatus(t, cleared, http.StatusOK)
	if got := decodeData[Entrypoint](t, cleared); got.PublicURL != nil || got.Name != created.Name {
		t.Errorf("after publicUrl null: publicUrl %v name %q, want cleared and unchanged", got.PublicURL, got.Name)
	}
	if !strings.Contains(cleared.Body.String(), `"publicUrl":null`) {
		t.Errorf("a cleared publicUrl is sent as null, got %s", cleared.Body.String())
	}
}

func TestEntrypoints_Scopes(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("scoped-slack")

	for _, tc := range []struct {
		method, path string
		body         any
		scope        string
	}{
		{http.MethodPost, "/entrypoints", `{}`, rbac.ScopeEntrypointCreate},
		{http.MethodPatch, "/entrypoints/" + created.ID, `{}`, rbac.ScopeEntrypointUpdate},
		{http.MethodDelete, "/entrypoints/" + created.ID, nil, rbac.ScopeEntrypointDelete},
		{http.MethodPut, "/entrypoints/" + created.ID + "/secretsDiff", `{}`, rbac.ScopeEntrypointUpdate},
		{http.MethodPost, "/entrypointDeployments", `{}`, rbac.ScopeEntrypointDeploy},
		{http.MethodGet, "/entrypointDeployments", nil, rbac.ScopeDeploymentRead},
		{http.MethodGet, "/entrypointDeployments/" + missingID, nil, rbac.ScopeDeploymentRead},
	} {
		rec := api.call(tc.method, tc.path, tc.body, rbac.ScopeEntrypointRead)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with only %s = %d, want 403 (needs %s)", tc.method, tc.path, rbac.ScopeEntrypointRead, rec.Code, tc.scope)
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/entrypoints", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/entrypoints", nil, rbac.ScopeEntrypointRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/entrypointSchemas/slack", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/entrypointSchemas/slack", nil, rbac.ScopeEntrypointRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/entrypointDeployments", nil, rbac.ScopeDeploymentRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/entrypointDeployments/"+missingID, nil, rbac.ScopeDeploymentRead), http.StatusNotFound)
}

func TestEntrypointDeployments_Success(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("deploy-slack")

	rec := api.deploy(created.ID)
	expectStatus(t, rec, http.StatusCreated)
	deployment := decodeData[EntrypointDeployment](t, rec)
	if deployment.Type != "entrypointDeployment" || deployment.Status != entplatform.DeployStatusSuccess || deployment.EntrypointID != created.ID {
		t.Errorf("deployment = %+v", deployment)
	}
	expectLocation(t, rec, "/entrypointDeployments/"+deployment.ID)
	if !strings.Contains(rec.Body.String(), `"failure":null`) {
		t.Errorf("a successful deployment sends failure as null, got %s", rec.Body.String())
	}

	expectStatus(t, api.call(http.MethodGet, "/entrypointDeployments/"+deployment.ID, nil), http.StatusOK)
	listed := decodeList[EntrypointDeployment](t, api.call(http.MethodGet, "/entrypointDeployments?entrypointId="+created.ID, nil))
	if len(listed.Data) != 1 || listed.Data[0].ID != deployment.ID {
		t.Errorf("deployments for the entrypoint = %+v", listed.Data)
	}

	after := decodeData[Entrypoint](t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil))
	if after.Deployed == nil {
		t.Fatal("deployed is null after a successful deploy")
	}
	if after.Deployed.Values["bot_token"] != redacted {
		t.Errorf("deployed.values.bot_token = %v, want it redacted", after.Deployed.Values["bot_token"])
	}
}

func TestEntrypointDeployments_FailureIs502AndKeepsTheRecord(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("failing-slack")
	api.gateway.failAdds.Store(true)

	rec := api.deploy(created.ID)
	expectStatus(t, rec, http.StatusBadGateway)
	body := decodeError(t, rec)
	if body.Message != msgDeployFailed {
		t.Errorf("message = %q, want %q", body.Message, msgDeployFailed)
	}
	if strings.Contains(rec.Body.String(), deployFailText) {
		t.Errorf("the upstream error reached the response: %s", rec.Body.String())
	}
	kept := api.expectKeptFailedDeployment(rec, entplatform.DeployFailureRuntimeError, deployFailText)
	if id := kept.Failure.ErrorID; id == nil || body.ErrorID != *id {
		t.Errorf("response errorId = %q, record failure = %+v, want one id", body.ErrorID, *kept.Failure)
	}
}

func (a *entrypointAPI) expectKeptFailedDeployment(rec *httptest.ResponseRecorder, wantReason, rawText string) EntrypointDeployment {
	a.t.Helper()
	location := rec.Header().Get("Location")
	id, ok := strings.CutPrefix(location, platpkg.APIV2Prefix+"/entrypointDeployments/")
	if !ok {
		a.t.Fatalf("Location = %q, want the deployment's v2 path", location)
	}

	kept := a.call(http.MethodGet, "/entrypointDeployments/"+id, nil)
	expectStatus(a.t, kept, http.StatusOK)
	d := decodeData[EntrypointDeployment](a.t, kept)
	if d.Status != entplatform.DeployStatusFailed || d.Failure == nil {
		a.t.Fatalf("kept deployment = %+v, want failed with a failure", d)
	}
	want := DeploymentFailure{Reason: wantReason, Message: fmt.Sprintf(deployFailureMessages[wantReason], "entrypoint")}
	if d.Failure.Reason != want.Reason || d.Failure.Message != want.Message || d.Failure.ErrorID == nil {
		a.t.Errorf("failure = %+v, want %+v with its errorId", *d.Failure, want)
	}
	if strings.Contains(kept.Body.String(), rawText) {
		a.t.Errorf("the raw error reached the kept record: %s", kept.Body.String())
	}
	return d
}

func TestDeploymentFailure(t *testing.T) {
	if got := deploymentFailure("entrypoint", entplatform.DeployStatusSuccess, "", ""); got != nil {
		t.Errorf("a successful deployment's failure = %+v, want nil", got)
	}
	got := deploymentFailure("entrypoint", entplatform.DeployStatusFailed, "", "")
	if got == nil || got.Reason != "unknown" || got.Message != "The entrypoint deployment failed." || got.ErrorID != nil {
		t.Errorf("a failure with no stored reason = %+v, want unknown with no errorId", got)
	}
}

func TestEntrypoints_RejectionsAreFiledUnderTheirField(t *testing.T) {
	t.Setenv(features.EnvVarName("whatsapp_gateway"), "false")
	api := newEntrypointAPI(t, nil)
	slack := api.createSlack("filed-slack")
	either := []string{entplatform.MsgEntrypointOrValuesRequired}
	create := func(fields map[string]any) map[string]any {
		body := map[string]any{"name": "rejected-one", "description": "Entrypoint the service rejects.", "entrypointType": "slack",
			"values": map[string]any{"bot_token": slackBotToken, "app_token": "xapp-app-token"}}
		maps.Copy(body, fields)
		return body
	}

	for _, tc := range []struct {
		name, method, path string
		body               any
		wantMessage        string
		wantDetails        map[string][]string
	}{
		{"values fail the type's rules", http.MethodPost, "/entrypoints",
			create(map[string]any{"values": map[string]any{"bot_token": "xoxp-user-token", "app_token": "xapp-app-token"}}),
			"Unable to create entrypoint", map[string][]string{"values.bot_token": {"bot_token must start with 'xoxb-'"}}},
		{"entrypoint type is malformed", http.MethodPost, "/entrypoints", create(map[string]any{"entrypointType": "Slack"}),
			"Unable to create entrypoint", map[string][]string{"entrypointType": {"type must be lowercase alphanumeric with underscores"}}},
		{"entrypoint type is switched off", http.MethodPost, "/entrypoints", create(map[string]any{"entrypointType": "whatsapp"}),
			"Unable to create entrypoint", map[string][]string{"entrypointType": {"whatsapp entrypoint is not enabled on this deployment"}}},
		{"publicUrl is not https", http.MethodPost, "/entrypoints", create(map[string]any{"publicUrl": "http://hooks.example.com"}),
			"Unable to create entrypoint", map[string][]string{"publicUrl": {"publicUrl must use https protocol"}}},
		{"publicUrl does not parse", http.MethodPost, "/entrypoints", create(map[string]any{"publicUrl": "https://hooks.example.com/%zz"}),
			"Unable to create entrypoint", map[string][]string{"publicUrl": {"publicUrl is not a valid URL"}}},
		{"a secret update is not a string", http.MethodPatch, "/entrypoints/" + slack.ID, `{"values":{"bot_token":7}}`,
			"Unable to update entrypoint", map[string][]string{"values": {`secret field "bot_token" must be a string, got float64`}}},
		{"a probe names no entrypoint or values", http.MethodPut, "/entrypoints/testConnection", `{"entrypointType":"slack"}`,
			msgValidationFailed, map[string][]string{"entrypointId": either, "values": either}},
		{"an event mesh probe has no broker", http.MethodPut, "/entrypoints/testConnection", `{"entrypointType":"event_mesh","values":{}}`,
			"Unable to test the entrypoint connection", map[string][]string{"values": {"broker_url is required in values"}}},
		{"a probe of a switched-off type", http.MethodPut, "/entrypoints/testConnection", `{"entrypointType":"whatsapp","values":{}}`,
			"Unable to test the entrypoint connection", map[string][]string{"entrypointType": {"whatsapp entrypoint is not enabled on this deployment"}}},
		{"a lookup while WhatsApp is switched off", http.MethodPut, "/entrypoints/whatsapp/messageTemplates", `{"businessAccountId":"1234567890","values":{}}`,
			"Unable to list WhatsApp message templates", map[string][]string{"entrypointType": {"whatsapp entrypoint is not enabled on this deployment"}}},
		{"a lookup names no entrypoint or values", http.MethodPut, "/entrypoints/whatsapp/messageTemplates", `{"businessAccountId":"1234567890"}`,
			msgValidationFailed, map[string][]string{"entrypointId": either, "values": either}},
		{"a lookup names a slack entrypoint", http.MethodPut, "/entrypoints/whatsapp/messageTemplates",
			map[string]any{"businessAccountId": "1234567890", "entrypointId": slack.ID},
			"Unable to list WhatsApp message templates", map[string][]string{"entrypointId": {"message templates are a WhatsApp entrypoint feature"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := api.call(tc.method, tc.path, tc.body)

			expectStatus(t, rec, http.StatusUnprocessableEntity)
			body := decodeError(t, rec)
			if body.Message != tc.wantMessage || !reflect.DeepEqual(body.ValidationDetails, tc.wantDetails) {
				t.Errorf("got %q %v\nwant %q %v", body.Message, body.ValidationDetails, tc.wantMessage, tc.wantDetails)
			}
		})
	}
}

func TestEntrypoints_SettingProblemsAreFiledUnderValues(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	rec := api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name": "bad-teams", "description": "Teams entrypoint the service rejects.", "entrypointType": "teams",
		"values": map[string]any{"microsoft_app_id": "not-a-guid", "microsoft_app_password": "secret"},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	if _, ok := details["values.microsoft_app_id"]; !ok || len(details) != 1 {
		t.Errorf("validationDetails = %v, want only values.microsoft_app_id", details)
	}
}

func TestEntrypointDeployments_UnfilledFieldsAre422(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	rec := api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name": "unfilled-slack", "description": "Slack entrypoint with a token still to fill in.", "entrypointType": "slack",
		"values": map[string]any{"bot_token": "<<__SAM_REQUIRED__>>", "app_token": "xapp-app-token"},
	})
	expectStatus(t, rec, http.StatusCreated)

	rec = api.deploy(decodeData[Entrypoint](t, rec).ID)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	if want := map[string][]string{"values.bot_token": {"bot_token is required"}}; body.Message != msgUnfilledFields || !reflect.DeepEqual(body.ValidationDetails, want) {
		t.Errorf("got %q %v, want %q %v", body.Message, body.ValidationDetails, msgUnfilledFields, want)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none: no record is kept", loc)
	}
}

func TestEntrypointDeployments_NoBrokerIs503(t *testing.T) {
	h := &entrypointHandler{deploySvc: entplatform.NewGatewayDeploymentService(nil, nil, nil, nil, slog.Default())}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"entrypointId":"` + missingID + `","action":"deploy"}`)

	Handle(h.deploy, msgDeployFailed).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/entrypointDeployments", body))

	expectStatus(t, rec, http.StatusServiceUnavailable)
	if got := decodeError(t, rec); got.Message != msgDeployFailed || got.ValidationDetails != nil || rec.Header().Get("Location") != "" {
		t.Errorf("got %s with Location %q, want the handler's sentence alone", rec.Body.String(), rec.Header().Get("Location"))
	}
}

func TestWhatsAppMessageTemplate_StatusIsLowercase(t *testing.T) {
	for meta, want := range map[string]string{"APPROVED": "approved", "PENDING_DELETION": "pending_deletion", "IN_APPEAL": "in_appeal"} {
		if got := toWhatsAppMessageTemplate(entplatform.WhatsAppMessageTemplate{Status: meta}).Status; got != want {
			t.Errorf("status %s = %q, want %q", meta, got, want)
		}
	}
}

func TestEntrypointDeployments_Validation(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("validate-deploy")

	expectDetail(t, api.call(http.MethodPost, "/entrypointDeployments", map[string]any{"entrypointId": created.ID, "action": "rollback"}),
		http.StatusUnprocessableEntity, "action")
	expectDetail(t, api.call(http.MethodPost, "/entrypointDeployments", map[string]any{"action": "deploy"}),
		http.StatusUnprocessableEntity, "entrypointId")
	expectStatus(t, api.deploy(missingID), http.StatusNotFound)
	for _, action := range []string{entplatform.DeployActionDeploy, entplatform.DeployActionUpdate, entplatform.DeployActionUndeploy} {
		for stray, value := range map[string]any{"values": map[string]any{}, "force": true, "version": 2} {
			body := map[string]any{"entrypointId": created.ID, "action": action, stray: value}
			expectDetail(t, api.call(http.MethodPost, "/entrypointDeployments", body), http.StatusBadRequest, stray)
		}
	}
	expectDetail(t, api.call(http.MethodGet, "/entrypointDeployments?entrypointId=nope", nil), http.StatusBadRequest, "entrypointId")
	expectDetail(t, api.call(http.MethodGet, "/entrypointDeployments/nope", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/entrypointDeployments/"+missingID, nil), http.StatusNotFound)
}

func TestEntrypoints_DeleteUndeploysFirst(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("undeploy-then-delete")
	expectStatus(t, api.deploy(created.ID), http.StatusCreated)
	before := api.gateway.removes.Load()

	expectStatus(t, api.call(http.MethodDelete, "/entrypoints/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil), http.StatusNotFound)
	if got := api.gateway.removes.Load() - before; got != 1 {
		t.Errorf("removes = %d, want 1", got)
	}
}

func TestEntrypoints_DeleteKeepsTheEntrypointWhenUndeployFails(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("undeploy-fails")
	expectStatus(t, api.deploy(created.ID), http.StatusCreated)
	before := api.gateway.removes.Load()
	api.gateway.failRemoves.Store(true)

	expectStatus(t, api.call(http.MethodDelete, "/entrypoints/"+created.ID, nil), http.StatusBadGateway)
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil), http.StatusOK)

	api.gateway.failRemoves.Store(false)
	expectStatus(t, api.call(http.MethodDelete, "/entrypoints/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil), http.StatusNotFound)
	if got := api.gateway.removes.Load() - before; got != 2 {
		t.Errorf("removes = %d, want 2: the retry must undeploy again", got)
	}
}

func TestEntrypoints_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, entrypointsTag, entrypointDeploymentsTag)
	want := []string{
		"DELETE /entrypoints/{id}", "GET /entrypointDeployments", "GET /entrypointDeployments/{id}", "GET /entrypoints", "GET /entrypoints/{id}", "PATCH /entrypoints/{id}",
		"POST /entrypointDeployments", "POST /entrypoints", "PUT /entrypoints/{id}/secretsDiff",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

func apiKeyRoutesTagged(t *testing.T, tags ...Tag) []string {
	t.Helper()
	core, deps := newTestCore(t)
	var got []string
	for _, rt := range routeTable(deps, core, core.Logger()) {
		if rt.AllowAPIKey && slices.Contains(tags, rt.Doc.Tag) {
			got = append(got, rt.Method+" "+rt.Path)
		}
	}
	slices.Sort(got)
	return got
}

func TestEntrypoints_NetworkingDetails(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	slack := api.createSlack("no-ingress-slack")
	rec := api.call(http.MethodGet, "/entrypoints/"+slack.ID+"/networkingDetails", nil)
	expectStatus(t, rec, http.StatusOK)
	if want := `{"data":{"dnsName":null,"port":null,"url":null,"webhookUrl":null,"apiPaths":[],"publicUrl":null}}` + "\n"; rec.Body.String() != want {
		t.Errorf("networking for a type with no ingress = %s, want %s", rec.Body.String(), want)
	}

	teams := createTeams(t, api)
	rec = api.call(http.MethodGet, "/entrypoints/"+teams.ID+"/networkingDetails", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[NetworkingDetails](t, rec); got.URL == nil || *got.URL != "/gw/"+teams.ID || got.APIPaths == nil {
		t.Errorf("shared-ingress networking = %+v", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"dnsName":null`) || !strings.Contains(body, `"port":null`) {
		t.Errorf("shared-ingress networking has no DNS name or port of its own, got %s", body)
	}
	expectStatus(t, api.call(http.MethodGet, "/entrypoints/"+missingID+"/networkingDetails", nil), http.StatusNotFound)
}

func TestEntrypoints_NetworkingDetailsWithoutDNSIs409(t *testing.T) {
	api := newEntrypointAPI(t, map[string]any{"ingress_domain": "sam.example.com"})
	teams := createTeams(t, api)
	path := "/entrypoints/" + teams.ID + "/networkingDetails"

	rec := api.call(http.MethodGet, path, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[NetworkingDetails](t, rec); got.DNSName == nil || !strings.HasSuffix(*got.DNSName, ".sam.example.com") {
		t.Errorf("dnsName = %v", got.DNSName)
	}

	if _, err := api.core.DB().ExecContext(t.Context(), `UPDATE gateways SET dns_id = NULL WHERE id = $1`, teams.ID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, api.call(http.MethodGet, path, nil), http.StatusConflict)
}

func createTeams(t *testing.T, api *entrypointAPI) Entrypoint {
	t.Helper()
	rec := api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           "teams-entrypoint",
		"description":    "Teams entrypoint the test creates.",
		"entrypointType": "teams",
		"values":         map[string]any{"microsoft_app_id": "12345678-1234-1234-1234-123456789abc", "microsoft_app_password": "app-password"},
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Entrypoint](t, rec)
	expectLocation(t, rec, "/entrypoints/"+created.ID)
	return created
}

func TestEntrypoints_SecretsDiff(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := api.createSlack("secrets-slack")
	path := "/entrypoints/" + created.ID + "/secretsDiff"

	rec := api.call(http.MethodPut, path, map[string]any{"values": map[string]any{"bot_token": slackBotToken, "app_token": "xapp-other"}})
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[SecretsDiffResult](t, rec)
	if !got.Secrets["bot_token"].Matches || got.Secrets["app_token"].Matches {
		t.Errorf("secrets = %+v, want bot_token matching and app_token not", got.Secrets)
	}
	if strings.Contains(rec.Body.String(), slackBotToken) {
		t.Error("the secret value reached the response")
	}

	expectDetail(t, api.call(http.MethodPut, path, `{}`), http.StatusUnprocessableEntity, "values")
	expectStatus(t, api.call(http.MethodPut, "/entrypoints/"+missingID+"/secretsDiff", map[string]any{"values": map[string]any{}}), http.StatusNotFound)
}

func TestEntrypoints_SecretsDiffWritesTheAuditLine(t *testing.T) {
	_, deps := newTestCore(t)
	created, err := deps.GatewaySvc.Create(t.Context(), entplatform.CreateGatewayRequest{
		Name: "audited-slack", Description: "Slack entrypoint the test audits.", Type: "slack",
		Values: map[string]any{"bot_token": slackBotToken, "app_token": "xapp-app-token"},
	}, "user1")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := &entrypointHandler{svc: deps.GatewaySvc, logger: slog.New(slog.NewJSONHandler(&logs, nil))}

	if _, err := h.secretsDiff(t.Context(), SecretsDiffRequest{ID: created.ID, Values: map[string]any{"bot_token": slackBotToken}}); err != nil {
		t.Fatal(err)
	}
	line := logs.String()
	if !strings.Contains(line, `"msg":"secrets diff"`) || !strings.Contains(line, created.ID) {
		t.Errorf("audit line = %q, want the secrets diff line for %s", line, created.ID)
	}
	if strings.Contains(line, slackBotToken) {
		t.Error("the secret value reached the audit line")
	}
}

func TestEntrypoints_TestConnection(t *testing.T) {
	t.Setenv(features.EnvVarName("whatsapp_gateway"), "true")
	api := newEntrypointAPI(t, nil)

	rec := api.call(http.MethodPut, "/entrypoints/testConnection", map[string]any{"entrypointType": "slack", "values": map[string]any{}})
	expectStatus(t, rec, http.StatusOK)
	if want := `{"data":{"success":true,"message":null,"errorClass":null,"stage":null}}` + "\n"; rec.Body.String() != want {
		t.Errorf("slack connection test = %s, want %s", rec.Body.String(), want)
	}

	rec = api.call(http.MethodPut, "/entrypoints/testConnection", map[string]any{"entrypointType": "whatsapp", "values": map[string]any{
		"access_token": "<<__SAM_WHATSAPP_TOKEN__>>", "phone_number_id": "123",
	}})
	expectStatus(t, rec, http.StatusOK)
	failed := decodeData[TestConnectionResult](t, rec)
	if failed.Success || *failed.ErrorClass != "config_incomplete" || *failed.Stage != "outbound" || !strings.Contains(*failed.Message, "substituted values") {
		t.Errorf("whatsapp probe with a placeholder token = %s, want a failed result", rec.Body.String())
	}

	expectDetail(t, api.call(http.MethodPut, "/entrypoints/testConnection", `{"entrypointId":"nope"}`), http.StatusUnprocessableEntity, "entrypointId")
}

func TestEntrypoints_MessageTemplatesThatCannotBeListedAre422(t *testing.T) {
	t.Setenv(features.EnvVarName("whatsapp_gateway"), "true")
	api := newEntrypointAPI(t, nil)
	path := "/entrypoints/whatsapp/messageTemplates"

	rec := api.call(http.MethodPut, path, map[string]any{
		"businessAccountId": "1234567890",
		"values":            map[string]any{"access_token": "<<__SAM_WHATSAPP_TOKEN__>>"},
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := decodeError(t, rec); !strings.Contains(body.Message, "substituted values") {
		t.Errorf("message = %q, want the service's explanation", body.Message)
	}
	expectDetail(t, api.call(http.MethodPut, path, `{"values":{}}`), http.StatusUnprocessableEntity, "businessAccountId")
}

func TestEntrypointSchemas_Paging(t *testing.T) {
	api := newEntrypointAPI(t, nil)

	all := decodeList[EntrypointSchema](t, api.call(http.MethodGet, "/entrypointSchemas", nil))
	if len(all.Data) < 2 || all.Meta.Pagination.Count != len(all.Data) {
		t.Fatalf("schemas = %d items, pagination %+v", len(all.Data), all.Meta.Pagination)
	}
	for _, s := range all.Data {
		if s.ID == "" || s.ID != s.EntrypointType || s.Type != "entrypointSchema" || s.Schema == nil || s.RequiredEnvVars == nil {
			t.Errorf("schema %+v is missing its id, type, schema or requiredEnvVars", s)
		}
	}

	second := decodeList[EntrypointSchema](t, api.call(http.MethodGet, "/entrypointSchemas?pageNumber=2&pageSize=1", nil))
	if len(second.Data) != 1 || second.Data[0].EntrypointType != all.Data[1].EntrypointType || second.Meta.Pagination.Count != len(all.Data) {
		t.Errorf("page 2 of size 1 = %+v, pagination %+v", second.Data, second.Meta.Pagination)
	}
	beyond := decodeList[EntrypointSchema](t, api.call(http.MethodGet, "/entrypointSchemas?pageNumber=99", nil))
	if len(beyond.Data) != 0 {
		t.Errorf("page 99 = %d items, want none", len(beyond.Data))
	}
}

func TestEntrypointSchemas_EveryListedIDIsFetchable(t *testing.T) {
	api := newEntrypointAPI(t, nil)

	all := decodeList[EntrypointSchema](t, api.call(http.MethodGet, "/entrypointSchemas?pageSize=100", nil))
	for _, listed := range all.Data {
		rec := api.call(http.MethodGet, "/entrypointSchemas/"+listed.ID, nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeData[EntrypointSchema](t, rec); !reflect.DeepEqual(got, listed) {
			t.Errorf("GET %s = %+v, want the listed %+v", listed.ID, got, listed)
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/entrypointSchemas/no_such_type", nil), http.StatusNotFound)
	for _, badKey := range []string{"Event-Mesh", "event-mesh", "Slack"} {
		expectDetail(t, api.call(http.MethodGet, "/entrypointSchemas/"+badKey, nil), http.StatusBadRequest, "id")
	}
}

func TestDeployedEntrypoint_CoversTheConfig(t *testing.T) {
	shown := map[string]struct{ snapshot, summary string }{
		"name":        {"name", "name"},
		"description": {"description", "description"},
		"type":        {"type", "entrypointType"},
		"values":      {"values", "values"},
	}
	leftOut := map[string]string{
		"id":           "the entrypoint's own id",
		"slug":         "fixed at create, so it never differs from the deployed one",
		"dns_id":       "internal; never sent",
		"public_url":   "a recorded address that the deploy does not use",
		"created_by":   "the entrypoint's audit field; deployedBy records the deploy",
		"updated_by":   "the entrypoint's audit field; deployedBy records the deploy",
		"created_time": "the entrypoint's audit field; deployedTime records the deploy",
		"updated_time": "the entrypoint's audit field; deployedTime records the deploy",
	}
	snapshot := jsonNames(reflect.TypeFor[entplatform.DeployedGatewayInfo]())
	summary := jsonNames(reflect.TypeFor[DeployedEntrypoint]())
	for _, f := range reflect.VisibleFields(reflect.TypeFor[store.Gateway]()) {
		column := f.Tag.Get("db")
		if _, ok := leftOut[column]; ok {
			continue
		}
		if s, ok := shown[column]; !ok || !snapshot[s.snapshot] || !summary[s.summary] {
			t.Errorf("store.Gateway.%s (%s) is neither carried through DeployedGatewayInfo into DeployedEntrypoint nor left out with a reason", f.Name, column)
		}
	}
}
