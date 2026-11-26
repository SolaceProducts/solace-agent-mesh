// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signedInvokeRequest(t *testing.T, auth *BotAuthenticator, appID string, activity any) *http.Request {
	t.Helper()
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("marshal activity: %v", err)
	}
	var sent struct {
		ServiceURL string `json:"serviceUrl"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("read the activity's serviceUrl: %v", err)
	}
	return signedRequest(t, auth, jwt.MapClaims{
		"aud":        appID,
		"iss":        botFrameworkIssuerV1,
		"serviceurl": sent.ServiceURL,
	}, body)
}

func signedRequest(t *testing.T, auth *BotAuthenticator, claims jwt.MapClaims, body []byte) *http.Request {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid = "test-key"
	auth.mu.Lock()
	auth.keys[kid] = &key.PublicKey
	auth.mu.Unlock()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+signed)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestTeamsServer_ServiceURLMustMatchTheToken(t *testing.T) {
	const appID = "app-under-test"
	const serviceURL = "https://smba.trafficmanager.net/amer/"
	activity := func(serviceURL string) []byte {
		body, err := json.Marshal(map[string]any{"type": ActivityTypeMessage, "id": "1", "serviceUrl": serviceURL})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	for name, tc := range map[string]struct {
		claims   jwt.MapClaims
		activity []byte
		want     int
	}{
		"channel token naming the activity's service": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV1, "serviceurl": serviceURL}, activity: activity(serviceURL), want: http.StatusOK,
		},
		"host case differs": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV1, "serviceurl": "https://SMBA.trafficmanager.net/amer/"}, activity: activity(serviceURL), want: http.StatusOK,
		},
		"activity names another service": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV1, "serviceurl": serviceURL}, activity: activity("https://attacker.invalid/"), want: http.StatusUnauthorized,
		},
		"channel token without the claim": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV1}, activity: activity(serviceURL), want: http.StatusUnauthorized,
		},
		"emulator token without the claim": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV2}, activity: activity(serviceURL), want: http.StatusOK,
		},
		"emulator token naming another service": {
			claims: jwt.MapClaims{"aud": appID, "iss": botFrameworkIssuerV2, "serviceurl": serviceURL}, activity: activity("https://attacker.invalid/"), want: http.StatusUnauthorized,
		},
	} {
		t.Run(name, func(t *testing.T) {
			auth := NewBotAuthenticator(appID, teamsNoopLogger())
			var handled atomic.Int64
			s := NewTeamsServer(&TeamsConfig{}, auth, func(context.Context, *Activity) *InvokeResponse {
				handled.Add(1)
				return nil
			}, teamsNoopLogger())
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, signedRequest(t, auth, tc.claims, tc.activity))
			s.Wait()
			if rec.Code != tc.want {
				t.Fatalf("answered %d, want %d", rec.Code, tc.want)
			}
			wantHandled := int64(0)
			if tc.want == http.StatusOK {
				wantHandled = 1
			}
			if got := handled.Load(); got != wantHandled {
				t.Errorf("activities handled = %d, want %d", got, wantHandled)
			}
		})
	}
}

func TestTeamsServer_DialogSubmitIsAnsweredWithEmptyWireBody(t *testing.T) {
	const appID = "app-under-test"
	auth := NewBotAuthenticator(appID, teamsNoopLogger())
	s := NewTeamsServer(&TeamsConfig{}, auth, func(context.Context, *Activity) *InvokeResponse {
		return &InvokeResponse{Status: http.StatusOK, EmptyBody: true}
	}, teamsNoopLogger())

	req := signedInvokeRequest(t, auth, appID, map[string]any{
		"type": ActivityTypeInvoke,
		"name": InvokeMessageSubmitAction,
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("submit response must have an empty body, got %d bytes: %q", len(body), body)
	}
}

func TestTeamsServer_FetchTaskWritesTheDialog(t *testing.T) {
	const appID = "app-under-test"
	auth := NewBotAuthenticator(appID, teamsNoopLogger())
	s := NewTeamsServer(&TeamsConfig{}, auth, func(context.Context, *Activity) *InvokeResponse {
		return &InvokeResponse{Status: http.StatusOK, Body: map[string]any{
			"task": map[string]any{"type": "continue"},
		}}
	}, teamsNoopLogger())

	req := signedInvokeRequest(t, auth, appID, map[string]any{
		"type": ActivityTypeInvoke,
		"name": InvokeMessageFetchTask,
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("dialog response is not JSON: %v (%q)", err, rec.Body.String())
	}
	task, ok := got["task"].(map[string]any)
	if !ok || task["type"] != "continue" {
		t.Errorf("want the task envelope on the wire, got %q", rec.Body.String())
	}
}

func TestTeamsServer_InvokeRefusedAfterClose(t *testing.T) {
	const appID = "app-under-test"
	auth := NewBotAuthenticator(appID, teamsNoopLogger())
	handled := false
	s := NewTeamsServer(&TeamsConfig{}, auth, func(context.Context, *Activity) *InvokeResponse {
		handled = true
		return &InvokeResponse{Status: http.StatusOK, EmptyBody: true}
	}, teamsNoopLogger())
	s.Close()

	req := signedInvokeRequest(t, auth, appID, map[string]any{
		"type": ActivityTypeInvoke,
		"name": InvokeMessageSubmitAction,
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 so Bot Framework retries elsewhere", rec.Code)
	}
	if handled {
		t.Error("the invoke was handled after Close; its work would not be drained")
	}
}

func TestTeamsServer_WaitDrainsTrackedBackground(t *testing.T) {
	s := NewTeamsServer(&TeamsConfig{}, nil, func(context.Context, *Activity) *InvokeResponse { return nil }, teamsNoopLogger())

	started := make(chan struct{})
	var done atomic.Bool
	if !s.TrackBackground(func() {
		close(started)
		time.Sleep(50 * time.Millisecond)
		done.Store(true)
	}) {
		t.Fatal("TrackBackground refused on an open server")
	}
	<-started

	s.Close()
	s.Wait()
	if !done.Load() {
		t.Error("Wait returned before tracked background work finished")
	}
}

func TestTeamsAdapter_InvokeDispatchRoutesFeedbackNames(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	a.registerFeedback("task-1", &TeamsTaskMeta{SessionID: "sess-1", ConversationID: "conv-1"}, "msg-1", "the answer")

	fetch := a.handleInvokeActivity(t.Context(), &Activity{
		Type:         ActivityTypeInvoke,
		Name:         InvokeMessageFetchTask,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"data":{"actionName":"feedback","actionValue":{"reaction":"like"}}}`),
	})
	if fetch == nil || fetch.Body == nil {
		t.Fatal("message/fetchTask did not reach the dialog handler")
	}

	submit := a.handleInvokeActivity(t.Context(), &Activity{
		Type:         ActivityTypeInvoke,
		Name:         InvokeMessageSubmitAction,
		ReplyToID:    "msg-1",
		Conversation: Conversation{ID: "conv-1"},
		Value:        json.RawMessage(`{"actionValue":{"feedback":"{\"rating\":\"up\"}"}}`),
	})
	if submit == nil || !submit.EmptyBody {
		t.Fatalf("message/submitAction did not reach the submit handler, got %+v", submit)
	}
	a.server.Wait()
}
