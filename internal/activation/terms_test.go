// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package activation

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft/spotdrafttest"
)

func signedIn(t *testing.T) (*Service, *spotdrafttest.Fake, string) {
	t.Helper()
	home := t.TempDir()
	s := newService(t, home, newFakeSolaceID().signsInAs(ada))
	_, err := signIn(t, s)
	require.NoError(t, err)
	fake, _ := spotDraftFor(t, home)
	return s, fake, home
}

func TestTheTermsAreOneCheckboxWithLegalsWordingAndEveryDocument(t *testing.T) {
	s, _, _ := signedIn(t)

	terms, err := s.Terms(t.Context())

	require.NoError(t, err)
	assert.False(t, terms.Agreed)
	assert.Equal(t, spotdrafttest.CheckboxText, terms.CheckboxText)
	require.Len(t, terms.Documents, 2)
	assert.Equal(t, "End User License Agreement", terms.Documents[0].Name)
	assert.Equal(t, "LLM Credits Terms", terms.Documents[1].Name)
	assert.Equal(t, "1.0", terms.Documents[0].Version)
	assert.NotEmpty(t, terms.Documents[0].URL)
}

func TestTheTermsShowTheAgreementSpotDraftAlreadyHolds(t *testing.T) {
	s, fake, _ := signedIn(t)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)

	terms, err := s.Terms(t.Context())

	require.NoError(t, err)
	assert.True(t, terms.Agreed)
	assert.Len(t, terms.Documents, 2, "an agreed checkbox still links its documents")
}

func TestABuildWithoutTheFreeModelShowsTheSameCheckbox(t *testing.T) {
	home := t.TempDir()
	s := NewService(NewStore(home), newFakeSolaceID().signsInAs(ada), slog.New(slog.DiscardHandler), linkedTo(t, home))
	t.Cleanup(s.Close)
	_, err := signIn(t, s)
	require.NoError(t, err)

	terms, err := s.Terms(t.Context())

	require.NoError(t, err)
	assert.Len(t, terms.Documents, 2)
}

func TestAConsentCheckSpotDraftCannotAnswerShowsTheCheckboxUnticked(t *testing.T) {
	s, fake, _ := signedIn(t)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)
	fake.FailNext(spotdrafttest.OpConsentStatus, http.StatusServiceUnavailable, "")

	terms, err := s.Terms(t.Context())

	require.NoError(t, err)
	assert.False(t, terms.Agreed)
}

func TestTheTermsAreUnavailableWhileSpotDraftIsDown(t *testing.T) {
	s, fake, _ := signedIn(t)
	fake.SetDown(true)

	_, err := s.Terms(t.Context())

	require.ErrorIs(t, err, ErrTermsUnavailable)
}

func TestTheTermsNeedASignedInAccountAndALinkedAgreement(t *testing.T) {
	s := newService(t, t.TempDir(), newFakeSolaceID())
	_, err := s.Terms(t.Context())
	require.ErrorIs(t, err, ErrNotSignedIn)

	home := t.TempDir()
	unlinked := NewService(NewStore(home), newFakeSolaceID().signsInAs(ada), slog.New(slog.DiscardHandler))
	t.Cleanup(unlinked.Close)
	_, err = signIn(t, unlinked)
	require.NoError(t, err)
	_, err = unlinked.Terms(t.Context())
	require.ErrorIs(t, err, ErrAgreementUnlinked)
	_, err = unlinked.Accept(t.Context(), testActor, false, nil)
	require.ErrorIs(t, err, ErrAgreementUnlinked)
	assert.False(t, unlinked.Active())
}

func TestGetStartedRecordsOneContractKeyedByTheSubjectForBothAcceptances(t *testing.T) {
	s, fake, home := signedIn(t)
	terms, err := s.Terms(t.Context())
	require.NoError(t, err)

	_, err = s.Accept(t.Context(), testActor, true, []string{terms.Documents[1].ID})

	require.NoError(t, err)
	executions := fake.Executions()
	require.Len(t, executions, 1)
	e := executions[0]
	assert.Equal(t, testEULAClickwrap, e.ClickwrapPublicID)
	assert.Equal(t, ada.Subject, e.UserIdentifier)
	assert.Equal(t, ada.Email, e.UserEmail)
	assert.Equal(t, "Ada", e.FirstName)
	assert.Equal(t, "Lovelace", e.LastName)
	assert.False(t, e.Agreements[0].HasClicked, "the EULA was not opened")
	assert.True(t, e.Agreements[1].HasClicked, "the LLM credits terms were opened")
	record := storedRecord(t, home)
	assert.NotEmpty(t, record.EULA.AgreementID)
	assert.Equal(t, record.EULA.AgreementID, record.FreeModelTerms.AgreementID)
}

func TestAddingYourOwnModelRecordsTheSameAgreement(t *testing.T) {
	s, fake, home := signedIn(t)

	_, err := s.Accept(t.Context(), testActor, false, nil)

	require.NoError(t, err)
	executions := fake.Executions()
	require.Len(t, executions, 1)
	assert.Equal(t, testEULAClickwrap, executions[0].ClickwrapPublicID)
	assert.Nil(t, storedRecord(t, home).FreeModelTerms)
}

func TestAgreeingCreatesNoSecondContractWhenSpotDraftAlreadyHoldsOne(t *testing.T) {
	s, fake, home := signedIn(t)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)

	_, err := s.Accept(t.Context(), testActor, false, nil)

	require.NoError(t, err)
	assert.Empty(t, fake.Executions())
	assert.True(t, s.Active())
	assert.Empty(t, storedRecord(t, home).EULA.AgreementID, "SpotDraft does not say which contract it holds")
}

func TestAConsentCheckThatFailsRecordsNothingAndTheNextAttemptFindsTheAgreement(t *testing.T) {
	s, fake, home := signedIn(t)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)
	fake.FailNext(spotdrafttest.OpConsentStatus, http.StatusServiceUnavailable, "")

	_, err := s.Accept(t.Context(), testActor, false, nil)

	require.ErrorIs(t, err, ErrAgreementNotRecorded)
	assert.Empty(t, fake.Executions(), "no contract is sent without an answer from the check")
	assert.False(t, s.Active())
	assert.Nil(t, storedRecord(t, home).EULA)

	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)
	assert.Empty(t, fake.Executions(), "the agreement SpotDraft already held is not sent again")
	assert.True(t, s.Active())
}

func TestAnAgreementToAnEarlierVersionStillCountsEvenWhenSpotDraftAsksForItAgain(t *testing.T) {
	s, fake, _ := signedIn(t)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)
	fake.PublishVersion(testEULAClickwrap, "2.0")

	terms, err := s.Terms(t.Context())
	require.NoError(t, err)
	assert.True(t, terms.Agreed)
	assert.Equal(t, "2.0", terms.Documents[0].Version)

	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)
	assert.Empty(t, fake.Executions(), "no new contract for the new version")
	assert.True(t, s.Active())
}

func TestSomeoneNewAgreesToTheVersionPublishedLast(t *testing.T) {
	s, fake, _ := signedIn(t)
	published := fake.PublishVersion(testEULAClickwrap, "2.0")

	terms, err := s.Terms(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "2.0", terms.Documents[0].Version)

	_, err = s.Accept(t.Context(), testActor, false, nil)
	require.NoError(t, err)
	executions := fake.Executions()
	require.Len(t, executions, 1)
	assert.Equal(t, published.Agreements[0].CurrentVersion.ID, executions[0].Agreements[0].VersionID)
}

func TestEitherPathStaysLockedUntilSpotDraftRecordsTheEULA(t *testing.T) {
	for _, freeModel := range []bool{false, true} {
		home := t.TempDir()
		s, platform := newServiceWithPlatform(t, home, newFakeSolaceID().signsInAs(ada))
		_, err := signIn(t, s)
		require.NoError(t, err)
		fake, _ := spotDraftFor(t, home)
		fake.SetDown(true)

		_, err = s.Accept(t.Context(), testActor, freeModel, nil)

		require.ErrorIs(t, err, ErrAgreementNotRecorded)
		assert.False(t, s.Active())
		assert.Nil(t, storedRecord(t, home).EULA, "nothing is recorded locally")
		_, refreshes := platform.state()
		assert.Zero(t, refreshes)

		fake.SetDown(false)
		_, err = s.Accept(t.Context(), testActor, freeModel, nil)
		require.NoError(t, err)
		assert.True(t, s.Active())
	}
}

func TestReviewingTheTermsAgainRecordsTheAgreementSpotDraftLacksWithoutChangingTheRecord(t *testing.T) {
	s, fake, home := signedIn(t)
	_, err := s.Accept(t.Context(), testActor, true, nil)
	require.NoError(t, err)
	before := storedRecord(t, home)
	fake.Reset()

	terms, err := s.Terms(t.Context())
	require.NoError(t, err)
	require.False(t, terms.Agreed)
	_, err = s.Accept(t.Context(), testActor, true, nil)

	require.NoError(t, err)
	executions := fake.Executions()
	require.Len(t, executions, 1)
	assert.Equal(t, testEULAClickwrap, executions[0].ClickwrapPublicID)
	assert.Equal(t, before, storedRecord(t, home))
}

func TestTheNameSolaceIDGaveIsSplitIntoFirstAndLast(t *testing.T) {
	for name, want := range map[string][2]string{
		"Ada Lovelace":           {"Ada", "Lovelace"},
		"  Ada   King Lovelace ": {"Ada", "King Lovelace"},
		"Ada":                    {"Ada", ""},
		"":                       {"", ""},
	} {
		first, last := splitName(name)
		assert.Equal(t, want, [2]string{first, last}, name)
	}
}
