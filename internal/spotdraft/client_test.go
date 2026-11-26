// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spotdraft_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft/spotdrafttest"
)

const (
	clickwrapID = "1a3799d1-d806-4a1f-bc38-c980399047b5"
	ada         = "auth0|ada"
)

func newClient(t *testing.T) (*spotdraft.Client, *spotdrafttest.Fake, spotdraft.Clickwrap) {
	t.Helper()
	fake := spotdrafttest.New()
	clickwrap := fake.AddClickwrap(clickwrapID, "End User License Agreement", "Free Model Terms")
	server := httptest.NewServer(fake.Handler())
	t.Cleanup(server.Close)
	return spotdraft.New(server.URL, spotdraft.WithFirstRetryDelay(time.Millisecond)), fake, clickwrap
}

func TestTheClickwrapListsEachAgreementsCurrentVersion(t *testing.T) {
	client, _, want := newClient(t)

	got, err := client.Clickwrap(t.Context(), clickwrapID)
	if err != nil {
		t.Fatalf("Clickwrap: %v", err)
	}
	if got.ID != want.ID || len(got.Agreements) != 2 || got.Agreements[1].CurrentVersion.Name != "Free Model Terms" || got.Agreements[0].CurrentVersion.PublicURL == "" {
		t.Fatalf("clickwrap = %+v, want %+v", got, want)
	}
	if got.CheckboxText() != spotdrafttest.CheckboxText {
		t.Fatalf("checkbox text = %q, want Legal's wording %q", got.CheckboxText(), spotdrafttest.CheckboxText)
	}
}

func TestTheClickwrapIsFetchedAgainAfterAServerError(t *testing.T) {
	client, fake, _ := newClient(t)
	fake.FailNext(spotdrafttest.OpClickwrap, http.StatusBadGateway, "")

	if _, err := client.Clickwrap(t.Context(), clickwrapID); err != nil {
		t.Fatalf("Clickwrap: %v", err)
	}
	if got := fake.Calls(spotdrafttest.OpClickwrap); got != 2 {
		t.Fatalf("clickwrap calls = %d, want 2", got)
	}
}

func TestARefusedClickwrapIsNotFetchedAgain(t *testing.T) {
	client, fake, _ := newClient(t)

	_, err := client.Clickwrap(t.Context(), "an-unknown-clickwrap")
	if err == nil || spotdraft.Retryable(err) {
		t.Fatalf("err = %v, want a refusal that is not retried", err)
	}
	if got := fake.Calls(spotdrafttest.OpClickwrap); got != 1 {
		t.Fatalf("clickwrap calls = %d, want 1", got)
	}
}

func TestAClickwrapWithNoPublishedAgreementCannotBeAgreedTo(t *testing.T) {
	client, fake, _ := newClient(t)
	fake.AddClickwrap("empty-clickwrap")

	if _, err := client.Clickwrap(t.Context(), "empty-clickwrap"); err != spotdraft.ErrNoAgreements {
		t.Fatalf("err = %v, want ErrNoAgreements", err)
	}
}

func TestConsentStatusReportsAnAgreementOnlyOnceMade(t *testing.T) {
	client, fake, _ := newClient(t)

	before, err := client.ConsentStatus(t.Context(), clickwrapID, ada)
	if err != nil || before != spotdraft.NotAccepted || before.Agreed() {
		t.Fatalf("before = %q, %v; want NOT_ACCEPTED", before, err)
	}
	fake.MarkAgreed(clickwrapID, ada)
	after, err := client.ConsentStatus(t.Context(), clickwrapID, ada)
	if err != nil || !after.Agreed() {
		t.Fatalf("after = %q, %v; want an agreement", after, err)
	}
}

func TestConsentStatusAsksOnce(t *testing.T) {
	client, fake, _ := newClient(t)
	fake.FailNext(spotdrafttest.OpConsentStatus, http.StatusServiceUnavailable, "")

	if _, err := client.ConsentStatus(t.Context(), clickwrapID, ada); err == nil {
		t.Fatal("ConsentStatus succeeded, want the 503")
	}
	if got := fake.Calls(spotdrafttest.OpConsentStatus); got != 1 {
		t.Fatalf("consent_status calls = %d, want 1", got)
	}
}

func TestExecuteRecordsEveryAgreementAtItsCurrentVersionWithWhoAgreed(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	opened := map[int64]bool{clickwrap.Agreements[0].ID: true}

	contractID, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada, Email: "ada@example.com", FirstName: "Ada", LastName: "Lovelace", Opened: opened})
	if err != nil || contractID == "" {
		t.Fatalf("Execute = %q, %v; want a contract", contractID, err)
	}
	executions := fake.Executions()
	if len(executions) != 1 {
		t.Fatalf("executions = %d, want 1", len(executions))
	}
	got := executions[0]
	if got.UserIdentifier != ada || got.UserEmail != "ada@example.com" || got.FirstName != "Ada" || got.LastName != "Lovelace" {
		t.Fatalf("execution = %+v", got)
	}
	for i, a := range clickwrap.Agreements {
		if got.Agreements[i].VersionID != a.CurrentVersion.ID || got.Agreements[i].HasClicked != opened[a.ID] {
			t.Fatalf("agreement %d = %+v, want version %d, opened %v", i, got.Agreements[i], a.CurrentVersion.ID, opened[a.ID])
		}
	}
}

func TestExecuteLeavesOutClickwrapIDLikeSpotDraftsOwnSDKBecauseTheLiveAPIAnswers500ToIt(t *testing.T) {
	client, fake, clickwrap := newClient(t)

	contractID, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada})
	if err != nil || contractID == "" {
		t.Fatalf("Execute = %q, %v; want a contract", contractID, err)
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 1 {
		t.Fatalf("execute calls = %d, want 1", got)
	}
}

func TestExecuteSendsThePublishedSchemasClickwrapIDOnlyOnceSpotDraftRefusesTheSDKForm(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.EnforcePublishedExecuteSchema()

	contractID, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada})
	if err != nil || contractID == "" {
		t.Fatalf("Execute = %q, %v; want a contract", contractID, err)
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 2 {
		t.Fatalf("execute calls = %d, want 2: the refused SDK form, then the documented one", got)
	}
	if got := len(fake.Executions()); got != 1 {
		t.Fatalf("contracts = %d, want 1", got)
	}
	if !fake.Agreed(clickwrapID, ada) {
		t.Fatal("SpotDraft holds no agreement")
	}
}

func TestExecuteRetriesOnlyOnceTheCheckShowsTheFailedAttemptDidNotLand(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.FailNext(spotdrafttest.OpExecute, http.StatusServiceUnavailable, "")

	contractID, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada})
	if err != nil || contractID == "" {
		t.Fatalf("Execute = %q, %v", contractID, err)
	}
	if got := fake.Calls(spotdrafttest.OpConsentStatus); got != 1 {
		t.Fatalf("consent_status calls = %d, want 1 before the retry", got)
	}
	if got := len(fake.Executions()); got != 1 {
		t.Fatalf("contracts = %d, want 1", got)
	}
}

func TestExecuteNeverCreatesASecondContractWhenTheFirstAnswerWasLost(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.FailNextExecuteAfterRecording(http.StatusGatewayTimeout)

	contractID, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada})
	if err != nil || contractID != "" {
		t.Fatalf("Execute = %q, %v; want success with no contract ID", contractID, err)
	}
	if got := len(fake.Executions()); got != 1 {
		t.Fatalf("contracts = %d, want 1", got)
	}
}

func TestExecuteStopsWhenTheCheckBeforeARetryFails(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.FailNext(spotdrafttest.OpExecute, http.StatusServiceUnavailable, "")
	fake.FailNext(spotdrafttest.OpConsentStatus, http.StatusServiceUnavailable, "")

	if _, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada}); err == nil {
		t.Fatal("Execute succeeded, want the first attempt's error")
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 1 {
		t.Fatalf("execute calls = %d, want 1", got)
	}
}

func TestExecuteNeverRetriesARefusalOfEitherForm(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.FailNext(spotdrafttest.OpExecute, http.StatusBadRequest, "")
	fake.FailNext(spotdrafttest.OpExecute, http.StatusBadRequest, "")

	if _, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada}); err == nil {
		t.Fatal("Execute succeeded, want the 400")
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 2 {
		t.Fatalf("execute calls = %d, want 2: the SDK's form, then the documented one", got)
	}
	if got := fake.Calls(spotdrafttest.OpConsentStatus); got != 0 {
		t.Fatalf("consent_status calls = %d, want 0", got)
	}
}

func TestExecuteGivesUpAfterThreeAttempts(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	for range 3 {
		fake.FailNext(spotdrafttest.OpExecute, http.StatusTooManyRequests, "")
	}

	if _, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada}); err == nil {
		t.Fatal("Execute succeeded, want the third 429")
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 3 {
		t.Fatalf("execute calls = %d, want 3", got)
	}
	if got := fake.Calls(spotdrafttest.OpConsentStatus); got != 2 {
		t.Fatalf("consent_status calls = %d, want one before each retry", got)
	}
}

func TestExecuteGivesUpWhileSpotDraftIsDown(t *testing.T) {
	client, fake, clickwrap := newClient(t)
	fake.SetDown(true)

	if _, err := client.Execute(t.Context(), clickwrap, spotdraft.Acceptance{UserIdentifier: ada}); err == nil || !spotdraft.Retryable(err) {
		t.Fatalf("err = %v, want a failure that may pass later", err)
	}
	if got := fake.Calls(spotdrafttest.OpExecute); got != 1 {
		t.Fatalf("execute calls = %d, want 1: the check before the retry also failed", got)
	}
}

func TestEveryCallSendsTheClickwrapAndALocalhostOrigin(t *testing.T) {
	var seen []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"NOT_ACCEPTED","trigger_acceptance":true}`))
	}))
	t.Cleanup(server.Close)

	if _, err := spotdraft.New(server.URL).ConsentStatus(t.Context(), clickwrapID, ada); err != nil {
		t.Fatalf("ConsentStatus: %v", err)
	}
	if got := seen[0].Get("clickwrap-id"); got != clickwrapID {
		t.Fatalf("clickwrap-id = %q", got)
	}
	if got := seen[0].Get("Origin"); got != spotdraft.Origin {
		t.Fatalf("Origin = %q", got)
	}
	if seen[0].Get("client-secret") != "" || seen[0].Get("Authorization") != "" {
		t.Fatal("the desktop must never send a credential")
	}
}

func TestRetryableSeparatesPassingFailuresFromFaults(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&spotdraft.StatusError{Code: http.StatusInternalServerError}, true},
		{&spotdraft.StatusError{Code: http.StatusTooManyRequests}, true},
		{&spotdraft.StatusError{Code: http.StatusForbidden}, false},
		{&spotdraft.StatusError{Code: http.StatusNotFound}, false},
		{context.DeadlineExceeded, true},
		{context.Canceled, false},
		{spotdraft.ErrNoAgreements, false},
	}
	for _, c := range cases {
		if got := spotdraft.Retryable(c.err); got != c.want {
			t.Errorf("Retryable(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
