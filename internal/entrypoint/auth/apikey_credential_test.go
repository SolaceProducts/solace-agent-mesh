// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
)

const testKeyPlaintext = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type fakeKeyAuthority struct {
	resolution apikey.Resolution
	err        error
	gotHash    string
	calls      int
}

func (f *fakeKeyAuthority) Validate(_ context.Context, keyHash string) (apikey.Resolution, error) {
	f.calls++
	f.gotHash = keyHash
	return f.resolution, f.err
}

var boundedAPIKeyReasons = []string{
	APIKeyReasonInvalid,
	APIKeyReasonNotEnabled,
	APIKeyReasonModeDisabled,
	APIKeyReasonUnavailable,
	APIKeyReasonSurfaceNotSupported,
	APIKeyReasonWrongPosition,
}

func TestResolveAPIKey_ReasonsAreBounded(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"invalid":               apikey.ErrInvalidKey,
		"not enabled":           apikey.ErrNotEnabled,
		"mode disabled":         apikey.ErrAuthorizationModeDisabled,
		"unavailable":           apikey.ErrAuthorityUnavailable,
		"wrapped invalid":       fmt.Errorf("look up key: %w", apikey.ErrInvalidKey),
		"wrapped unavailable":   fmt.Errorf("resolve owner scopes: %w", apikey.ErrAuthorityUnavailable),
		"unanticipated":         errors.New("connection reset by peer while reading row 4"),
		"unanticipated wrapped": fmt.Errorf("outer: %w", errors.New("pq: relation \"api_keys\" does not exist")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, rejection := ResolveAPIKey(t.Context(), &fakeKeyAuthority{err: err}, testKeyPlaintext)
			if rejection == nil {
				t.Fatal("expected a rejection")
			}
			if !slices.Contains(boundedAPIKeyReasons, rejection.Reason) {
				t.Errorf("reason %q is outside the bounded set %v", rejection.Reason, boundedAPIKeyReasons)
			}
			if strings.Contains(rejection.Reason, " ") {
				t.Errorf("reason %q contains whitespace, so it did not come from a constant", rejection.Reason)
			}
		})
	}
	if got := RejectAPIKeyPosition().Reason; !slices.Contains(boundedAPIKeyReasons, got) {
		t.Errorf("position reason %q is outside the bounded set", got)
	}
}

func TestResolveAPIKey_UnanticipatedErrorFailsLoudNotQuiet(t *testing.T) {
	t.Parallel()
	_, rejection := ResolveAPIKey(t.Context(),
		&fakeKeyAuthority{err: errors.New("no such table")}, testKeyPlaintext)

	if rejection.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 so a broken authority is not reported as a bad credential", rejection.Status)
	}
	if rejection.Reason != APIKeyReasonUnavailable {
		t.Errorf("reason = %q, want %q", rejection.Reason, APIKeyReasonUnavailable)
	}
}

func TestResolveAPIKey_StatusAndReasonPerOutcome(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err        error
		wantStatus int
		wantReason string
	}{
		"invalid":       {apikey.ErrInvalidKey, http.StatusUnauthorized, APIKeyReasonInvalid},
		"not enabled":   {apikey.ErrNotEnabled, http.StatusUnauthorized, APIKeyReasonNotEnabled},
		"mode disabled": {apikey.ErrAuthorizationModeDisabled, http.StatusUnauthorized, APIKeyReasonModeDisabled},
		"unavailable":   {apikey.ErrAuthorityUnavailable, http.StatusServiceUnavailable, APIKeyReasonUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, rejection := ResolveAPIKey(t.Context(), &fakeKeyAuthority{err: tc.err}, testKeyPlaintext)
			if rejection.Status != tc.wantStatus {
				t.Errorf("status = %d, want %d", rejection.Status, tc.wantStatus)
			}
			if rejection.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", rejection.Reason, tc.wantReason)
			}
		})
	}
}

func TestResolveAPIKey_EveryRefusalLooksIdenticalToTheCaller(t *testing.T) {
	t.Parallel()
	var bodies, codes []string
	for _, err := range []error{
		apikey.ErrInvalidKey,
		apikey.ErrNotEnabled,
		apikey.ErrAuthorizationModeDisabled,
	} {
		_, rejection := ResolveAPIKey(t.Context(), &fakeKeyAuthority{err: err}, testKeyPlaintext)
		bodies = append(bodies, rejection.Message)
		codes = append(codes, string(rejection.Code))
	}
	_, nilRejection := ResolveAPIKey(t.Context(), nil, testKeyPlaintext)
	bodies = append(bodies, nilRejection.Message)
	codes = append(codes, string(nilRejection.Code))
	bodies = append(bodies, RejectAPIKeyPosition().Message)
	codes = append(codes, string(RejectAPIKeyPosition().Code))

	for i := range bodies {
		if bodies[i] != bodies[0] {
			t.Errorf("401 body %d = %q, want the same %q as every other refusal", i, bodies[i], bodies[0])
		}
		if codes[i] != codes[0] {
			t.Errorf("401 error code %d = %q, want the same %q as every other refusal", i, codes[i], codes[0])
		}
	}
}

func TestResolveAPIKey_NilAuthorityRefusesWithoutCallingOut(t *testing.T) {
	t.Parallel()
	identity, rejection := ResolveAPIKey(t.Context(), nil, testKeyPlaintext)
	if identity != nil {
		t.Fatal("a nil authority produced an identity")
	}
	if rejection.Reason != APIKeyReasonNotEnabled {
		t.Errorf("reason = %q, want %q", rejection.Reason, APIKeyReasonNotEnabled)
	}
	if rejection.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rejection.Status)
	}
}

func TestResolveAPIKey_NilHolderRefusesAsNotEnabled(t *testing.T) {
	t.Parallel()
	var holder *apikey.LazyAuthority
	identity, rejection := ResolveAPIKey(t.Context(), holder, testKeyPlaintext)
	if identity != nil {
		t.Fatal("a nil holder produced an identity")
	}
	if rejection.Reason != APIKeyReasonNotEnabled {
		t.Errorf("reason = %q, want %q", rejection.Reason, APIKeyReasonNotEnabled)
	}
	if rejection.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rejection.Status)
	}
}

func TestResolveAPIKey_HandsTheAuthorityOnlyTheHash(t *testing.T) {
	t.Parallel()
	const plaintext = testKeyPlaintext
	authority := &fakeKeyAuthority{resolution: apikey.Resolution{OwnerSub: "system:ci"}}

	if _, rejection := ResolveAPIKey(t.Context(), authority, plaintext); rejection != nil {
		t.Fatalf("unexpected rejection: %+v", rejection)
	}
	if authority.gotHash == plaintext {
		t.Fatal("the plaintext itself was handed to the authority")
	}
	if authority.gotHash != apikey.Hash(plaintext) {
		t.Errorf("authority got %q, want the SHA-256 of the complete plaintext", authority.gotHash)
	}
	if strings.Contains(authority.gotHash, apikey.Marker) {
		t.Error("the value handed over still carries the key marker")
	}
}

func TestResolveAPIKey_IdentityShape(t *testing.T) {
	t.Parallel()
	authority := &fakeKeyAuthority{resolution: apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "11111111-1111-1111-1111-111111111111",
		OwnerSub: "system:ci-deployer",
		Scopes:   []string{"agent_builder:_:create"},
	}}

	identity, rejection := ResolveAPIKey(t.Context(), authority, testKeyPlaintext)
	if rejection != nil {
		t.Fatalf("unexpected rejection: %+v", rejection)
	}
	if identity.UserID() != "system:ci-deployer" {
		t.Errorf("UserID = %q, want the owner's system: subject", identity.UserID())
	}
	if identity.Email() != "" {
		t.Errorf("Email = %q, want empty: a system user has no mailbox", identity.Email())
	}
	if identity.AuthMethod() != APIKeyAuthMethod {
		t.Errorf("AuthMethod = %q, want %q", identity.AuthMethod(), APIKeyAuthMethod)
	}
	if identity.CredentialID() != "key-1" {
		t.Errorf("CredentialID = %q, want the key's id: without it every action is attributed only to a system user holding several keys", identity.CredentialID())
	}
	if identity.PlatformUserID() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("PlatformUserID = %q, want the owner's users.id so its spend books against that row", identity.PlatformUserID())
	}
	if !identity.Authenticated() {
		t.Error("Authenticated = false on a resolved key")
	}
	if !slices.Equal(identity.Scopes(), []string{"agent_builder:_:create"}) {
		t.Errorf("Scopes = %v, want the surviving set", identity.Scopes())
	}
}

func TestResolveAPIKey_EmptyScopesStillAuthenticates(t *testing.T) {
	t.Parallel()
	authority := &fakeKeyAuthority{resolution: apikey.Resolution{
		KeyID: "key-1", OwnerSub: "system:ci", Scopes: nil,
	}}

	identity, rejection := ResolveAPIKey(t.Context(), authority, testKeyPlaintext)
	if rejection != nil {
		t.Fatalf("an owner with no covering roles produced a 401: %+v", rejection)
	}
	if len(identity.Scopes()) != 0 {
		t.Errorf("Scopes = %v, want empty", identity.Scopes())
	}
	if !identity.ScopesResolved() {
		t.Error("ScopesResolved = false, so a downstream resolver would fill in default roles")
	}
}

func TestRejectAPIKeyPosition(t *testing.T) {
	t.Parallel()
	rejection := RejectAPIKeyPosition()
	if rejection.Status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rejection.Status)
	}
	if rejection.Reason != APIKeyReasonWrongPosition {
		t.Errorf("reason = %q, want %q", rejection.Reason, APIKeyReasonWrongPosition)
	}
}

func TestResolveAPIKey_MalformedKeyNeverReachesTheAuthority(t *testing.T) {
	t.Parallel()
	for name, presented := range map[string]string{
		"marker only":       apikey.Marker,
		"too short":         apikey.Marker + "x",
		"too long":          testKeyPlaintext + "A",
		"outside base64url": apikey.Marker + strings.Repeat("!", 43),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			authority := &fakeKeyAuthority{}
			identity, rejection := ResolveAPIKey(t.Context(), authority, presented)
			if identity != nil || rejection == nil {
				t.Fatalf("identity=%v rejection=%v, want a refusal", identity, rejection)
			}
			if authority.calls != 0 {
				t.Errorf("the authority was asked %d times about a string that cannot be a key", authority.calls)
			}
			if rejection.Status != http.StatusUnauthorized || rejection.Reason != APIKeyReasonInvalid {
				t.Errorf("rejection = %d/%s, want 401/%s", rejection.Status, rejection.Reason, APIKeyReasonInvalid)
			}
		})
	}

	_, unknown := ResolveAPIKey(t.Context(), &fakeKeyAuthority{err: apikey.ErrInvalidKey}, testKeyPlaintext)
	_, malformed := ResolveAPIKey(t.Context(), &fakeKeyAuthority{}, apikey.Marker+"x")
	if *unknown != *malformed {
		t.Errorf("a malformed key is answered differently from an unknown one:\n%+v\n%+v", malformed, unknown)
	}
}

type capturedLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capturedLogs) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *capturedLogs) records(msg string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, line := range bytes.Split(c.buf.Bytes(), []byte("\n")) {
		var rec map[string]any
		if json.Unmarshal(line, &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

func captureDefaultLogs(t *testing.T) *capturedLogs {
	t.Helper()
	logs := &capturedLogs{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func TestResolveAPIKey_LogsTheCauseOfAnOutageOnce(t *testing.T) {
	logs := captureDefaultLogs(t)
	cause := fmt.Errorf("%w: look up key: connection refused", apikey.ErrAuthorityUnavailable)

	_, rejection := ResolveAPIKey(t.Context(), &fakeKeyAuthority{err: cause}, testKeyPlaintext)

	if rejection == nil || rejection.Status != http.StatusServiceUnavailable {
		t.Fatalf("rejection = %+v, want a 503", rejection)
	}
	records := logs.records("api key authority unavailable")
	if len(records) != 1 {
		t.Fatalf("logged %d times, want exactly once", len(records))
	}
	if records[0]["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", records[0]["level"])
	}
	if got, _ := records[0]["error"].(string); !strings.Contains(got, "connection refused") {
		t.Errorf("error field = %q, want it to carry the cause", got)
	}
	raw, _ := json.Marshal(records[0])
	for label, secret := range map[string]string{"plaintext": testKeyPlaintext, "hash": apikey.Hash(testKeyPlaintext)} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the log line carries the key %s", label)
		}
	}
}

func TestResolveAPIKey_ACallerWhoHasGoneIsNotAnOutage(t *testing.T) {
	logs := captureDefaultLogs(t)
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	cause := fmt.Errorf("%w: look up key: %w", apikey.ErrAuthorityUnavailable, context.Canceled)

	ResolveAPIKey(gone, &fakeKeyAuthority{err: cause}, testKeyPlaintext)

	if n := len(logs.records("api key authority unavailable")); n != 0 {
		t.Errorf("a disconnect was logged as an outage %d times", n)
	}
}

func TestAPIKeyRejection_RespondAnswersALiveCallerAndNotOneWhoHasGone(t *testing.T) {
	t.Parallel()
	rejection := newAPIKeyRejection(APIKeyReasonInvalid)

	live := httptest.NewRecorder()
	rejection.Respond(live, httptest.NewRequest(http.MethodGet, "/x", nil))
	if live.Code != http.StatusUnauthorized || !strings.Contains(live.Body.String(), msgAPIKeyRejected) {
		t.Errorf("live caller got %d %q, want the 401 refusal", live.Code, live.Body.String())
	}

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	departed := httptest.NewRecorder()
	rejection.Respond(departed, httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(gone))
	if departed.Body.Len() != 0 || departed.Header().Get("Content-Type") != "" {
		t.Errorf("a caller who has gone was written to: %q", departed.Body.String())
	}
}
