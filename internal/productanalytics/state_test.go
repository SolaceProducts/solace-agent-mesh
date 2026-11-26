// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func clearsMarkerOnDenial(consentState string) bool {
	return consentState == string(ConsentDenied)
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Now()} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type captureLogs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *captureLogs) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureLogs) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *captureLogs) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *captureLogs) WithGroup(string) slog.Handler      { return c }

func (c *captureLogs) matching(msg string) []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []slog.Record
	for _, r := range c.records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

type emptyMarkerStore struct {
	consent       string
	ensureCalls   int
	bindingSecret string
}

func (e *emptyMarkerStore) EnsureIdentity(context.Context) (string, string, error) {
	e.ensureCalls++
	return "", "", nil
}

func (e *emptyMarkerStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	if e.bindingSecret == observed {
		e.bindingSecret = replacement
	}
	return e.bindingSecret, nil
}

func (e *emptyMarkerStore) ConsentState(context.Context) (string, error) { return e.consent, nil }

func (e *emptyMarkerStore) SetConsent(context.Context, string) error { return nil }

func consentOf(t *testing.T, s *PersistedState) ConsentState {
	t.Helper()
	state, err := s.ConsentWithError(context.Background())
	if err != nil {
		t.Fatalf("consent read failed: %v", err)
	}
	return state
}

type fakeStateStore struct {
	installID     string
	bindingSecret string
	consent       string
	err           error
	installErr    error
	secretErr     error
	ensureCalls   int
	consentCalls  int
	setCalls      int
	replaceCalls  int
	minted        int
}

func (f *fakeStateStore) EnsureIdentity(context.Context) (string, string, error) {
	f.ensureCalls++
	if err := errors.Join(f.err, f.installErr); err != nil {
		return "", "", err
	}
	if f.installID == "" {
		if f.consent == string(ConsentDenied) {
			return "", "", nil
		}
		f.minted++
		f.installID = fmt.Sprintf("11111111-1111-1111-1111-%012d", f.minted)
	}
	return f.installID, f.bindingSecret, nil
}

func (f *fakeStateStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	f.replaceCalls++
	if err := errors.Join(f.err, f.secretErr); err != nil {
		return "", err
	}
	if f.consent == string(ConsentDenied) {
		return "", nil
	}
	if f.bindingSecret == observed {
		f.bindingSecret = replacement
	}
	return f.bindingSecret, nil
}

func (f *fakeStateStore) ConsentState(context.Context) (string, error) {
	f.consentCalls++
	if f.err != nil {
		return "", f.err
	}
	return f.consent, nil
}

func (f *fakeStateStore) SetConsent(_ context.Context, consentState string) error {
	f.setCalls++
	if f.err != nil {
		return f.err
	}
	f.consent = consentState
	if clearsMarkerOnDenial(consentState) {
		f.installID = ""
		f.bindingSecret = ""
	}
	return nil
}

func TestPersistedStateUnsetCollects(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{}, nil)
	if got := consentOf(t, s); got != ConsentUnset {
		t.Errorf("consent = %q, want unset", got)
	}
	if !s.Enabled(context.Background()) {
		t.Error("an install with no decision recorded must still collect")
	}
}

func TestPersistedStateGrantedEnables(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{consent: string(ConsentGranted)}, nil)
	if !s.Enabled(context.Background()) {
		t.Error("granted consent must permit collection")
	}
}

func TestPersistedStateDeniedDisables(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{consent: string(ConsentDenied)}, nil)
	if s.Enabled(context.Background()) {
		t.Error("denied consent must not permit collection")
	}
}

func TestPersistedStateReadErrorFailsClosed(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{err: errors.New("db down")}, nil)
	if got, err := s.ConsentWithError(context.Background()); err == nil {
		t.Errorf("consent on a failed read = %q with no error, want the failure surfaced", got)
	}
	if s.Enabled(context.Background()) {
		t.Error("a failed consent read must deny collection")
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("install id on a failed consent read = %q, want empty", got)
	}
}

func TestPersistedStateCachesConsentReads(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	for i := 0; i < 5; i++ {
		s.Enabled(context.Background())
	}
	if store.consentCalls != 1 {
		t.Errorf("consent reads = %d, want 1 (cached)", store.consentCalls)
	}
}

func TestPersistedStateConsentCacheExpires(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	clock := newTestClock()
	s.now = clock.now

	if !s.Enabled(context.Background()) {
		t.Fatal("precondition: a granted install should collect")
	}

	store.consent = string(ConsentDenied)
	if !s.Enabled(context.Background()) {
		t.Error("the cached decision should still be served inside the TTL")
	}
	if store.consentCalls != 1 {
		t.Errorf("consent reads = %d, want 1 inside the TTL", store.consentCalls)
	}

	clock.advance(cacheTTL)
	if s.Enabled(context.Background()) {
		t.Error("a denial recorded elsewhere must take effect once the cache expires")
	}
}

func TestPersistedStateRemoteWithdrawalDislodgesTheCachedMarker(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	clock := newTestClock()
	s.now = clock.now

	before := s.Identity(context.Background()).Marker
	if before == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	store.consent = string(ConsentDenied)
	store.installID = ""
	clock.advance(cacheTTL)
	if s.Enabled(context.Background()) {
		t.Fatal("the withdrawal should have reached this process once the cache expired")
	}

	store.consent = string(ConsentGranted)
	clock.advance(cacheTTL)

	after := s.Identity(context.Background()).Marker
	if after == before {
		t.Errorf("marker after a withdrawal elsewhere = %q, want anything but the discarded one", after)
	}
	if after == "" {
		t.Error("a collecting install must still hold a marker after the re-read")
	}
}

func TestPersistedStateLocalDecisionAfterARemoteWithdrawalDropsTheMarker(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	clock := newTestClock()
	s.now = clock.now

	before := s.Identity(context.Background()).Marker
	if before == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	store.consent = string(ConsentDenied)
	store.installID = ""

	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if got := s.Identity(context.Background()).Marker; got == before {
		t.Errorf("marker after recording the opt-in = %q, want anything but the discarded one", got)
	}
}

func TestPersistedStateEmptyMarkerYieldsNoIdentity(t *testing.T) {
	store := &emptyMarkerStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)

	for i := range 2 {
		if got := s.Identity(context.Background()).Marker; got != "" {
			t.Fatalf("call %d: marker = %q, want empty", i, got)
		}
	}
	if store.ensureCalls != 2 {
		t.Errorf("marker reads = %d, want one per call", store.ensureCalls)
	}
}

func TestPersistedStateReadFailureLogsOncePerWindow(t *testing.T) {
	logs := &captureLogs{}
	store := &fakeStateStore{err: errors.New("db down")}
	s := NewPersistedState(store, slog.New(logs))
	clock := newTestClock()
	s.now = clock.now

	const msg = "product analytics consent read failed, collection stopped"
	for range 5 {
		s.Enabled(context.Background())
	}
	if got := len(logs.matching(msg)); got != 1 {
		t.Errorf("lines within one window = %d, want 1", got)
	}

	clock.advance(cacheTTL)
	s.Enabled(context.Background())
	if got := len(logs.matching(msg)); got != 2 {
		t.Errorf("lines after the window elapsed = %d, want 2 — sustained loss must stay visible", got)
	}
}

func TestPersistedStateUnrecognizedValueLogsWithoutTheValue(t *testing.T) {
	logs := &captureLogs{}
	const tampered = "granted_but_not_really"
	s := NewPersistedState(&fakeStateStore{consent: tampered}, slog.New(logs))

	if s.Enabled(context.Background()) {
		t.Fatal("precondition: an uninterpretable value must not permit collection")
	}

	records := logs.matching("product analytics consent state not recognized, treating as denied")
	if len(records) != 1 {
		t.Fatalf("lines = %d, want 1", len(records))
	}
	records[0].Attrs(func(a slog.Attr) bool {
		if strings.Contains(a.Value.String(), tampered) {
			t.Errorf("attribute %q carries the stored value", a.Key)
		}
		return true
	})
	if records[0].Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", records[0].Level)
	}
}

func TestPersistedStateUnrecognizedValueIsThrottled(t *testing.T) {
	logs := &captureLogs{}
	const tampered = "not_a_real_state"
	store := &fakeStateStore{consent: tampered}
	s := NewPersistedState(store, slog.New(logs))
	clock := newTestClock()
	s.now = clock.now

	const calls = 5
	for range calls {
		s.Enabled(context.Background())
	}
	records := logs.matching("product analytics consent state not recognized, treating as denied")
	if len(records) != 1 {
		t.Errorf("lines within one window = %d, want 1 throttled line", len(records))
	}

	clock.advance(cacheTTL)
	for range calls {
		s.Enabled(context.Background())
	}
	if got := len(logs.matching("product analytics consent state not recognized, treating as denied")); got != 2 {
		t.Errorf("lines after the window elapsed = %d, want 2 — unrecognized must stay visible", got)
	}
}

func TestPersistedStateRetriesAfterReadFailure(t *testing.T) {
	store := &fakeStateStore{err: errors.New("db down")}
	s := NewPersistedState(store, nil)
	if _, err := s.ConsentWithError(context.Background()); err == nil {
		t.Fatal("precondition: the first read should have failed")
	}

	store.err = nil
	store.consent = string(ConsentGranted)
	if !s.Enabled(context.Background()) {
		t.Error("expected the retry to observe the granted consent")
	}
}

func TestPersistedStateSetConsentRefreshesCache(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentDenied)}
	s := NewPersistedState(store, nil)
	if s.Enabled(context.Background()) {
		t.Fatal("precondition: a denied install should start off")
	}

	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled(context.Background()) {
		t.Error("granting consent must take effect without a restart")
	}
	if store.consent != string(ConsentGranted) {
		t.Errorf("persisted consent = %q, want granted", store.consent)
	}
}

func TestPersistedStateDenyDiscardsMarker(t *testing.T) {
	store := &fakeStateStore{}
	s := NewPersistedState(store, nil)
	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	granted := s.Identity(context.Background()).Marker
	if granted == "" {
		t.Fatal("precondition: granting consent should issue a marker")
	}

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("install id after withdrawal = %q, want empty", got)
	}
	if store.installID != "" {
		t.Errorf("persisted install id after withdrawal = %q, want empty", store.installID)
	}
}

func TestPersistedStateOptingBackInIssuesFreshMarker(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{}, nil)
	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	first := s.Identity(context.Background()).Marker
	if first == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	second := s.Identity(context.Background()).Marker
	if second == "" || second == first {
		t.Errorf("install id after opting back in = %q, want a fresh one (was %q)", second, first)
	}
}

func TestPersistedStateDecidingToStayOnKeepsTheMarker(t *testing.T) {
	for _, prior := range []ConsentState{ConsentUnset, ConsentGranted} {
		t.Run(string(prior), func(t *testing.T) {
			store := &fakeStateStore{consent: string(prior)}
			s := NewPersistedState(store, nil)
			held := s.Identity(context.Background()).Marker
			if held == "" {
				t.Fatalf("precondition: a %s install should hold a marker", prior)
			}

			if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
				t.Fatal(err)
			}
			if got := s.Identity(context.Background()).Marker; got != held {
				t.Errorf("install id after answering = %q, want the held %q", got, held)
			}
			if store.installID != held {
				t.Errorf("persisted install id = %q, want the held %q", store.installID, held)
			}
		})
	}
}

func TestPersistedStateWritingUnsetKeepsTheMarker(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	held := s.Identity(context.Background()).Marker
	if held == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	if err := s.SetConsent(context.Background(), ConsentUnset); err != nil {
		t.Fatal(err)
	}
	if got := consentOf(t, s); got != ConsentUnset {
		t.Errorf("consent = %q, want unset", got)
	}
	if got := s.Identity(context.Background()).Marker; got != held {
		t.Errorf("install id after writing unset = %q, want the held %q", got, held)
	}
	if store.installID != held {
		t.Errorf("persisted install id = %q, want the held %q", store.installID, held)
	}
}

func TestPersistedStateRegrantOverAGrantKeepsTheMarker(t *testing.T) {
	store := &fakeStateStore{}
	s := NewPersistedState(store, nil)
	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	first := s.Identity(context.Background()).Marker
	if first == "" {
		t.Fatal("precondition: granting consent should issue a marker")
	}

	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if second := s.Identity(context.Background()).Marker; second != first {
		t.Errorf("install id after re-affirming the grant = %q, want the existing %q", second, first)
	}
	if store.installID != first {
		t.Errorf("persisted install id = %q, want the existing %q", store.installID, first)
	}
}

func TestPersistedStateGrantNeverMintsOverTheStoredMarker(t *testing.T) {
	const held = "22222222-2222-2222-2222-222222222222"
	store := &fakeStateStore{consent: string(ConsentGranted), installID: held}
	s := NewPersistedState(store, nil)

	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if store.installID != held {
		t.Errorf("install id = %q, want the held %q — a grant must not split the install", store.installID, held)
	}
}

func TestPersistedStateSetConsentRejectsUnknownState(t *testing.T) {
	store := &fakeStateStore{}
	s := NewPersistedState(store, nil)
	if err := s.SetConsent(context.Background(), ConsentState("maybe")); err == nil {
		t.Error("expected an error for an unknown consent state")
	}
	if store.setCalls != 0 {
		t.Error("an invalid state must not reach the store")
	}
}

func TestPersistedStateInstallIDIsReadFromTheStoreEachTime(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	first := s.Identity(context.Background()).Marker
	second := s.Identity(context.Background()).Marker

	if first == "" || first != second {
		t.Errorf("install id unstable: %q then %q", first, second)
	}
	if store.ensureCalls != 2 {
		t.Errorf("marker reads = %d, want one per call — the marker must not be cached", store.ensureCalls)
	}
}

func TestPersistedStateRemoteWithdrawalTakesTheMarkerImmediately(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)

	if s.Identity(context.Background()).Marker == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	store.consent = string(ConsentDenied)
	store.installID = ""
	if !s.Enabled(context.Background()) {
		t.Fatal("precondition: the cached decision should still read as collecting")
	}

	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("marker after a withdrawal elsewhere = %q, want empty on the next batch", got)
	}
}

func TestPersistedStateConsentCheckDoesNotMintMarker(t *testing.T) {
	for _, state := range []ConsentState{ConsentUnset, ConsentDenied, ConsentGranted} {
		t.Run(string(state), func(t *testing.T) {
			store := &fakeStateStore{consent: string(state)}
			s := NewPersistedState(store, nil)
			s.Enabled(context.Background())

			if store.ensureCalls != 0 {
				t.Errorf("marker reads = %d, want 0 — the consent check must not mint", store.ensureCalls)
			}
		})
	}
}

func TestPersistedStateInstallIDErrorYieldsEmpty(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted), installErr: errors.New("db down")}
	s := NewPersistedState(store, nil)
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("install id on failure = %q, want empty", got)
	}
}

func TestPersistedStateInstallIDEmptyAfterDenial(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentDenied), installID: "11111111-1111-1111-1111-111111111111"}
	s := NewPersistedState(store, nil)
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("install id for a denied install = %q, want empty", got)
	}
	if store.ensureCalls != 0 {
		t.Errorf("marker reads = %d, want 0 for a denied install", store.ensureCalls)
	}
}

func TestPersistedStateUnsetInstallIDIsIssued(t *testing.T) {
	const held = "11111111-1111-1111-1111-111111111111"
	store := &fakeStateStore{consent: string(ConsentUnset), installID: held}
	s := NewPersistedState(store, nil)
	if got := s.Identity(context.Background()).Marker; got != held {
		t.Errorf("install id for an unanswered install = %q, want %q", got, held)
	}
}

func TestNormalizeConsent(t *testing.T) {
	cases := []struct {
		raw            string
		want           ConsentState
		wantRecognized bool
	}{
		{"granted", ConsentGranted, true},
		{"denied", ConsentDenied, true},
		{"unset", ConsentUnset, true},
		{"", ConsentUnset, true},
		{"nonsense", ConsentDenied, false},
		{"GRANTED", ConsentDenied, false},
		{"granted ", ConsentDenied, false},
		{"DENIED", ConsentDenied, false},
	}
	for _, tc := range cases {
		got, recognized := normalizeConsent(tc.raw)
		if got != tc.want {
			t.Errorf("normalizeConsent(%q) = %q, want %q", tc.raw, got, tc.want)
		}
		if recognized != tc.wantRecognized {
			t.Errorf("normalizeConsent(%q) recognized = %v, want %v", tc.raw, recognized, tc.wantRecognized)
		}
	}
}

func TestPersistedStateUnrecognizedStoredValueDisables(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{consent: "granted_limited"}, nil)
	if s.Enabled(context.Background()) {
		t.Error("a consent state this build cannot interpret must not permit collection")
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("install id = %q, want empty for an uninterpretable decision", got)
	}
}

type readGate struct {
	entered chan struct{}
	release chan struct{}
}

func newReadGate() readGate {
	return readGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g readGate) suspendUntilReleased() {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
}

type blockingStateStore struct {
	mu            sync.Mutex
	consent       string
	installID     string
	bindingSecret string

	consentGate readGate
	markerGate  readGate
	writeGate   *readGate
}

func newBlockingStateStore() *blockingStateStore {
	return &blockingStateStore{consentGate: newReadGate(), markerGate: newReadGate()}
}

func (b *blockingStateStore) ConsentState(context.Context) (string, error) {
	b.mu.Lock()
	captured := b.consent
	b.mu.Unlock()
	b.consentGate.suspendUntilReleased()
	return captured, nil
}

func (b *blockingStateStore) EnsureIdentity(context.Context) (string, string, error) {
	b.mu.Lock()
	captured := b.installID
	b.mu.Unlock()
	b.markerGate.suspendUntilReleased()
	return captured, "", nil
}

func (b *blockingStateStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	if b.bindingSecret == observed {
		b.bindingSecret = replacement
	}
	return b.bindingSecret, nil
}

func (b *blockingStateStore) SetConsent(_ context.Context, consentState string) error {
	if b.writeGate != nil {
		b.writeGate.suspendUntilReleased()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consent = consentState
	if clearsMarkerOnDenial(consentState) {
		b.installID = ""
	}
	return nil
}

func (b *blockingStateStore) currentInstallID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.installID
}

func TestPersistedStateDenyBeatsInFlightRead(t *testing.T) {
	store := newBlockingStateStore()
	store.consent = string(ConsentGranted)
	s := NewPersistedState(store, nil)

	inFlight := make(chan ConsentState, 1)
	go func() {
		state, _ := s.ConsentWithError(context.Background())
		inFlight <- state
	}()
	<-store.consentGate.entered

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	close(store.consentGate.release)

	if got := <-inFlight; got != ConsentDenied {
		t.Errorf("in-flight read returned %q, want denied — a stale read overwrote the user's decision", got)
	}
	if got := consentOf(t, s); got != ConsentDenied {
		t.Errorf("cached consent = %q, want denied", got)
	}
	if s.Enabled(context.Background()) {
		t.Error("collection must stay off after the user denied")
	}
}

func TestPersistedStateDenyBeatsAnInFlightReadThatOutlastsTheTTL(t *testing.T) {
	store := newBlockingStateStore()
	store.consent = string(ConsentGranted)
	s := NewPersistedState(store, nil)
	clock := newTestClock()
	s.now = clock.now

	inFlight := make(chan ConsentState, 1)
	go func() {
		state, _ := s.ConsentWithError(context.Background())
		inFlight <- state
	}()
	<-store.consentGate.entered

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * cacheTTL)
	close(store.consentGate.release)

	if got := <-inFlight; got != ConsentDenied {
		t.Errorf("in-flight read returned %q, want denied — a stale read outlived the decision", got)
	}
	if s.Enabled(context.Background()) {
		t.Error("collection must stay off after the user denied")
	}
}

func TestPersistedStateStayingOnKeepsTheInstallsIdentity(t *testing.T) {
	const held = "11111111-1111-1111-1111-111111111111"
	store := newBlockingStateStore()
	store.consent = string(ConsentUnset)
	store.installID = held
	close(store.consentGate.release)
	s := NewPersistedState(store, nil)

	inFlight := make(chan string, 1)
	go func() { inFlight <- s.Identity(context.Background()).Marker }()
	<-store.markerGate.entered

	if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	close(store.markerGate.release)
	<-inFlight

	if got := store.currentInstallID(); got != held {
		t.Errorf("persisted marker = %q, want the held %q — recording a decision replaced it", got, held)
	}
	if got := s.Identity(context.Background()).Marker; got != held {
		t.Errorf("marker the install reports = %q, want the held %q", got, held)
	}
}

func TestPersistedStateDenyIsNotReversedByAReadDuringItsOwnWrite(t *testing.T) {
	const beforeDenial = "11111111-1111-1111-1111-111111111111"
	store := newBlockingStateStore()
	store.consent = string(ConsentUnset)
	store.installID = beforeDenial
	close(store.consentGate.release)
	close(store.markerGate.release)
	writeGate := newReadGate()
	store.writeGate = &writeGate
	s := NewPersistedState(store, nil)
	clock := newTestClock()
	s.now = clock.now

	denied := make(chan error, 1)
	go func() { denied <- s.SetConsent(context.Background(), ConsentDenied) }()
	<-writeGate.entered

	clock.advance(2 * cacheTTL)

	if s.Enabled(context.Background()) {
		t.Error("a read inside the denial's own write must not turn collection back on")
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("marker inside the denial's own write = %q, want empty", got)
	}

	close(writeGate.release)
	if err := <-denied; err != nil {
		t.Fatal(err)
	}
	if s.Enabled(context.Background()) {
		t.Error("collection must stay off once the denial has committed")
	}
}

func TestPersistedStateDenialDuringAMarkerReadYieldsNoMarker(t *testing.T) {
	const beforeDenial = "11111111-1111-1111-1111-111111111111"
	store := newBlockingStateStore()
	store.consent = string(ConsentGranted)
	store.installID = beforeDenial
	close(store.consentGate.release)
	s := NewPersistedState(store, nil)

	inFlight := make(chan string, 1)
	go func() { inFlight <- s.Identity(context.Background()).Marker }()
	<-store.markerGate.entered

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	close(store.markerGate.release)

	if got := <-inFlight; got != "" {
		t.Errorf("marker read straddling a denial returned %q, want empty — the denial discarded it", got)
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("marker after the denial = %q, want empty", got)
	}
}

type observeOnSetStore struct {
	consent       string
	installID     string
	bindingSecret string
	setErr        error
	onSet         func()
}

func (o *observeOnSetStore) EnsureIdentity(context.Context) (string, string, error) {
	if o.installID == "" {
		o.installID = "22222222-2222-2222-2222-222222222222"
	}
	return o.installID, o.bindingSecret, nil
}

func (o *observeOnSetStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	if o.bindingSecret == observed {
		o.bindingSecret = replacement
	}
	return o.bindingSecret, nil
}

func (o *observeOnSetStore) ConsentState(context.Context) (string, error) {
	return o.consent, nil
}

func (o *observeOnSetStore) SetConsent(_ context.Context, consentState string) error {
	if o.onSet != nil {
		o.onSet()
	}
	if o.setErr != nil {
		return o.setErr
	}
	o.consent = consentState
	if clearsMarkerOnDenial(consentState) {
		o.installID = ""
	}
	return nil
}

func TestPersistedStateWithdrawalIsVisibleBeforeTheStoreWrite(t *testing.T) {
	store := &observeOnSetStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	if got := consentOf(t, s); got != ConsentGranted {
		t.Fatalf("precondition: consent = %q, want granted", got)
	}

	var observedConsent ConsentState
	var observedMarker string
	store.onSet = func() {
		observedConsent, _ = s.ConsentWithError(context.Background())
		observedMarker = s.Identity(context.Background()).Marker
	}

	if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if observedConsent != ConsentDenied {
		t.Errorf("consent during the store write = %q, want denied — the gate was still open", observedConsent)
	}
	if observedMarker != "" {
		t.Errorf("marker during the store write = %q, want empty", observedMarker)
	}
}

func TestPersistedStateFailedGrantLeavesCollectionOff(t *testing.T) {
	store := &observeOnSetStore{consent: string(ConsentDenied), setErr: errors.New("write failed")}
	s := NewPersistedState(store, nil)

	if err := s.SetConsent(context.Background(), ConsentGranted); err == nil {
		t.Fatal("expected the store error to surface")
	}
	if s.Enabled(context.Background()) {
		t.Error("collection must stay off after a grant that failed to persist")
	}
	if got := s.Identity(context.Background()).Marker; got != "" {
		t.Errorf("marker = %q, want empty after a failed grant", got)
	}
}

type concurrentWriteStore struct {
	mu            sync.Mutex
	consent       string
	installID     string
	bindingSecret string

	grantWrote   chan struct{}
	releaseGrant chan struct{}
	denyWrote    chan struct{}
}

func newConcurrentWriteStore() *concurrentWriteStore {
	return &concurrentWriteStore{
		grantWrote:   make(chan struct{}),
		releaseGrant: make(chan struct{}),
		denyWrote:    make(chan struct{}),
	}
}

func (c *concurrentWriteStore) EnsureIdentity(context.Context) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.installID, c.bindingSecret, nil
}

func (c *concurrentWriteStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	if c.bindingSecret == observed {
		c.bindingSecret = replacement
	}
	return c.bindingSecret, nil
}

func (c *concurrentWriteStore) ConsentState(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.consent, nil
}

func (c *concurrentWriteStore) SetConsent(_ context.Context, consentState string) error {
	c.mu.Lock()
	c.consent = consentState
	if clearsMarkerOnDenial(consentState) {
		c.installID = ""
	}
	c.mu.Unlock()

	if clearsMarkerOnDenial(consentState) {
		close(c.denyWrote)
		return nil
	}
	close(c.grantWrote)
	<-c.releaseGrant
	return nil
}

func (c *concurrentWriteStore) persisted() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.consent
}

func TestPersistedStateConcurrentWritesKeepCacheAgreeingWithStore(t *testing.T) {
	store := newConcurrentWriteStore()
	s := NewPersistedState(store, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.SetConsent(context.Background(), ConsentGranted); err != nil {
			t.Error(err)
		}
	}()
	<-store.grantWrote

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.SetConsent(context.Background(), ConsentDenied); err != nil {
			t.Error(err)
		}
	}()

	select {
	case <-store.denyWrote:
		t.Error("withdrawal committed while the grant was still publishing — the two writes interleaved")
	case <-time.After(100 * time.Millisecond):
	}

	close(store.releaseGrant)
	wg.Wait()

	persisted := store.persisted()
	if got := consentOf(t, s); string(got) != persisted {
		t.Errorf("cached consent %q disagrees with persisted %q", got, persisted)
	}
	if persisted == string(ConsentDenied) {
		if s.Enabled(context.Background()) {
			t.Error("collection still enabled after the withdrawal persisted")
		}
		if got := s.Identity(context.Background()).Marker; got != "" {
			t.Errorf("marker = %q, want empty after the withdrawal persisted", got)
		}
	}
}

func TestPersistedStateFailedWithdrawalDoesNotStickInTheCache(t *testing.T) {
	store := &observeOnSetStore{consent: string(ConsentGranted), setErr: errors.New("write failed")}
	s := NewPersistedState(store, nil)
	if got := consentOf(t, s); got != ConsentGranted {
		t.Fatalf("precondition: consent = %q, want granted", got)
	}

	if err := s.SetConsent(context.Background(), ConsentDenied); err == nil {
		t.Fatal("expected the store error to surface")
	}
	if got := consentOf(t, s); got != ConsentGranted {
		t.Errorf("consent after a failed withdrawal = %q, want granted re-read from the store", got)
	}
	if got := s.Identity(context.Background()).Marker; got == "" {
		t.Error("install id after a failed withdrawal = empty, want the marker the store still holds")
	}
}

func TestCacheTTLStaysShortEnoughToConverge(t *testing.T) {
	if cacheTTL > time.Minute {
		t.Errorf("cacheTTL = %s: `sam analytics disable` would not reach a running gateway promptly", cacheTTL)
	}
	if cacheTTL <= 0 {
		t.Errorf("cacheTTL = %s: every consent check would hit the database", cacheTTL)
	}
}

func expireCache(s *PersistedState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consentReadAt = time.Now().Add(-2 * cacheTTL)
}

func TestPersistedStateObservesAnExternalWithdrawal(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	if !s.Enabled(t.Context()) {
		t.Fatal("granted consent should permit collection")
	}

	store.consent = string(ConsentDenied)
	expireCache(s)

	if s.Enabled(t.Context()) {
		t.Error("collection continued after another process withdrew consent")
	}
	if got := s.Identity(t.Context()).Marker; got != "" {
		t.Errorf("install id = %q after withdrawal, want empty", got)
	}
}

func TestPersistedStateObservesAnExternalGrant(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentDenied), installID: ""}
	s := NewPersistedState(store, nil)
	if s.Enabled(t.Context()) {
		t.Fatal("denied consent should not permit collection")
	}

	store.consent = string(ConsentGranted)
	store.installID = "11111111-1111-1111-1111-111111111111"
	expireCache(s)

	if !s.Enabled(t.Context()) {
		t.Error("collection did not resume after another process granted consent")
	}
	if got := s.Identity(t.Context()).Marker; got != store.installID {
		t.Errorf("install id = %q after external grant, want %q", got, store.installID)
	}
}

func TestPersistedStateObservesAnExternalMarkerRotation(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted), installID: "11111111-1111-1111-1111-111111111111"}
	s := NewPersistedState(store, nil)
	first := s.Identity(t.Context()).Marker

	store.installID = "22222222-2222-2222-2222-222222222222"
	expireCache(s)

	second := s.Identity(t.Context()).Marker
	if second != store.installID {
		t.Errorf("install id = %q, want the rotated %q", second, store.installID)
	}
	if first == second {
		t.Error("the cached marker survived a rotation by another process")
	}
}

func TestPersistedStateCachesTheDecisionButNotTheMarker(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	const batches = 5
	for range batches {
		s.Enabled(t.Context())
		s.Identity(t.Context())
	}
	if store.consentCalls != 1 {
		t.Errorf("consent reads = %d, want 1 within the TTL", store.consentCalls)
	}
	if store.ensureCalls != batches {
		t.Errorf("marker reads = %d, want one per batch", store.ensureCalls)
	}
}

func TestEnabledFreshSeesAnExternalWithdrawalImmediately(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	if !s.Enabled(t.Context()) {
		t.Fatal("granted consent should permit collection")
	}

	store.consent = string(ConsentDenied)

	if !s.Enabled(t.Context()) {
		t.Fatal("precondition: the cached grant should still be served inside the TTL")
	}
	if s.EnabledFresh(t.Context()) {
		t.Error("EnabledFresh served a cached grant after another process withdrew consent")
	}
	if s.Enabled(t.Context()) {
		t.Error("the cache was not refreshed by the bypassing read")
	}
}

func TestEnabledFreshRefusesAnUnreadableDecision(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, slog.New(slog.DiscardHandler))
	if !s.Enabled(t.Context()) {
		t.Fatal("precondition: granted consent should permit collection")
	}

	store.consent = string(ConsentDenied)
	store.err = errors.New("db down")

	if s.EnabledFresh(t.Context()) {
		t.Error("collection continued on a consent read that failed, so an opt-out recorded elsewhere was ignored")
	}
}

func TestEnabledFreshStillCollectsWithNoDecisionRecorded(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{consent: string(ConsentUnset)}, slog.New(slog.DiscardHandler))

	if !s.EnabledFresh(t.Context()) {
		t.Error("an install that has answered nothing must still collect")
	}
}

func TestEnabledFreshFailsClosedWithNoCache(t *testing.T) {
	s := NewPersistedState(&fakeStateStore{err: errors.New("db down")}, nil)
	if s.EnabledFresh(t.Context()) {
		t.Error("EnabledFresh permitted collection with no readable decision")
	}
}

func TestEnabledFreshDeniesOnAStaleGrantWithADeadStore(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	s := NewPersistedState(store, nil)
	if !s.Enabled(t.Context()) {
		t.Fatal("precondition: the grant should be cached")
	}

	expireCache(s)
	store.err = errors.New("db down")

	if s.EnabledFresh(t.Context()) {
		t.Error("a stale cached grant permitted collection while the store was unreadable")
	}
}

func TestEnabledFreshReadsTheStoreOnceOnFailure(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeStateStore, *PersistedState)
	}{
		{"never read", func(*fakeStateStore, *PersistedState) {}},
		{"stale cache", func(store *fakeStateStore, s *PersistedState) {
			store.consent = string(ConsentGranted)
			s.Enabled(context.Background())
			expireCache(s)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStateStore{}
			s := NewPersistedState(store, nil)
			tc.setup(store, s)
			before := store.consentCalls

			store.err = errors.New("db down")
			s.EnabledFresh(t.Context())

			if got := store.consentCalls - before; got != 1 {
				t.Errorf("store reads on the failure path = %d, want 1", got)
			}
		})
	}
}

type sequencedStateStore struct {
	mu            sync.Mutex
	next          int
	values        []string
	entered       []chan struct{}
	released      []chan struct{}
	bindingSecret string
}

func newSequencedStateStore(values ...string) *sequencedStateStore {
	s := &sequencedStateStore{values: values}
	for range values {
		s.entered = append(s.entered, make(chan struct{}))
		s.released = append(s.released, make(chan struct{}))
	}
	return s
}

func (s *sequencedStateStore) ConsentState(context.Context) (string, error) {
	s.mu.Lock()
	i := s.next
	s.next++
	s.mu.Unlock()
	close(s.entered[i])
	<-s.released[i]
	return s.values[i], nil
}

func (s *sequencedStateStore) EnsureIdentity(context.Context) (string, string, error) {
	return "", "", nil
}

func (s *sequencedStateStore) SetConsent(context.Context, string) error { return nil }

func TestEnabledFreshIsNotDefeatedByASlowerConcurrentRead(t *testing.T) {
	store := newSequencedStateStore(string(ConsentGranted), string(ConsentDenied))
	s := NewPersistedState(store, nil)

	stale := make(chan ConsentState, 1)
	go func() {
		state, err := s.ConsentWithError(context.Background())
		if err != nil {
			t.Error(err)
		}
		stale <- state
	}()
	<-store.entered[0]

	fresh := make(chan bool, 1)
	go func() { fresh <- s.EnabledFresh(context.Background()) }()
	<-store.entered[1]

	close(store.released[0])
	if got := <-stale; got != ConsentGranted {
		t.Fatalf("precondition: the earlier read saw %q, want granted", got)
	}
	close(store.released[1])

	if <-fresh {
		t.Error("EnabledFresh permitted collection after its own store read returned denied")
	}
	if got := s.Consent(context.Background()); got == ConsentGranted {
		t.Errorf("cached consent = %q after the store reported denied", got)
	}
}

func (s *sequencedStateStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	if s.bindingSecret == observed {
		s.bindingSecret = replacement
	}
	return s.bindingSecret, nil
}
