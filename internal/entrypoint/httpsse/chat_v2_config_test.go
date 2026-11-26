// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

func chatV2Serve(mux http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func chatV2RawBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return body
}

func chatV2Object(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want an object", key, m[key])
	}
	return obj
}

func chatV2AssertNoKeys(t *testing.T, value any, path string, banned map[string]bool) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if banned[key] {
				t.Errorf("%s%s is present, want it absent", path, key)
			}
			chatV2AssertNoKeys(t, child, path+key+".", banned)
		}
	case []any:
		for _, child := range v {
			chatV2AssertNoKeys(t, child, path, banned)
		}
	}
}

func TestChatV2ConfigMatchesV1(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	caller := "alice@example.com"
	v1 := chatV2RawBody(t, chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/config", nil), caller, "*")))
	v2 := decodeData[chatv2.ChatConfig](t, chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil), caller, "*")))

	for key, want := range map[string]any{
		"persistenceEnabled":       v2.PersistenceEnabled,
		"schedulerEnabled":         v2.SchedulerEnabled,
		"activationGateEnabled":    v2.ActivationGateEnabled,
		"peopleSearchAvailable":    v2.PeopleSearchAvailable,
		"frontendAppName":          v2.AppName,
		"frontendCollectFeedback":  v2.CollectFeedback,
		"frontendUseAuthorization": v2.AuthorizationEnabled,
		"identityServiceType":      v2.IdentityServiceType,
	} {
		if got := v1[key]; got != want {
			t.Errorf("%s: v1 = %v, v2 = %v", key, got, want)
		}
	}

	limits := chatV2Object(t, v1, "validationLimits")
	if got, want := limits["projectNameMax"], float64(v2.ValidationLimits.ProjectNameMax); got != want {
		t.Errorf("validationLimits.projectNameMax: v1 = %v, v2 = %v", got, want)
	}
	if got, want := limits["maxPerFileUploadSizeBytes"], float64(v2.ValidationLimits.MaxPerFileUploadSizeBytes); got != want {
		t.Errorf("validationLimits.maxPerFileUploadSizeBytes: v1 = %v, v2 = %v", got, want)
	}
}

func TestChatV2ConfigOmitsTheIdpAndVendorFields(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil), "alice@example.com"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	chatV2AssertNoKeys(t, chatV2RawBody(t, rec), "", map[string]bool{
		"externalAuthServiceUrl":  true,
		"externalAuthCallbackUri": true,
		"externalAuthProvider":    true,
		"toolConfigStatus":        true,
		"engineTTS":               true,
		"ttsProvider":             true,
	})
}

func TestChatV2ConfigNullsAreExplicit(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil), "alice@example.com"))
	data := chatV2Object(t, chatV2RawBody(t, rec), "data")
	for _, key := range []string{
		"loginUrl", "welcomeMessage", "logoUrl", "smallLogoUrl",
		"redirectUrl", "serverUrl", "platformServerUrl", "claimKey", "sessionIdleTimeoutSeconds", "googleChatEndpointBase",
	} {
		value, ok := data[key]
		if !ok || value != nil {
			t.Errorf("%s = %v (present %v), want an explicit null", key, value, ok)
		}
	}
}

func TestChatV2ConfigPeopleSearchFollowsTheIdentityService(t *testing.T) {
	mux := newCoverageAdapter(t, func(core *entrypoint.GatewayCore) {
		core.SetIdentityService(&probeIdentitySvc{ok: true})
	}).Mux()
	got := decodeData[chatv2.ChatConfig](t, chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil), "alice@example.com")))
	if !got.PeopleSearchAvailable {
		t.Errorf("peopleSearchAvailable = false, want true when the identity service can search")
	}
}

func TestChatV2ConfigCarriesTheGoogleChatEndpointBaseWithoutATrailingSlash(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://mesh.example.com/")
	adapter, _, _ := setupTestAdapter(t)
	handler := &configHandler{core: adapter.core, logger: adapter.logger}

	v2, err := handler.readChatConfig(t.Context())
	if err != nil {
		t.Fatalf("readChatConfig returned %v, want the config", err)
	}
	rec := httptest.NewRecorder()
	handler.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	v1 := chatV2RawBody(t, rec)

	if v2.GoogleChatEndpointBase == nil || *v2.GoogleChatEndpointBase != "https://mesh.example.com" || v1["googleChatEndpointBase"] != "https://mesh.example.com" {
		t.Errorf("googleChatEndpointBase: v1 = %v, v2 = %v, want https://mesh.example.com in both", v1["googleChatEndpointBase"], v2.GoogleChatEndpointBase)
	}
}

func TestChatV2LoginConfigNeedsNoSignIn(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := chatV2Serve(mux, httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	_ = decodeData[chatv2.ChatLoginConfig](t, rec)
}

func TestChatV2LoginConfigIsSubsetOfConfig(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	login := decodeData[chatv2.ChatLoginConfig](t, chatV2Serve(mux, httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config/login", nil)))
	cfg := decodeData[chatv2.ChatConfig](t, chatV2Serve(mux, withIdentity(httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil), "alice@example.com")))

	if login.AppName == "" || login.IdentityServiceType == "" {
		t.Fatalf("login config = %+v, want the app name and identity service type filled in", login)
	}
	projected := chatv2.ChatLoginConfig{
		AuthorizationEnabled: cfg.AuthorizationEnabled,
		LoginURL:             cfg.LoginURL,
		IdentityServiceType:  cfg.IdentityServiceType,
		AppName:              cfg.AppName,
		WelcomeMessage:       cfg.WelcomeMessage,
		LogoURL:              cfg.LogoURL,
		SmallLogoURL:         cfg.SmallLogoURL,
		DisclaimerText:       cfg.DisclaimerText,
	}
	if !reflect.DeepEqual(login, projected) {
		t.Errorf("login config = %+v, want the same fields as /config: %+v", login, projected)
	}
}
