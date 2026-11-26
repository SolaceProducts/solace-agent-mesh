// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type recordingT struct {
	errs []string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func intPtr(v int) *int { return &v }

func driveCounts(t *testing.T, p *MockOAuthProvider) {
	t.Helper()

	verifier := oauth2.GenerateVerifier()
	challenge := oauth2.S256ChallengeFromVerifier(verifier)
	code := p.IssueAuthorizationCode(testUserID, challenge, testRedirectURI, testClientID)
	if status, body := exchangeCode(t, p.TokenURL(), authCodeForm(code, verifier)); status != 200 {
		t.Fatalf("seed auth_code: status=%d body=%v", status, body)
	}

	challenge2 := oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier())
	code2 := p.IssueAuthorizationCode(testUserID, challenge2, testRedirectURI, testClientID)
	if status, _ := exchangeCode(t, p.TokenURL(), authCodeForm(code2, "wrong-verifier")); status != 400 {
		t.Fatalf("seed auth_code failure: unexpected status=%d", status)
	}
}

func newProviderForAssertTest(t *testing.T) *MockOAuthProvider {
	t.Helper()
	p, err := NewMockOAuthProvider(MockOAuthProviderConfig{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestAssertOAuthState_NilScenarioIsNoOp(t *testing.T) {
	h := &TestHarness{}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 0 {
		t.Errorf("nil scenario should be no-op, got errors: %v", rec.errs)
	}
}

func TestAssertOAuthState_NilTriggerIsNoOp(t *testing.T) {
	h := &TestHarness{
		scenario: &TestScenario{},
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 0 {
		t.Errorf("nil trigger should be no-op, got errors: %v", rec.errs)
	}
}

func TestAssertOAuthState_NilProviderIsNoOp(t *testing.T) {
	h := &TestHarness{
		scenario: &TestScenario{
			OAuthTrigger: &OAuthTrigger{
				ExpectedAuthCodeGrants: intPtr(99),
			},
		},
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 0 {
		t.Errorf("nil provider should be no-op, got errors: %v", rec.errs)
	}
}

func TestAssertOAuthState_AllNilFieldsSkip(t *testing.T) {
	p := newProviderForAssertTest(t)
	driveCounts(t, p)

	h := &TestHarness{
		scenario:      &TestScenario{OAuthTrigger: &OAuthTrigger{}},
		oauthProvider: p,
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 0 {
		t.Errorf("all-nil expected fields should skip, got errors: %v", rec.errs)
	}
}

func TestAssertOAuthState_AuthCodeMismatch(t *testing.T) {
	p := newProviderForAssertTest(t)
	driveCounts(t, p)

	h := &TestHarness{
		scenario: &TestScenario{
			OAuthTrigger: &OAuthTrigger{
				ExpectedAuthCodeGrants: intPtr(2),
			},
		},
		oauthProvider: p,
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(rec.errs), rec.errs)
	}
	msg := rec.errs[0]
	for _, want := range []string{"authorization_code grants", "got 1", "want 2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestAssertOAuthState_RefreshMismatch(t *testing.T) {
	p := newProviderForAssertTest(t)
	driveCounts(t, p)

	h := &TestHarness{
		scenario: &TestScenario{
			OAuthTrigger: &OAuthTrigger{
				ExpectedRefreshGrants: intPtr(1),
			},
		},
		oauthProvider: p,
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(rec.errs), rec.errs)
	}
	msg := rec.errs[0]
	for _, want := range []string{"refresh_token grants", "got 0", "want 1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestAssertOAuthState_ErrorMismatch(t *testing.T) {
	p := newProviderForAssertTest(t)
	driveCounts(t, p)

	h := &TestHarness{
		scenario: &TestScenario{
			OAuthTrigger: &OAuthTrigger{
				ExpectedErrors: intPtr(0),
			},
		},
		oauthProvider: p,
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 1 {
		t.Fatalf("want exactly 1 error, got %d: %v", len(rec.errs), rec.errs)
	}
	msg := rec.errs[0]
	for _, want := range []string{"errors", "got 1", "want 0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestAssertOAuthState_MultipleMismatches(t *testing.T) {
	p := newProviderForAssertTest(t)
	driveCounts(t, p)

	h := &TestHarness{
		scenario: &TestScenario{
			OAuthTrigger: &OAuthTrigger{
				ExpectedAuthCodeGrants: intPtr(0),
				ExpectedRefreshGrants:  intPtr(2),
				ExpectedErrors:         intPtr(0),
			},
		},
		oauthProvider: p,
	}
	rec := &recordingT{}
	h.assertOAuthStateImpl(rec)
	if len(rec.errs) != 3 {
		t.Fatalf("want 3 errors, got %d: %v", len(rec.errs), rec.errs)
	}
	if !strings.Contains(rec.errs[0], "authorization_code") {
		t.Errorf("errs[0] = %q, want authorization_code first", rec.errs[0])
	}
	if !strings.Contains(rec.errs[1], "refresh_token") {
		t.Errorf("errs[1] = %q, want refresh_token second", rec.errs[1])
	}
	if !strings.Contains(rec.errs[2], "errors") {
		t.Errorf("errs[2] = %q, want errors third", rec.errs[2])
	}
}
