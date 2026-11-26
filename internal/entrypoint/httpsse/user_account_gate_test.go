// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const scheduledTaskTestKey = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

func scheduledTaskRoutes(t *testing.T) *http.ServeMux {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(t.Context()) })

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "schedkey"),
		},
		"scheduler_service": map[string]any{"enabled": true},
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithDefaultLLM(&mockLLMClient{response: "unused"}),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})
	return adapter.Mux()
}

func keyCaller(t *testing.T, req *http.Request) *http.Request {
	t.Helper()
	identity, rejection := auth.ResolveAPIKey(t.Context(),
		gateKeyAuthority{scopes: []string{"agent:*:invoke"}}, scheduledTaskTestKey)
	if rejection != nil {
		t.Fatalf("resolve key: %+v", rejection)
	}
	return req.WithContext(auth.ContextWithSubject(req.Context(), identity))
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func newTaskBody() map[string]any {
	return map[string]any{
		"name":               "nightly",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        []map[string]string{{"type": "text", "text": "hello"}},
	}
}

func TestScheduledTaskRoutes_RefuseAnAPIKey(t *testing.T) {
	mux := scheduledTaskRoutes(t)
	someTask := idgen.New()

	for name, req := range map[string]*http.Request{
		"create":       jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks", newTaskBody()),
		"update":       jsonRequest(t, http.MethodPatch, "/api/v1/scheduledTasks/"+someTask, map[string]any{"name": "renamed"}),
		"enable":       jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks/"+someTask+"/enable", map[string]any{}),
		"run":          jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks/"+someTask+"/run", map[string]any{}),
		"trigger":      jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks/"+someTask+"/trigger", map[string]any{}),
		"builder chat": jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks/builder/chat", map[string]any{"message": "every morning"}),
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, keyCaller(t, req))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), msgScheduledTasksNeedUserAccount) {
				t.Errorf("body does not say why: %s", rec.Body.String())
			}
		})
	}

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, keyCaller(t, httptest.NewRequest(http.MethodGet, "/api/v1/scheduledTasks", nil)))
	if list.Code != http.StatusOK {
		t.Fatalf("a key can no longer read its tasks: %d %s", list.Code, list.Body.String())
	}
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v: %s", err, list.Body.String())
	}
	if len(page.Data) != 0 {
		t.Errorf("a refused create left %d task(s) behind", len(page.Data))
	}
}

func TestScheduledTaskRoutes_StoppingStaysOpenToAKeyAndPeopleStillCreate(t *testing.T) {
	mux := scheduledTaskRoutes(t)
	someTask := idgen.New()

	for _, path := range []string{"/disable"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, keyCaller(t, jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks/"+someTask+path, map[string]any{})))
		if rec.Code == http.StatusForbidden {
			t.Errorf("%s was refused to a key: %s", path, rec.Body.String())
		}
	}
	del := httptest.NewRecorder()
	mux.ServeHTTP(del, keyCaller(t, httptest.NewRequest(http.MethodDelete, "/api/v1/scheduledTasks/"+someTask, nil)))
	if del.Code == http.StatusForbidden {
		t.Errorf("delete was refused to a key: %s", del.Body.String())
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withIdentity(jsonRequest(t, http.MethodPost, "/api/v1/scheduledTasks", newTaskBody()), "person@example.com"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("a person's create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestProjectShareRoutes_RefuseAnAPIKey(t *testing.T) {
	mux := scheduledTaskRoutes(t)
	project := idgen.New()
	path := "/api/v1/projects/" + project + "/shares"

	for name, req := range map[string]*http.Request{
		"list":   httptest.NewRequest(http.MethodGet, path, nil),
		"create": jsonRequest(t, http.MethodPost, path, map[string]any{"shares": []map[string]string{{"userEmail": "a@example.com", "accessLevel": "resource_viewer"}}}),
		"delete": jsonRequest(t, http.MethodDelete, path, map[string]any{"userEmails": []string{"a@example.com"}}),
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, keyCaller(t, req))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), msgProjectSharingNeedsUserAccount) {
				t.Errorf("body does not say why: %s", rec.Body.String())
			}
		})
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, path, nil), "person@example.com"))
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), msgProjectSharingNeedsUserAccount) {
		t.Errorf("a person was refused by the account-type gate: %s", rec.Body.String())
	}
}

func TestShareLinkSnapshot_AnswersASystemUserWith403NotA401(t *testing.T) {
	sess := idgen.New()
	_, mux, sessRepo, _ := setupShareLinkTest(t)
	seedSession(t, sessRepo, "user-1", sess, "Chat")
	link := createShareAs(t, mux, sess, "user-1", "user1@example.com", "")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+link.ID+"/updateSnapshot", "", "system:ci-deployer", ""))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), msgShareLinksNeedUserAccount) {
		t.Fatalf("system user got %d %s, want 403 %q", rec.Code, rec.Body.String(), msgShareLinksNeedUserAccount)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, reqWithUser("POST", "/api/v1/share/"+link.ID+"/updateSnapshot", "", "user-2", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a person with no email got %d, want the 401 they always got", rec.Code)
	}
}
